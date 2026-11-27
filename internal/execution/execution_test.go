package execution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

const discovery = "spiffe://abom.local/ns/abom/sa/abom-worker-discovery"

type memLedger struct{ entries []authz.Entry }

func (m *memLedger) AppendAssertion(kind, digest string, envelope []byte, at time.Time) (authz.Entry, error) {
	prev := authz.Genesis
	if n := len(m.entries); n > 0 {
		prev = m.entries[n-1].Chain
	}
	e := authz.Entry{Seq: int64(len(m.entries) + 1), PredicateType: kind, Digest: digest,
		Chain: authz.NextChain(prev, digest), Envelope: envelope, RecordedAt: at.UTC().Format(time.RFC3339Nano)}
	m.entries = append(m.entries, e)
	return e, nil
}
func (m *memLedger) Assertions(int64, int) ([]authz.Entry, error) { return m.entries, nil }
func (m *memLedger) Head() (authz.Entry, bool, error) {
	if len(m.entries) == 0 {
		return authz.Entry{}, false, nil
	}
	return m.entries[len(m.entries)-1], true, nil
}

// analysis writes one edges result, one extra result and a lock, as AnalyzeTask does.
func analysis(ctx context.Context, t expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
	lock := filepath.Join(dir, "lock", ".github", "workflows")
	if err := os.MkdirAll(lock, 0o755); err != nil {
		return expand.RemoteOutput{}, err
	}
	for path, body := range map[string]string{
		filepath.Join(dir, "edges.json"): `{"edges":[]}`, filepath.Join(dir, "deps.json"): `{"deps":[]}`,
		filepath.Join(lock, "actions.lock"): "lockVersion: 0.0.3\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return expand.RemoteOutput{}, err
		}
	}
	return expand.RemoteOutput{Commit: "abc123", Ref: "HEAD", Digest: "d1", LockTree: filepath.Join(dir, "lock"),
		ResultPath: filepath.Join(dir, "edges.json"), ResultQueryID: "abom/edges",
		Extra: map[string]string{"abom/deps": filepath.Join(dir, "deps.json")}}, nil
}

type fixture struct {
	runner *Runner
	ledger *memLedger
	ca     *localCA
	launch *InProcess
	// objects is where attempts put what they produce; lent is each launch's secrets.
	objects DirStore
	lent    []map[string]string
	// private is the directory each worker keeps from the agent beside it.
	private string
	// keys are the decision point's, which every worker's bundle trusts.
	keys string
	// identity is who each worker runs as, and test the tests it runs.
	identity string
	test     TestFunc
	session  SessionFunc
}

