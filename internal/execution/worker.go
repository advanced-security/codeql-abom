package execution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/expand"
)

// Worker runs one granted attempt, reading its grant and inputs and writing what it leaves only
// through an attestor.
type Worker struct {
	// Attestor holds the worker's secrets for it, in a pod across a socket from another container; nil
	// holds them in this process, from the fields below.
	Attestor Attestor
	// Bundle verifies the grant: the decision point's keys.
	Bundle   authz.Bundle
	Identity *Identity
	// AttemptKey is the key the attempt's grant names, which signs everything the worker signs beside
	// its identity.
	AttemptKey *AttemptKey
	// Renew reads the worker's identity again before each signature, since a session can outlast the
	// certificate it began with; nil signs with Identity throughout.
	Renew   func() (*Identity, error)
	Objects ObjectStore
	Analyze AnalyzeFunc
	// Test runs a query's tests; a worker without one refuses a test grant.
	Test TestFunc
	// Session runs an authoring session; a worker without one refuses a session grant.
	Session SessionFunc
	// Pack prepares the operator's pack unpacked at root before anything runs from it.
	Pack func(ctx context.Context, root string) error
	Dir  string
	// Private is a directory only this worker's container holds, for what the agent beside a session
	// must not read; empty is the system's temporary directory, which in a session's pod is.
	Private string
	Now     func() time.Time
}

func (w *Worker) private() string {
	if w.Private != "" {
		return w.Private
	}
	return os.TempDir()
}

// attestor is what holds the worker's secrets for the grant in env.
func (w *Worker) attestor(env authz.Envelope) Attestor {
	if w.Attestor != nil {
		return w.Attestor
	}
	return Hold(env, Secrets{Bundle: w.Bundle, Identity: w.Identity, Renew: w.Renew, AttemptKey: w.AttemptKey,
		Objects: w.Objects, Now: w.Now})
}

