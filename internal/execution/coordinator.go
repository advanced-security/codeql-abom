package execution

import (
	"cmp"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// Runner is the coordinator's half: expand.RemoteRunner over a Launcher.
type Runner struct {
	Point   *authz.Point
	Objects ObjectStore
	Launch  Launcher
	// Roots verify a worker's identity; Identity is the one this pool's workers run as.
	Roots    *x509.CertPool
	Pool     string
	Identity string
	// Tokens lends a worker a read token for the repository it analyzes; nil lends none.
	Tokens RepositoryTokens
	// PackRoot is where the operator's pack is, whose queries travel with it; nil has none.
	PackRoot func() string
	Timeout  time.Duration
	Now      func() time.Time

	// handed are the packs already put where workers read them, by digest.
	mu     sync.Mutex
	handed map[string]bool
}

var _ expand.RemoteRunner = (*Runner)(nil)

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run grants one attempt, launches its worker, waits, and accepts the result only as signed by the
// pool's identity for this grant, with every output's bytes as signed.
func (r *Runner) Run(ctx context.Context, t expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
	attempt, err := newAttempt()
	if err != nil {
		return expand.RemoteOutput{}, err
	}
	g := r.newGrant(attempt)
	if g.Task, g.PackSHA256, err = r.withPack(ctx, t); err != nil {
		return expand.RemoteOutput{}, err
	}
	secrets := map[string]string{}
	// uninstalled is a read the App lends nothing for: the worker reads anonymously, so a refusal
	// may be a private repository the App cannot see, and is explained as one read here would be.
	uninstalled := false
	if r.Tokens != nil {
		token, err := r.Tokens.RepositoryToken(ctx, t.Owner, t.Repo)
		if err != nil {
			return expand.RemoteOutput{}, fmt.Errorf("lending a read token for %s/%s: %w", t.Owner, t.Repo, err)
		}
		if token != "" {
			secrets[TokenSecret] = token
		}
		uninstalled = token == ""
	}
	what := t.Owner + "/" + t.Repo
	grantDigest, err := r.execute(ctx, g, secrets, what)
	if err != nil {
		return expand.RemoteOutput{}, err
	}
	var out expand.RemoteOutput
	err = r.settle(ctx, Receipt{Attempt: attempt, Grant: grantDigest, Repository: what},
		func(result Result) (err error) {
			out, err = r.materialize(ctx, attempt, result, dir)
			return err
		},
		func(err error) error { return fmt.Errorf("the result for %s: %w", what, err) },
		func(err error) error { return err })
	if err != nil && uninstalled {
		return expand.RemoteOutput{}, expand.Uninstalled(err, t.Owner)
	}
	if err != nil {
		return expand.RemoteOutput{}, err
	}
	return out, nil
}

// RunTests runs tests of the pack under root in a worker of this pool, and writes back what the
// run left beside them. It is runner.Runner.RunTests across the boundary: dirs are inside root.
func (r *Runner) RunTests(ctx context.Context, root string, dirs []string) (runner.TestResult, error) {
	var tests []string
	for _, d := range dirs {
		rel, err := filepath.Rel(root, d)
		if err != nil || !plain(filepath.ToSlash(rel)) {
			return runner.TestResult{}, fmt.Errorf("test %s is not inside the pack at %s", d, root)
		}
		tests = append(tests, filepath.ToSlash(rel))
	}
	attempt, err := newAttempt()
	if err != nil {
		return runner.TestResult{}, err
	}
	pack, err := r.handOver(ctx, root)
	if err != nil {
		return runner.TestResult{}, err
	}
	g := r.newGrant(attempt)
	g.Test = &TestTask{PackSHA256: pack, Tests: tests}
	what := strings.Join(tests, ",")
	grantDigest, err := r.execute(ctx, g, map[string]string{}, what)
	if err != nil {
		return runner.TestResult{}, err
	}
	var result runner.TestResult
	wrap := func(err error) error { return fmt.Errorf("the result of %s: %w", what, err) }
	err = r.settle(ctx, Receipt{Attempt: attempt, Grant: grantDigest, Tests: what},
		func(accepted Result) (err error) {
			result, err = r.landTests(ctx, attempt, accepted, root, tests)
			return err
		}, wrap, wrap)
	if err != nil {
		return runner.TestResult{}, err
	}
	return result, nil
}

// settle accepts an attempt's result and lands it, signing a receipt whatever happens: refused with
// the reason, or accepted with the worker's own failure or with what landed. wrapAccept and wrapLand
// are each caller's words for the two ways a result is refused.
func (r *Runner) settle(ctx context.Context, receipt Receipt, land func(Result) error,
	wrapAccept, wrapLand func(error) error) error {
	receipt.Verdict = ReceiptRefused
	result, attestation, err := r.accept(ctx, receipt.Attempt, receipt.Grant)
	receipt.Attestation = attestation
	if err != nil {
		receipt.Reason = err.Error()
		return errors.Join(wrapAccept(err), r.sign(ctx, receipt))
	}
	receipt.Commit = result.Commit
	if f := result.Failure; f != nil {
		receipt.Verdict, receipt.Outcome = ReceiptAccepted, f.Kind
		return errors.Join(failureError(f), r.sign(ctx, receipt))
	}
	if err := land(result); err != nil {
		receipt.Reason = err.Error()
		return errors.Join(wrapLand(err), r.sign(ctx, receipt))
	}
	receipt.Verdict = ReceiptAccepted
	// Unrecorded is unaccepted: a result whose receipt cannot be signed is not ingested.
	return r.sign(ctx, receipt)
}

func (r *Runner) prefix() string { return Prefix + r.Pool + "/" }

func (r *Runner) newGrant(attempt string) Grant {
	now := r.now()
	return Grant{Attempt: attempt, Pool: r.Pool, Identity: r.Identity, Prefix: r.prefix(),
		IssuedAt: now.UTC().Format(time.RFC3339Nano), NotAfter: now.Add(r.Timeout).UTC().Format(time.RFC3339Nano)}
}

// withPack moves an analysis's findings queries that live in the operator's pack to paths inside
// it, and hands the pack over, answering its digest; a task with none travels as it is.
func (r *Runner) withPack(ctx context.Context, t expand.RemoteTask) (expand.RemoteTask, string, error) {
	if r.PackRoot == nil || r.PackRoot() == "" {
		return t, "", nil
	}
	root := r.PackRoot()
	var shipped []string
	for _, q := range t.Findings {
		rel, err := filepath.Rel(root, q)
		if err == nil && plain(filepath.ToSlash(rel)) {
			t.PackFindings = append(t.PackFindings, filepath.ToSlash(rel))
		} else {
			shipped = append(shipped, q)
		}
	}
	if len(t.PackFindings) == 0 {
		return t, "", nil
	}
	t.Findings = shipped
	pack, err := r.handOver(ctx, root)
	return t, pack, err
}

// handOver puts the pack under root where this pool's workers read it, by its digest, answering the
// digest the grant binds it by. A pack already handed over is not written again.
func (r *Runner) handOver(ctx context.Context, root string) (string, error) {
	body, err := packBundle(root)
	if err != nil {
		return "", fmt.Errorf("bundling the pack: %w", err)
	}
	sum := digest(body)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handed[sum] {
		return sum, nil
	}
	if err := r.Objects.Put(ctx, packKey(r.prefix(), sum), body); err != nil {
		return "", fmt.Errorf("handing over the pack: %w", err)
	}
	if r.handed == nil {
		r.handed = map[string]bool{}
	}
	r.handed[sum] = true
	return sum, nil
}

// execute signs g, launches its worker with secrets and waits for it, answering the grant's digest.
func (r *Runner) execute(ctx context.Context, g Grant, secrets map[string]string, what string) (string, error) {
	body, err := json.Marshal(g)
	if err != nil {
		return "", err
	}
	env, grantDigest, err := r.Point.Sign(ctx, authz.Statement{
		Subject:       []authz.Subject{{Name: g.Attempt, Digest: map[string]string{"sha256": digest(body)}}},
		PredicateType: authz.PredicateTaskGrant, Predicate: body})
	if err != nil {
		return "", fmt.Errorf("granting %s: %w", what, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	defer r.Launch.Cleanup(context.WithoutCancel(ctx), g.Attempt)
	if err := r.Launch.Start(waitCtx, Launch{Attempt: g.Attempt, Grant: env, Secrets: secrets}); err != nil {
		return "", fmt.Errorf("starting the worker for %s: %w", what, err)
	}
	if err := r.Launch.Wait(waitCtx, g.Attempt); err != nil {
		return "", fmt.Errorf("the worker for %s: %w", what, err)
	}
	return grantDigest, nil
}

func (r *Runner) sign(ctx context.Context, receipt Receipt) error {
	receipt.At = r.now().UTC().Format(time.RFC3339Nano)
	body, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	_, _, err = r.Point.Sign(ctx, authz.Statement{
		Subject:       []authz.Subject{{Name: receipt.Attempt, Digest: map[string]string{"sha256": receipt.Grant}}},
		PredicateType: authz.PredicateIngestionReceipt, Predicate: body})
	if err != nil {
		return fmt.Errorf("recording the receipt for %s: %w", cmp.Or(receipt.Repository, receipt.Tests), err)
	}
	return nil
}

// accept verifies an attempt's attestation: chained to the roots, naming the pool's identity,
// signed with that identity's key, for this attempt's grant.
func (r *Runner) accept(ctx context.Context, attempt, grantDigest string) (Result, string, error) {
	signed, err := r.Objects.Get(ctx, attestationKey(r.prefix(), attempt))
	if err != nil {
		return Result{}, "", fmt.Errorf("no attestation: %w", err)
	}
	result, err := r.verify(attempt, grantDigest, signed)
	return result, digest(signed), err
}

func (r *Runner) verify(attempt, grantDigest string, signed []byte) (Result, error) {
	var env authz.Envelope
	if err := json.Unmarshal(signed, &env); err != nil {
		return Result{}, fmt.Errorf("the attestation is not an envelope: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return Result{}, err
	}
	var claimed struct {
		Predicate Result `json:"predicate"`
	}
	if err := json.Unmarshal(payload, &claimed); err != nil {
		return Result{}, err
	}
	id, key, err := VerifyChain(claimed.Predicate.Chain, r.Roots, r.now())
	if err != nil {
		return Result{}, err
	}
	if id != r.Identity {
		return Result{}, fmt.Errorf("signed by %s, not %s", id, r.Identity)
	}
	st, err := authz.OpenWithKey(env, key)
	if err != nil {
		return Result{}, err
	}
	if st.PredicateType != authz.PredicateResultAttestation {
		return Result{}, fmt.Errorf("%s is not a result attestation", st.PredicateType)
	}
	var result Result
	if err := json.Unmarshal(st.Predicate, &result); err != nil {
		return Result{}, err
	}
	if result.Attempt != attempt || result.Grant != grantDigest || result.Identity != id {
		return Result{}, errors.New("the attestation is for another attempt, grant or identity")
	}
	return result, nil
}

// materialize brings every output back under dir once all of them verify: only the kinds an
// analysis leaves, each exactly the bytes signed, so a refused result writes nothing.
func (r *Runner) materialize(ctx context.Context, attempt string, result Result, dir string) (expand.RemoteOutput, error) {
	bodies := make([][]byte, len(result.Outputs))
	for i, o := range result.Outputs {
		switch o.Kind {
		case KindEdges, KindExtra, KindSarif, KindLock:
		default:
			return expand.RemoteOutput{}, fmt.Errorf("output %s is a %s, which an analysis does not leave", o.Name, o.Kind)
		}
		body, err := r.fetch(ctx, attempt, o)
		if err != nil {
			return expand.RemoteOutput{}, err
		}
		bodies[i] = body
	}
	out := expand.RemoteOutput{Commit: result.Commit, Ref: result.Ref, Digest: result.Digest,
		LockTree: filepath.Join(dir, "lock")}
	if err := os.MkdirAll(out.LockTree, 0o755); err != nil {
		return out, err
	}
	for i, o := range result.Outputs {
		file := filepath.Join(dir, filepath.FromSlash(o.Name))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			return out, err
		}
		if err := os.WriteFile(file, bodies[i], 0o644); err != nil {
			return out, err
		}
		switch o.Kind {
		case KindEdges:
			out.ResultPath, out.ResultQueryID = file, o.QueryID
		case KindExtra:
			if out.Extra == nil {
				out.Extra = map[string]string{}
			}
			out.Extra[o.QueryID] = file
		case KindSarif:
			out.SarifPath = file
		}
	}
	return out, nil
}

// landTests checks every output against what a run of the granted tests leaves, and only then
// writes them, so a refused result changes nothing.
func (r *Runner) landTests(ctx context.Context, attempt string, accepted Result, root string, tests []string) (runner.TestResult, error) {
	dirs := leftIn(root, tests)
	granted := func(rel, suffix string) bool {
		dir, name := path.Split(rel)
		return strings.HasSuffix(name, suffix) && name != suffix && slices.Contains(dirs, strings.TrimSuffix(dir, "/"))
	}
	var result runner.TestResult
	read := false
	actuals := map[string][]byte{}
	databases := map[string][]byte{}
	for _, o := range accepted.Outputs {
		body, err := r.fetch(ctx, attempt, o)
		if err != nil {
			return result, err
		}
		switch rel := ""; o.Kind {
		case KindTestResult:
			if err := json.Unmarshal(body, &result); err != nil {
				return result, fmt.Errorf("output %s: %w", o.Name, err)
			}
			read = true
		case KindActual:
			if rel = strings.TrimPrefix(o.Name, "actual/"); !granted(rel, ".actual") {
				return result, fmt.Errorf("output %s is not what a run of %s leaves", o.Name, strings.Join(tests, ", "))
			}
			actuals[rel] = body
		case KindTestDatabase:
			if rel = strings.TrimSuffix(strings.TrimPrefix(o.Name, "database/"), ".tgz"); !granted(rel, ".testproj") {
				return result, fmt.Errorf("output %s is not what a run of %s leaves", o.Name, strings.Join(tests, ", "))
			}
			databases[rel] = body
		default:
			return result, fmt.Errorf("output %s is a %s, which a test run does not leave", o.Name, o.Kind)
		}
	}
	if !read {
		return result, errors.New("the result names no test outcome")
	}
	for rel, body := range databases {
		if err := replaceDatabase(filepath.Join(root, filepath.FromSlash(rel)), body); err != nil {
			return result, err
		}
	}
	for rel, body := range actuals {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(rel)), body, 0o644); err != nil {
			return result, err
		}
	}
	return result, nil
}

// replaceDatabase puts a test's database in place whole: unpacked beside it, then swapped in.
func replaceDatabase(db string, archive []byte) error {
	staged := db + ".incoming"
	if err := os.RemoveAll(staged); err != nil {
		return err
	}
	if err := untarGz(archive, staged, databaseLimit); err != nil {
		_ = os.RemoveAll(staged)
		return fmt.Errorf("unpacking %s: %w", filepath.Base(db), err)
	}
	if err := os.RemoveAll(db); err != nil {
		return err
	}
	return os.Rename(staged, db)
}

// fetch reads one output, refusing one whose name is not plain or whose bytes are not those signed.
func (r *Runner) fetch(ctx context.Context, attempt string, o Output) ([]byte, error) {
	if !plain(o.Name) {
		return nil, fmt.Errorf("output %q is not a plain relative name", o.Name)
	}
	body, err := r.Objects.Get(ctx, outputKey(r.prefix(), attempt, o.Name))
	if err != nil {
		return nil, fmt.Errorf("output %s: %w", o.Name, err)
	}
	if digest(body) != o.SHA256 || int64(len(body)) != o.Size {
		return nil, fmt.Errorf("output %s is not what was signed", o.Name)
	}
	return body, nil
}
