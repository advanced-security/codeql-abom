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

// Worker runs one granted attempt.
type Worker struct {
	// Bundle verifies the grant: the decision point's keys.
	Bundle   authz.Bundle
	Identity *Identity
	Objects  ObjectStore
	Analyze  AnalyzeFunc
	// Test runs a query's tests; a worker without one refuses a test grant.
	Test TestFunc
	// Pack prepares the operator's pack unpacked at root before anything runs from it.
	Pack func(ctx context.Context, root string) error
	Dir  string
	Now  func() time.Time
}

func (w *Worker) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// Run verifies the grant and does exactly what it says. A grant that does not verify, has expired,
// or names another identity is refused before anything is fetched, and nothing is written.
func (w *Worker) Run(ctx context.Context, env authz.Envelope) error {
	g, grantDigest, err := w.open(env)
	if err != nil {
		return err
	}
	dir := filepath.Join(w.Dir, g.Attempt)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	r := Result{Attempt: g.Attempt, Grant: grantDigest, Identity: w.Identity.ID, Chain: w.Identity.ChainPEM(), Outputs: []Output{}}
	run := w.runAnalysis
	if g.Test != nil {
		run = w.runTests
	}
	if err := run(ctx, g, dir, &r); err != nil {
		return err
	}
	return w.attest(ctx, g, r)
}

// open verifies the grant in env as this worker's, unexpired, and answers it with its digest.
func (w *Worker) open(env authz.Envelope) (Grant, string, error) {
	now := w.now()
	st, err := w.Bundle.Open(env, now)
	if err != nil {
		return Grant{}, "", fmt.Errorf("worker: the grant does not verify: %w", err)
	}
	if st.PredicateType != authz.PredicateTaskGrant {
		return Grant{}, "", fmt.Errorf("worker: %s is not a task grant", st.PredicateType)
	}
	var g Grant
	if err := json.Unmarshal(st.Predicate, &g); err != nil {
		return Grant{}, "", fmt.Errorf("worker: reading the grant: %w", err)
	}
	if g.Identity != w.Identity.ID {
		return Grant{}, "", fmt.Errorf("worker: the grant is for %s, and this worker is %s", g.Identity, w.Identity.ID)
	}
	if until, err := time.Parse(time.RFC3339Nano, g.NotAfter); err != nil || !now.Before(until) {
		return Grant{}, "", errors.New("worker: the grant has expired")
	}
	grantDigest, err := env.Digest()
	return g, grantDigest, err
}

// runAnalysis runs an analysis grant, from the pack it binds when its findings queries live there.
func (w *Worker) runAnalysis(ctx context.Context, g Grant, dir string, r *Result) error {
	task := g.Task
	if g.PackSHA256 != "" {
		root, why := w.unpack(ctx, g, g.PackSHA256, dir)
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
	return w.upload(ctx, g, out, r)
}

// runTests runs a test grant: on the pack it binds and no other, then uploads the result and what
// the run left in each granted test's directory.
func (w *Worker) runTests(ctx context.Context, g Grant, dir string, r *Result) error {
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
	root, why := w.unpack(ctx, g, g.Test.PackSHA256, dir)
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
	if err := w.putBytes(ctx, g, r, "test/result.json", KindTestResult, "", encoded); err != nil {
		return err
	}
	for _, d := range leftIn(root, g.Test.Tests) {
		if err := w.putLeft(ctx, g, r, root, d); err != nil {
			return err
		}
	}
	return nil
}

// unpack reads the pack a grant binds by digest and prepares it under dir, answering why not.
func (w *Worker) unpack(ctx context.Context, g Grant, want, dir string) (string, string) {
	body, err := w.Objects.Get(ctx, packKey(g.Prefix, want))
	if err != nil {
		return "", "reading the pack: " + err.Error()
	}
	if got := digest(body); got != want {
		return "", "the pack read is not the pack granted"
	}
	root := filepath.Join(dir, "pack")
	if err := untarGz(body, root, packLimit); err != nil {
		return "", "unpacking the pack: " + err.Error()
	}
	if w.Pack != nil {
		if err := w.Pack(ctx, root); err != nil {
			return "", "preparing the pack: " + err.Error()
		}
	}
	return root, ""
}

// put uploads the file at file as one output.
func (w *Worker) put(ctx context.Context, g Grant, r *Result, name, kind, queryID, file string) error {
	body, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return w.putBytes(ctx, g, r, name, kind, queryID, body)
}

// putBytes uploads one output and lists it in the result.
func (w *Worker) putBytes(ctx context.Context, g Grant, r *Result, name, kind, queryID string, body []byte) error {
	if err := w.Objects.Put(ctx, outputKey(g.Prefix, g.Attempt, name), body); err != nil {
		return err
	}
	r.Outputs = append(r.Outputs, Output{Name: name, Kind: kind, QueryID: queryID,
		SHA256: digest(body), Size: int64(len(body))})
	return nil
}

func (w *Worker) upload(ctx context.Context, g Grant, out expand.RemoteOutput, r *Result) error {
	r.Commit, r.Ref, r.Digest = out.Commit, out.Ref, out.Digest
	put := func(name, kind, queryID, file string) error { return w.put(ctx, g, r, name, kind, queryID, file) }
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

// putLeft uploads each .actual and test database a run left directly in one directory.
func (w *Worker) putLeft(ctx context.Context, g Grant, r *Result, root, test string) error {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(test)))
	if err != nil {
		return err
	}
	for _, e := range entries {
		rel := test + "/" + e.Name()
		switch {
		case e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".actual"):
			if err := w.put(ctx, g, r, "actual/"+rel, KindActual, "", filepath.Join(root, filepath.FromSlash(rel))); err != nil {
				return err
			}
		case e.IsDir() && strings.HasSuffix(e.Name(), ".testproj"):
			db := filepath.Join(root, filepath.FromSlash(rel))
			var files []string
			if err := filepath.WalkDir(db, func(p string, d fs.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					files = append(files, p)
				}
				return err
			}); err != nil {
				return err
			}
			archive, err := tarGz(db, files, databaseLimit)
			if err != nil {
				return err
			}
			if err := w.putBytes(ctx, g, r, "database/"+rel+".tgz", KindTestDatabase, "", archive); err != nil {
				return err
			}
		}
	}
	return nil
}

// attest signs the result and writes it last: its presence is what says the outputs are complete.
func (w *Worker) attest(ctx context.Context, g Grant, r Result) error {
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	subjects := []authz.Subject{{Name: g.Attempt, Digest: map[string]string{"sha256": r.Grant}}}
	for _, o := range r.Outputs {
		subjects = append(subjects, authz.Subject{Name: o.Name, Digest: map[string]string{"sha256": o.SHA256}})
	}
	env, err := authz.Seal(ctx, w.Identity, authz.Statement{Subject: subjects,
		PredicateType: authz.PredicateResultAttestation, Predicate: body})
	if err != nil {
		return err
	}
	signed, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return w.Objects.Put(ctx, attestationKey(g.Prefix, g.Attempt), signed)
}

func safe(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}