func newFixture(t *testing.T, analyze AnalyzeFunc) *fixture {
	t.Helper()
	keys := filepath.Join(t.TempDir(), "authz")
	signer, err := authz.OpenLocal(keys, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ca, err := newLocalCA("abom.local")
	if err != nil {
		t.Fatal(err)
	}
	objects := DirStore(t.TempDir())
	ledger := &memLedger{}
	f := &fixture{ledger: ledger, ca: ca, keys: keys, identity: discovery, objects: objects, private: t.TempDir()}
	f.launch = &InProcess{Worker: func(secrets map[string]string) (*Worker, error) {
		f.lent = append(f.lent, secrets)
		id, err := ca.Issue(f.identity, time.Hour)
		if err != nil {
			return nil, err
		}
		trust, err := authz.ReadBundle(keys)
		if err != nil {
			return nil, err
		}
		key, err := ParseAttemptKey(secrets[AttemptKeySecret])
		if err != nil {
			return nil, err
		}
		return &Worker{Bundle: trust, Identity: id, AttemptKey: key, Objects: objects, Analyze: analyze, Test: f.test,
			Session: f.session, Dir: t.TempDir(), Private: f.private}, nil
	}}
	f.runner = &Runner{Point: &authz.Point{Signer: signer, Ledger: ledger}, Objects: objects, Launch: f.launch,
		Roots: ca.Pool(), Pool: "discovery", Identity: discovery, Timeout: time.Minute}
	return f
}

// keyOf is the key attempt was lent, found by the public half the grant the coordinator kept names.
func (f *fixture) keyOf(t *testing.T, attempt string) *AttemptKey {
	t.Helper()
	signed, err := f.objects.Get(context.Background(), grantKey(f.runner.prefix(), attempt), maxSigned)
	if err != nil {
		t.Fatalf("the grant of %s: %v", attempt, err)
	}
	g := grantIn(t, signed)
	for _, secrets := range f.lent {
		if key, err := ParseAttemptKey(secrets[AttemptKeySecret]); err == nil && key.PublicPEM() == g.AttemptKey {
			return key
		}
	}
	t.Fatalf("no attempt was lent the key %s's grant names", attempt)
	return nil
}

// grantIn is the grant a signed envelope holds, unverified.
func grantIn(t *testing.T, signed []byte) Grant {
	t.Helper()
	var env authz.Envelope
	if err := json.Unmarshal(signed, &env); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Predicate Grant `json:"predicate"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		t.Fatal(err)
	}
	return st.Predicate
}

func task() expand.RemoteTask {
	return expand.RemoteTask{Owner: "octo", Repo: "lib", Ref: "v1",
		Queries: []expand.RemoteQuery{{Path: "/q/Edges.ql", ID: "abom/edges", Edges: true}, {Path: "/q/Deps.ql", ID: "abom/deps"}}}
}

// A worker does what its signed grant says and nothing else, and what it returns arrives as it was
// produced: the grant is logged, and every output is back under the coordinator's directory.
func TestAWorkerDoesWhatItsGrantSaysAndItsOutputArrivesIntact(t *testing.T) {
	var got expand.RemoteTask
	f := newFixture(t, func(ctx context.Context, tk expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
		got = tk
		return analysis(ctx, tk, dir)
	})
	dir := t.TempDir()

	out, err := f.runner.Run(context.Background(), task(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "octo" || got.Repo != "lib" || len(got.Queries) != 2 {
		t.Errorf("the worker ran %+v", got)
	}
	if out.Commit != "abc123" || out.Digest != "d1" || out.ResultQueryID != "abom/edges" || !strings.HasPrefix(out.ResultPath, dir) {
		t.Errorf("output = %+v", out)
	}
	for _, path := range []string{out.ResultPath, out.Extra["abom/deps"], filepath.Join(out.LockTree, ".github", "workflows", "actions.lock")} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is not back: %v", path, err)
		}
	}
	if len(f.ledger.entries) != 2 || f.ledger.entries[0].PredicateType != authz.PredicateTaskGrant {
		t.Fatalf("logged %+v, want the grant and then its receipt", f.ledger.entries)
	}
	r := receiptOf(t, f.ledger.entries[1])
	if r.Verdict != ReceiptAccepted || r.Grant != f.ledger.entries[0].Digest || r.Attestation == "" ||
		r.Repository != "octo/lib" || r.Commit != "abc123" {
		t.Errorf("receipt = %+v; want the grant's result accepted at its commit", r)
	}
}

// receiptOf reads the receipt an entry of the log holds.
func receiptOf(t *testing.T, e authz.Entry) Receipt {
	t.Helper()
	if e.PredicateType != authz.PredicateIngestionReceipt {
		t.Fatalf("entry %d is %s, not a receipt", e.Seq, e.PredicateType)
	}
	var env authz.Envelope
	if err := json.Unmarshal(e.Envelope, &env); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Predicate Receipt `json:"predicate"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		t.Fatal(err)
	}
	return st.Predicate
}

// A grant names the identity it was issued to; a worker running as any other refuses it and does
// nothing, and the coordinator reports the attempt failed rather than waiting for a result.
func TestAGrantIssuedToAnotherIdentityIsRefused(t *testing.T) {
	ran := false
	f := newFixture(t, func(ctx context.Context, tk expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
		ran = true
		return analysis(ctx, tk, dir)
	})
	f.runner.Identity = "spiffe://abom.local/ns/abom/sa/abom-worker-tests"

	if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err == nil {
		t.Error("a grant for another identity produced a result")
	}
	if ran {
		t.Error("the worker analyzed under a grant that was not its own")
	}
}