// Run verifies the grant and does exactly what it says. A grant that does not verify, has expired,
// or names another identity or key is refused before anything is fetched, and nothing is written. An
// attestor that is not in this process verifies the grant it holds, and env is not read.
func (w *Worker) Run(ctx context.Context, env authz.Envelope) error {
	a := w.attestor(env)
	g, _, err := a.Grant(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.Dir, g.Attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	var r Result
	run := w.runAnalysis
	switch {
	case g.Session != nil:
		run = w.runSession
	case g.Test != nil:
		run = w.runTests
	}
	if err := run(ctx, a, g, dir, &r); err != nil {
		return err
	}
	if g.Session != nil {
		// A session stopped by its pod's SIGTERM still signs what it left, inside its grace period.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), finishing)
		defer cancel()
	}
	return a.Attest(ctx, r)
}

// runAnalysis runs an analysis grant, from the pack it binds when its findings queries live there.
func (w *Worker) runAnalysis(ctx context.Context, a Attestor, g Grant, dir string, r *Result) error {
	task := g.Task
	if g.PackSHA256 != "" {
		root, why := w.unpack(ctx, a, g.PackSHA256, dir)
		if why != "" {
			r.Failure = &Failure{Kind: FailedRetryable, Why: why}
			return nil
		}
		task.Findings = slices.Clone(task.Findings)
		for _, q := range task.PackFindings {
			task.Findings = append(task.Findings, filepath.Join(root, filepath.FromSlash(q)))
		}
		task.PackFindings = nil
	}
	out, err := w.Analyze(ctx, task, dir)
	if err != nil {
		r.Failure = classify(err)
		return nil
	}
	return upload(ctx, a, out, r)
}

// runTests runs a test grant: on the pack it binds and no other, then uploads the result and what
// the run left in each granted test's directory.
func (w *Worker) runTests(ctx context.Context, a Attestor, g Grant, dir string, r *Result) error {
	refuse := func(why string) error {
		r.Failure = &Failure{Kind: FailedRetryable, Why: why}
		return nil
	}
	if w.Test == nil {
		return errors.New("worker: this worker runs no tests")
	}
	for _, t := range g.Test.Tests {
		if !plain(t) {
			return refuse(fmt.Sprintf("test %q is not a plain relative name", t))
		}
	}
	root, why := w.unpack(ctx, a, g.Test.PackSHA256, dir)
	if why != "" {
		return refuse(why)
	}
	var tests []string
	for _, t := range g.Test.Tests {
		tests = append(tests, filepath.Join(root, filepath.FromSlash(t)))
	}
	result, err := w.Test(ctx, root, tests)
	if err != nil {
		return refuse(err.Error())
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	if err := a.Output(ctx, Output{Name: "test/result.json", Kind: KindTestResult}, encoded); err != nil {
		return err
	}
	pack, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = pack.Close() }()
	for _, d := range leftIn(root, g.Test.Tests) {
		if err := putLeftIn(ctx, a, pack, d); err != nil {
			return err
		}
	}
	return nil
}

// unpack reads the pack a grant binds by digest and prepares it under dir, answering why not.
func (w *Worker) unpack(ctx context.Context, a Attestor, want, dir string) (string, string) {
	root := filepath.Join(dir, "pack")
	return root, w.unpackAt(ctx, a, want, root)
}

// unpackAt reads the pack a grant binds by digest and prepares it at root, answering why not.
func (w *Worker) unpackAt(ctx context.Context, a Attestor, want, root string) string {
	body, err := a.Input(ctx, Input{Kind: InputPack, SHA256: want})
	if err != nil {
		return "reading the pack: " + err.Error()
	}
	if got := digest(body); got != want {
		return "the pack read is not the pack granted"
	}
	if err := untarGz(body, root, packLimit); err != nil {
		return "unpacking the pack: " + err.Error()
	}
	if w.Pack != nil {
		if err := w.Pack(ctx, root); err != nil {
			return "preparing the pack: " + err.Error()
		}
	}
	return ""
}

// putFile uploads the file at file as one output.
func putFile(ctx context.Context, a Attestor, name, kind, queryID, file string) error {
	body, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return a.Output(ctx, Output{Name: name, Kind: kind, QueryID: queryID}, body)
}

func upload(ctx context.Context, a Attestor, out expand.RemoteOutput, r *Result) error {
	r.Commit, r.Ref, r.Digest = out.Commit, out.Ref, out.Digest
	put := func(name, kind, queryID, file string) error { return putFile(ctx, a, name, kind, queryID, file) }
	if out.ResultPath != "" {
		if err := put("results/"+safe(out.ResultQueryID)+".json", KindEdges, out.ResultQueryID, out.ResultPath); err != nil {
			return err
		}
	}
	for id, file := range out.Extra {
		if err := put("results/"+safe(id)+".json", KindExtra, id, file); err != nil {
			return err
		}
	}
	if out.SarifPath != "" {
		if err := put("findings.sarif", KindSarif, "", out.SarifPath); err != nil {
			return err
		}
	}
	if out.LockTree == "" {
		return nil
	}
	return filepath.WalkDir(out.LockTree, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(out.LockTree, p)
		if err != nil {
			return err
		}
		return put("lock/"+filepath.ToSlash(rel), KindLock, "", p)
	})
}

// putLeftIn uploads each .actual and test database a run left directly in the directory test, a slash
// path within pack, each read through no link and from nothing but a regular file.
func putLeftIn(ctx context.Context, a Attestor, pack *os.Root, test string) error {
	entries, err := fs.ReadDir(pack.FS(), test)
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := test + "/" + e.Name()
		switch {
		case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".actual"):
			body, err := readIn(pack, rel, maxObject)
			if err != nil {
				return err
			}
			if err := a.Output(ctx, Output{Name: "actual/" + rel, Kind: KindActual}, body); err != nil {
				return err
			}
		case e.IsDir() && strings.HasSuffix(e.Name(), ".testproj"):
			var files []string
			if err := fs.WalkDir(pack.FS(), rel, func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					files = append(files, p)
				}
				return err
			}); err != nil {
				return err
			}
			archive, err := archiveIn(pack, rel, files, databaseLimit)
			if err != nil {
				return err
			}
			if err := a.Output(ctx, Output{Name: "database/" + rel + ".tgz", Kind: KindTestDatabase}, archive); err != nil {
				return err
			}
		}
	}
	return nil
}

func safe(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}
