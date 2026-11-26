package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/execution"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// lentToken is the one read token a coordinator lent this attempt, for whichever owner it names. It
// is never borrowed from another installation: one is lent only for the task's own repository, and
// the coordinator explains a read it lent nothing for.
type lentToken string

func (t lentToken) Token(context.Context, string) (string, bool, error)  { return string(t), false, nil }
func (t lentToken) AccountToken(context.Context, string) (string, error) { return string(t), nil }

type workerConfig struct {
	grant, trust, svid, workDir, codeql string
	threads, ram                        int
}

// parseWorkerArgs reads the worker's flags, which execution.Kubernetes writes into every Job.
func parseWorkerArgs(args []string, stderr io.Writer) (workerConfig, error) {
	var c workerConfig
	fs := flag.NewFlagSet("worker", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&c.grant, "grant", "", "file holding the signed task grant")
	fs.StringVar(&c.trust, "trust", "", "directory holding trust.json, the decision point's bundle")
	fs.StringVar(&c.svid, "svid", "/var/run/secrets/spiffe.io", "directory holding this workload's SPIFFE identity")
	fs.StringVar(&c.workDir, "work-dir", os.TempDir(), "scratch directory for the checkout and database")
	fs.StringVar(&c.codeql, "codeql", "codeql", "path to the CodeQL CLI")
	fs.IntVar(&c.threads, "threads", 0, "CodeQL threads; 0 uses every core the pod has")
	fs.IntVar(&c.ram, "ram", 0, "CodeQL RAM budget in MB; 0 lets CodeQL decide")
	return c, fs.Parse(args)
}

// runWorker does one granted attempt: verify the grant, fetch, build, query, upload and attest.
func runWorker(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	c, err := parseWorkerArgs(args, stderr)
	if err != nil {
		return err
	}
	if c.grant == "" || c.trust == "" {
		return errors.New("worker: name the grant with --grant and the decision bundle with --trust")
	}
	body, err := os.ReadFile(c.grant)
	if err != nil {
		return fmt.Errorf("worker: reading the grant: %w", err)
	}
	var grant authz.Envelope
	if err := json.Unmarshal(body, &grant); err != nil {
		return fmt.Errorf("worker: the grant is not an envelope: %w", err)
	}
	bundle, err := authz.ReadBundle(c.trust)
	if err != nil {
		return fmt.Errorf("worker: reading the decision bundle: %w", err)
	}
	identity, err := execution.LoadSVID(c.svid)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}
	objects, err := execution.S3FromEnv(ctx)
	if err != nil {
		return fmt.Errorf("worker: %w", err)
	}

	r := &runner.Runner{CodeQLPath: c.codeql, Threads: c.threads, RAMMB: c.ram, Lock: runner.SharedLock(),
		Server:    &runner.CLIServer{CodeQLPath: c.codeql},
		Evaluator: &runner.QueryServer{CodeQLPath: c.codeql, Threads: c.threads, RAMMB: c.ram}}
	var tokens ghapp.TokenSource
	if t := os.Getenv(execution.TokenSecret); t != "" {
		tokens = lentToken(t)
	}
	e := &expand.Expander{Fetcher: &expand.Checkout{Tokens: tokens}, Builder: r, CodeQL: r, Analyzer: r,
		Decode: expand.Decoder(r.Decode)}
	w := &execution.Worker{Bundle: bundle, Identity: identity, Objects: objects, Analyze: e.AnalyzeTask, Dir: c.workDir,
		// The pack the grant bound is the only operator pack this process ever sees.
		Pack: func(ctx context.Context, root string) error {
			runner.SetCustomPackRoot(root)
			return r.InstallPack(ctx)
		},
		Test: func(ctx context.Context, _ string, tests []string) (runner.TestResult, error) {
			return r.RunTests(ctx, tests)
		}}
	if err := w.Run(ctx, grant); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "worker: %s finished its attempt and signed the result\n", identity.ID)
	return nil
}