// A result is accepted only when it is signed by an identity the coordinator's roots vouch for,
// naming the pool's identity, for this attempt's grant, with every output's bytes as signed.
func TestAResultIsAcceptedOnlyAsSignedByThePoolsIdentity(t *testing.T) {
	t.Run("an untrusted CA", func(t *testing.T) {
		f := newFixture(t, analysis)
		other, err := newLocalCA("abom.local")
		if err != nil {
			t.Fatal(err)
		}
		f.runner.Roots = other.Pool()
		if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err == nil {
			t.Error("a result signed under an untrusted CA was accepted")
		}
	})
	t.Run("an output longer than signed", func(t *testing.T) {
		f := newFixture(t, analysis)
		f.launch.After = func(objects ObjectStore, attempt string) {
			key := outputKey(f.runner.prefix(), attempt, "results/abom_edges.json")
			body, err := objects.Get(context.Background(), key, maxObject)
			if err != nil {
				t.Error(err)
				return
			}
			_ = objects.Put(context.Background(), key, append(body, make([]byte, 1<<20)...))
		}
		// Read no further than the length signed, which is all a coordinator holds room for.
		if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); !errors.Is(err, ErrTooLarge) {
			t.Errorf("an output longer than signed answered %v; want it refused at the length signed", err)
		}
	})
	t.Run("an output changed after signing", func(t *testing.T) {
		f := newFixture(t, analysis)
		f.launch.After = func(objects ObjectStore, attempt string) {
			_ = objects.Put(context.Background(), outputKey(f.runner.prefix(), attempt, "results/abom_edges.json"), []byte(`{"edges":["forged"]}`))
		}
		if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err == nil {
			t.Error("a changed output was accepted")
		}
		// What was refused is signed and logged as what was accepted is, so a tamper leaves a record.
		last := f.ledger.entries[len(f.ledger.entries)-1]
		if r := receiptOf(t, last); r.Verdict != ReceiptRefused || !strings.Contains(r.Reason, "abom_edges.json") {
			t.Errorf("receipt = %+v; want the result refused naming the changed output", r)
		}
	})
}

// Every worker of a pool signs as the pool's one identity, and every result names its grant where any
// of them can read it, so each attempt is lent a key of its own, named in its grant: a result is
// accepted only signed by both the pool's identity and that key, and never by another attempt's.
func TestAResultIsAcceptedOnlySignedByItsPoolAndItsAttemptsOwnKey(t *testing.T) {
	other, _, err := NewAttemptKey()
	if err != nil {
		t.Fatal(err)
	}
	for name, signers := range map[string]func(f *fixture, attempt string) *AttemptKey{
		"its own key, as a control":     func(f *fixture, attempt string) *AttemptKey { return f.keyOf(t, attempt) },
		"the pool's identity alone":     func(*fixture, string) *AttemptKey { return nil },
		"another attempt's key instead": func(*fixture, string) *AttemptKey { return other },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, analysis)
			f.launch.After = func(objects ObjectStore, attempt string) {
				resignWith(t, f, objects, attempt, signers(f, attempt))
			}
			_, err := f.runner.Run(context.Background(), task(), t.TempDir())
			if control := name == "its own key, as a control"; control != (err == nil) {
				t.Errorf("a result signed with %s answered %v", name, err)
			}
		})
	}
}

