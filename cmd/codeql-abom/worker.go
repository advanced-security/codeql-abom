package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/harness"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// lentToken is the one read token a coordinator lent this attempt, for whichever owner it names. It
// is never borrowed from another installation: one is lent only for the task's own repository, and
// the coordinator explains a read it lent nothing for.
type lentToken string

func (t lentToken) Token(context.Context, string) (string, bool, error)  { return string(t), false, nil }
func (t lentToken) AccountToken(context.Context, string) (string, error) { return string(t), nil }

type workerConfig struct {
	attestor, workDir, codeql, qlmcp string
	threads, ram                     int
}

// parseWorkerArgs reads the worker's flags, which execution.Kubernetes writes into every Job.
func parseWorkerArgs(args []string, stderr io.Writer) (workerConfig, error) {
	var c workerConfig
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.attestor, "attestor", "", "socket of the attestor holding this attempt's grant and secrets")
	fs.StringVar(&c.workDir, "work-dir", os.TempDir(), "scratch directory for the checkout and database")
	fs.StringVar(&c.codeql, "codeql", "codeql", "path to the CodeQL CLI")
	fs.IntVar(&c.threads, "threads", 0, "CodeQL threads; 0 uses every core the pod has")
	fs.IntVar(&c.ram, "ram", 0, "CodeQL RAM budget in MB; 0 lets CodeQL decide")
	fs.StringVar(&c.qlmcp, "ql-mcp", harness.DefaultQLMCP, "command line that starts ql-mcp beside a session")
	return c, fs.Parse(args)
}

// runWorker does one granted attempt: fetch, build, query, and hand what it made to its attestor,
// which verified the grant and holds everything the attempt signs and writes with.
func runWorker(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	c, err := parseWorkerArgs(args, stderr)
	if err != nil {
		return err
	}
	if c.attestor == "" {
		return errors.New("worker: name the attestor's socket with --attestor")
	}
	attestor := execution.DialAttestor(c.attestor)
	r := &runner.Runner{CodeQLPath: c.codeql, Threads: c.threads, RAMMB: c.ram, Lock: runner.SharedLock(),
		Server:    &runner.CLIServer{CodeQLPath: c.codeql},
		Evaluator: &runner.QueryServer{CodeQLPath: c.codeql, Threads: c.threads, RAMMB: c.ram}}
	var tokens ghapp.TokenSource
	if t := os.Getenv(execution.TokenSecret); t != "" {
		tokens = lentToken(t)
	}
	e := &expand.Expander{Fetcher: &expand.Checkout{Tokens: tokens}, Builder: r, CodeQL: r, Analyzer: r,
		Decode: expand.Decoder(r.Decode)}
	w := &execution.Worker{Attestor: attestor, Analyze: e.AnalyzeTask, Dir: c.workDir,
		// The pack the grant bound is the only operator pack this process ever sees.
		Pack: func(ctx context.Context, root string) error {
			runner.SetCustomPackRoot(root)
			return r.InstallPack(ctx)
		},
		Test: func(ctx context.Context, _ string, tests []string) (runner.TestResult, error) {
			return r.RunTests(ctx, tests)
		},
		Session: func(ctx context.Context, t execution.SessionTask, in execution.SessionRead,
			report func([]byte) error) (execution.SessionOutcome, error) {
			return runGrantedSession(ctx, c, r, t, in, report)
		}}
	if err := w.Run(ctx, authz.Envelope{}); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "worker: finished its attempt, and its attestor signed the result")
	return nil
}

// runGrantedSession runs the session a grant names, as the single binary runs one, reading the
// snapshot it was handed where the single binary reads the store, talking to the Copilot CLI that
// serves beside it in the pod, which alone holds the session's token, and reporting what it does as it
// does it.
func runGrantedSession(ctx context.Context, c workerConfig, r *runner.Runner, t execution.SessionTask,
	in execution.SessionRead, report func([]byte) error) (execution.SessionOutcome, error) {
	var snap store.AuthoringSnapshot
	if err := json.Unmarshal(in.Snapshot, &snap); err != nil {
		return execution.SessionOutcome{}, fmt.Errorf("reading what the session reads: %w", err)
	}
	if snap.Session.RowID != t.Session {
		return execution.SessionOutcome{}, fmt.Errorf("the snapshot is of session %d, not %d", snap.Session.RowID, t.Session)
	}
	url, token := os.Getenv(execution.AgentURLVariable), os.Getenv(execution.AgentConnectionSecret)
	if url == "" || token == "" {
		return execution.SessionOutcome{}, errors.New("no Copilot CLI serves beside this worker")
	}
	// Prepared elsewhere, the pack the agent works in is the operator pack the session resolves.
	runner.SetCustomPackRoot(t.Workspace)
	tmp, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return execution.SessionOutcome{}, err
	}
	// Where only this container writes: the agent beside it shares the pack the tests are of.
	tested := filepath.Join(tmp, "abom-tests")
	sessions := &harness.Sessions{
		Workspace: t.Workspace,
		Databases: in.Databases,
		Libraries: codeqlLibraries(),
		WorkDir:   filepath.Join(c.workDir, "authoring"),
		Model:     t.Model,
		Lock:      &runner.Lock{},
		Limit:     time.Duration(t.LimitSeconds) * time.Second,
		Version:   r.Version,
		RunTests:  execution.PrivateTests(t.Workspace, tested, r.RunTestsHeldIn),
		TestedAt:  tested,
		Agent: &harness.Copilot{Remote: &harness.RemoteCLI{URL: url, Token: token}, Model: t.Model,
			QLMCP: strings.Fields(c.qlmcp), Serial: relay(), CodeQL: c.codeql, Packs: runner.PackSearchPath(),
			Credits: t.Credits},
	}
	tests, err := sessions.Granted(ctx, snap, t.By, func(entries []store.AuthoringEntry, inflight []harness.Flight) error {
		body, err := json.Marshal(harness.Reported{Entries: entries, InFlight: inflight})
		if err != nil {
			return err
		}
		return report(body)
	})
	return execution.SessionOutcome{Tests: tests}, err
}