// Each attempt is lent a key no other attempt holds, named by its public half in the grant, and its
// private half reaches the worker in the attempt's secrets and is never written where the pool reads.
func TestEveryAttemptIsLentAKeyOfItsOwn(t *testing.T) {
	f := newFixture(t, analysis)
	var grants []Grant
	f.launch.After = func(objects ObjectStore, attempt string) {
		signed, err := objects.Get(context.Background(), grantKey(f.runner.prefix(), attempt), maxSigned)
		if err != nil {
			t.Error(err)
			return
		}
		grants = append(grants, grantIn(t, signed))
	}
	for range 2 {
		if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	if len(grants) != 2 || grants[0].AttemptKey == "" || grants[0].AttemptKey == grants[1].AttemptKey {
		t.Fatalf("the grants are %+v; want each to name a key of its own", grants)
	}
	for i, g := range grants {
		key, err := ParseAttemptKey(f.lent[i][AttemptKeySecret])
		if err != nil || key.PublicPEM() != g.AttemptKey {
			t.Errorf("attempt %d was lent a key its grant does not name: %v", i, err)
		}
	}
	if err := filepath.WalkDir(string(f.objects), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(p)
		for _, secrets := range f.lent {
			if err == nil && strings.Contains(string(body), strings.TrimSpace(secrets[AttemptKeySecret])) {
				t.Errorf("%s holds an attempt's private key", p)
			}
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// A grant names the key its attempt signs with, so a worker lent another refuses it and does nothing:
// what it signed would be refused anyway.
func TestAWorkerLentAnotherKeyThanItsGrantNamesDoesNothing(t *testing.T) {
	ran := false
	f := newFixture(t, func(ctx context.Context, tk expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
		ran = true
		return analysis(ctx, tk, dir)
	})
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.AttemptKey, _, err = NewAttemptKey()
		}
		return w, err
	}
	if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err == nil || ran {
		t.Errorf("a worker lent another key answered %v, ran: %v", err, ran)
	}
}

// A worker's identity may sign anything, so an analysis brings back only what an analysis leaves,
// and writes nothing until all of it verifies: a result carrying anything else is refused whole.
func TestAnAnalysisResultCarryingAnythingElseIsRefusedWhole(t *testing.T) {
	f := newFixture(t, analysis)
	f.launch.After = func(objects ObjectStore, attempt string) {
		forge(t, f, objects, attempt, map[string]string{"actual/test/A/A.actual": "select 2\n"})
	}
	dir := t.TempDir()
	if _, err := f.runner.Run(context.Background(), task(), dir); err == nil || !strings.Contains(err.Error(), "A.actual") {
		t.Errorf("Run = %v; want the result refused for the output an analysis does not leave", err)
	}
	if written, _ := os.ReadDir(dir); len(written) != 0 {
		t.Errorf("wrote %d entries under the attempt's directory for a refused result", len(written))
	}
}

// A failure a worker meets settles exactly as the same failure met here: in the same class, and
// with the same reason, which a row keeps and is read back by.
func TestAWorkersFailureSettlesExactlyAsTheSameFailureHere(t *testing.T) {
	for name, here := range map[string]error{
		"nothing to analyze": &expand.NothingToAnalyze{Tree: expand.Tree{Ref: "HEAD", SHA: "e1"},
			Why: "lib has no workflows or action manifest"},
		"refused": fmt.Errorf("%w: cloning https://github.com/octo/lib.git at %q: %s", expand.ErrUnavailable, "v9",
			"fatal: unable to access 'https://github.com/octo/lib.git/': The requested URL returned error: 403"),
		"absent":                    fmt.Errorf("%w: %w: octo/lib has no v9", expand.ErrUnavailable, expand.ErrReferenceMissing),
		"unavailable, said no more": expand.ErrUnavailable,
		"no source code":            fmt.Errorf("creating the database: %w", runner.ErrNoSourceCode),
		"transient":                 errors.New("fatal: early EOF"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, func(context.Context, expand.RemoteTask, string) (expand.RemoteOutput, error) {
				return expand.RemoteOutput{}, here
			})
			_, back := f.runner.Run(context.Background(), task(), t.TempDir())
			if back == nil {
				t.Fatal("the failure came back as a success")
			}
			var nothingHere, nothingBack *expand.NothingToAnalyze
			for what, same := range map[string]bool{
				"unavailable":        expand.IsUnavailable(back) == expand.IsUnavailable(here),
				"no source code":     runner.IsNoSourceCode(back) == runner.IsNoSourceCode(here),
				"nothing to analyze": errors.As(back, &nothingBack) == errors.As(here, &nothingHere),
				"reason":             back.Error() == here.Error(),
			} {
				if !same {
					t.Errorf("%s differs: here %q, back %q", what, here, back)
				}
			}
		})
	}
}

// A worker the App lent nothing to reads anonymously, so a refusal it meets says why, as a local
// read without the App's own installation does: it may be a private repository the App cannot see.
func TestAWorkerLentNothingSaysTheAppIsNotInstalledThere(t *testing.T) {
	refused := fmt.Errorf("%w: cloning https://github.com/octo/lib.git at %q: %s", expand.ErrUnavailable, "v1",
		"remote: Repository not found.")
	f := newFixture(t, func(context.Context, expand.RemoteTask, string) (expand.RemoteOutput, error) {
		return expand.RemoteOutput{}, refused
	})
	f.runner.Tokens = lends{}
	_, back := f.runner.Run(context.Background(), task(), t.TempDir())
	if !expand.IsUnavailable(back) || !errors.Is(back, ghapp.ErrNotInstalled) ||
		!strings.HasPrefix(back.Error(), refused.Error()) || !strings.Contains(back.Error(), "private repository") {
		t.Errorf("came back as %q; want the refusal, and that the App is not installed on octo", back)
	}
}

// lends answers a read token per repository, as an App lends one: none for a repository it names none for.
type lends map[string]string

func (l lends) RepositoryToken(_ context.Context, owner, repo string) (string, error) {
	return l[owner+"/"+repo], nil
}

// A worker is lent a token for the one repository its task names, and no token at all where there
// is none to lend for it, rather than another installation's that reads more.
func TestAWorkerIsLentATokenForItsOwnRepositoryOrNone(t *testing.T) {
	f := newFixture(t, analysis)
	f.runner.Tokens = lends{"octo/lib": "ghs_lib"}
	if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	other := task()
	other.Owner, other.Repo = "actions", "checkout"
	if _, err := f.runner.Run(context.Background(), other, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(f.lent) != 2 || f.lent[0]["GITHUB_TOKEN"] != "ghs_lib" {
		t.Fatalf("lent %v; want octo/lib's token to its attempt", f.lent)
	}
	if token, lent := f.lent[1]["GITHUB_TOKEN"]; lent {
		t.Errorf("an attempt with no token for its repository was lent %q", token)
	}
}

// Every object an attempt reads or writes is under its pool's prefix, which the grant carries, so the
// object store's credentials can confine each pool to its own attempts.
func TestAnAttemptsObjectsLiveUnderItsPoolsPrefix(t *testing.T) {
	f := newFixture(t, analysis)
	if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := filepath.WalkDir(string(f.objects), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(string(f.objects), p)
			keys = append(keys, filepath.ToSlash(rel))
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) == 0 {
		t.Fatal("the attempt stored nothing")
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "attempts/discovery/") {
			t.Errorf("%s is outside the discovery pool's prefix", key)
		}
	}
}

// reads counts what a coordinator reads of the outputs results name.
type reads struct {
	ObjectStore
	mu      sync.Mutex
	outputs []string
}

func (r *reads) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if _, name, ok := strings.Cut(key, "/out/"); ok {
		r.mu.Lock()
		r.outputs = append(r.outputs, name)
		r.mu.Unlock()
	}
	return r.ObjectStore.Get(ctx, key, limit)
}

func (r *reads) read() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.outputs)
}

// A worker signs for whatever it likes, and a coordinator holds every output of a result before any of
// it lands, so a result naming more than may be held is refused before any output is read.
func TestAResultNamingMoreThanMayBeHeldIsRefusedBeforeAnyOfItIsRead(t *testing.T) {
	for name, change := range map[string]func(*Result){
		"an output longer than any may be": func(r *Result) { r.Outputs[0].Size = maxObject + 1 },
		"an output shorter than nothing":   func(r *Result) { r.Outputs[0].Size = -1 },
		"outputs longer together than may be held": func(r *Result) {
			for i := range 3 {
				r.Outputs = append(r.Outputs, Output{Name: fmt.Sprintf("lock/%d", i), Kind: KindLock, Size: maxObject})
			}
		},
		"more outputs than a result names": func(r *Result) {
			for len(r.Outputs) <= maxOutputs {
				r.Outputs = append(r.Outputs, Output{Name: fmt.Sprintf("lock/%d", len(r.Outputs)), Kind: KindLock})
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, analysis)
			f.launch.After = func(objects ObjectStore, attempt string) { resign(t, f, objects, attempt, change) }
			counted := &reads{ObjectStore: f.objects}
			f.runner.Objects = counted
			if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err == nil {
				t.Errorf("a result with %s was accepted", name)
			}
			if read := counted.read(); len(read) != 0 {
				t.Errorf("read %v of a result with %s", read, name)
			}
			if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); r.Verdict != ReceiptRefused {
				t.Errorf("receipt = %+v; want it refused", r)
			}
		})
	}
}

// Results settling together, in whichever pools, hold no more than a coordinator may: one waits for
// room, and nothing of it is read until there is.
func TestResultsSettlingTogetherHoldNoMoreThanMayBeHeld(t *testing.T) {
	f := newFixture(t, analysis)
	counted := &reads{ObjectStore: f.objects}
	f.runner.Objects = counted
	if err := holding.Acquire(context.Background(), maxHeld); err != nil {
		t.Fatal(err)
	}
	held := true
	t.Cleanup(func() {
		if held {
			holding.Release(maxHeld)
		}
	})
	waiting, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := f.runner.Run(waiting, task(), t.TempDir()); err == nil {
		t.Fatal("a result was read while there was no room to hold it")
	}
	if read := counted.read(); len(read) != 0 {
		t.Errorf("read %v while there was no room", read)
	}
	if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); !strings.Contains(r.Reason, "deadline") {
		t.Errorf("receipt = %+v; want the result given up while it waited for room", r)
	}
	holding.Release(maxHeld)
	held = false
	if _, err := f.runner.Run(context.Background(), task(), t.TempDir()); err != nil {
		t.Errorf("once there was room the result answered %v", err)
	}
}
