package execution

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

const qlaasTest = "spiffe://abom.local/ns/abom/sa/abom-worker-qlaas-test"

// pack is a custom query pack as the coordinator holds it, with a previous run's leavings beside
// its tests.
func pack(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		"qlpack.yml":                       "name: abom/custom-queries\n",
		"src/A.ql":                         "select 1\n",
		"test/qlpack.yml":                  "name: abom/custom-queries-tests\n",
		"test/A/A.qlref":                   "query: src/A.ql\n",
		"test/A/A.expected":                "| 1 |\n",
		"test/A/.github/workflows/ci.yml":  "on: push\n",
		"test/A/A.actual":                  "stale\n",
		"test/A/A.testproj/db-actions/old": "stale",
		"test/B/B.qlref":                   "query: src/B.ql\n",
		".codeql/cache/big":                "cache",
	} {
		file := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testFixture(t *testing.T, test TestFunc) *fixture {
	t.Helper()
	f := newFixture(t, analysis)
	f.identity, f.test = qlaasTest, test
	f.runner.Pool, f.runner.Identity = "qlaas-test", qlaasTest
	return f
}

// passing is a worker's CodeQL: it reads the pack it was given and writes what a run leaves.
func passing(seen *[]string) TestFunc {
	return func(_ context.Context, root string, tests []string) (runner.TestResult, error) {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				rel, _ := filepath.Rel(root, p)
				*seen = append(*seen, filepath.ToSlash(rel))
			}
			return err
		})
		// As the CLI does: a .qlref's output beside it under its own name, and one database for its directory.
		for _, test := range tests {
			dir := filepath.Dir(test)
			actual := strings.TrimSuffix(filepath.Base(test), ".qlref") + ".actual"
			if err := os.WriteFile(filepath.Join(dir, actual), []byte("| 1 |\n"), 0o644); err != nil {
				return runner.TestResult{}, err
			}
			db := filepath.Join(dir, filepath.Base(dir)+".testproj", "db-actions")
			if err := os.MkdirAll(db, 0o755); err != nil {
				return runner.TestResult{}, err
			}
			if err := os.WriteFile(filepath.Join(db, "fresh"), []byte("db"), 0o644); err != nil {
				return runner.TestResult{}, err
			}
		}
		return runner.TestResult{Passed: true, Total: len(tests), Detail: "All 1 tests passed."}, nil
	}
}

// A query's tests run in a worker on the pack the grant binds by digest, without a previous
// run's leavings, and what the run left comes back beside the tests: the result, each .actual and
// the database the tree is printed from.
func TestAQueryTestRunsInAWorkerOnThePackItsGrantBinds(t *testing.T) {
	var seen []string
	f := testFixture(t, passing(&seen))
	root := pack(t)

	got, err := f.runner.RunTests(context.Background(), root, []string{filepath.Join(root, "test", "A", "A.qlref")})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Passed || got.Total != 1 || got.Detail != "All 1 tests passed." {
		t.Errorf("result = %+v", got)
	}
	slices.Sort(seen)
	want := []string{"qlpack.yml", "src/A.ql", "test/A/.github/workflows/ci.yml", "test/A/A.expected",
		"test/A/A.qlref", "test/B/B.qlref", "test/qlpack.yml"}
	if !slices.Equal(seen, want) {
		t.Errorf("the worker was given %v; want the pack without outputs or caches %v", seen, want)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "test", "A", "A.actual")); string(body) != "| 1 |\n" {
		t.Errorf("A.actual = %q; want what the worker's run wrote", body)
	}
	if _, err := os.Stat(filepath.Join(root, "test", "A", "A.testproj", "db-actions", "fresh")); err != nil {
		t.Errorf("the test database did not come back: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "test", "A", "A.testproj", "db-actions", "old")); err == nil {
		t.Error("the previous run's database survived beside the new one")
	}
	if len(f.ledger.entries) != 2 {
		t.Fatalf("logged %d entries; want the grant and its receipt", len(f.ledger.entries))
	}
	if g := grantOf(t, f.ledger.entries[0]); g.Test == nil || g.Test.PackSHA256 == "" ||
		!slices.Equal(g.Test.Tests, []string{"test/A/A.qlref"}) {
		t.Errorf("grant = %+v; want the tests and the bundle's digest", g)
	}
	if r := receiptOf(t, f.ledger.entries[1]); r.Verdict != ReceiptAccepted || r.Tests != "test/A/A.qlref" {
		t.Errorf("receipt = %+v; want the tests' result accepted", r)
	}
}

// grantOf reads the grant an entry of the log holds.
func grantOf(t *testing.T, e authz.Entry) Grant {
	t.Helper()
	var env authz.Envelope
	if err := json.Unmarshal(e.Envelope, &env); err != nil {
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

// swapping is an object store whose pack is replaced after the grant was signed over it.
type swapping struct{ ObjectStore }

func (s swapping) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	body, err := s.ObjectStore.Get(ctx, key, limit)
	if strings.Contains(key, "/packs/") {
		return append(bytes.Clone(body), 0), err
	}
	return body, err
}

// A worker runs nothing on a pack other than the one its grant names, and the coordinator records
// nothing from it.
func TestAWorkerRunsNoTestsOnAPackOtherThanTheOneGranted(t *testing.T) {
	ran := false
	f := testFixture(t, func(context.Context, string, []string) (runner.TestResult, error) {
		ran = true
		return runner.TestResult{Passed: true}, nil
	})
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.Objects = swapping{w.Objects}
		}
		return w, err
	}
	root := pack(t)

	if _, err := f.runner.RunTests(context.Background(), root, []string{filepath.Join(root, "test", "A")}); err == nil ||
		!strings.Contains(err.Error(), "not the pack granted") {
		t.Errorf("RunTests = %v; want the swapped pack refused", err)
	}
	if ran {
		t.Error("the worker ran tests on a pack its grant does not name")
	}
	if body, _ := os.ReadFile(filepath.Join(root, "test", "A", "A.actual")); string(body) != "stale\n" {
		t.Errorf("A.actual = %q; want nothing written back", body)
	}
}

// A worker's identity may sign anything, so what it returns lands only where a run of the granted
// tests writes: an .actual or a database in a granted test's own directory.
func TestOnlyWhatTheGrantedTestsLeaveComesBack(t *testing.T) {
	f := testFixture(t, func(_ context.Context, root string, _ []string) (runner.TestResult, error) {
		return runner.TestResult{Passed: true, Total: 1}, nil
	})
	f.launch.After = func(objects ObjectStore, attempt string) {
		forge(t, f, objects, attempt, map[string]string{"actual/src/A.ql": "select 2\n"})
	}
	root := pack(t)

	if _, err := f.runner.RunTests(context.Background(), root, []string{filepath.Join(root, "test", "A")}); err == nil ||
		!strings.Contains(err.Error(), "src/A.ql") {
		t.Errorf("RunTests = %v; want an output outside the granted tests refused", err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "src", "A.ql")); string(body) != "select 1\n" {
		t.Errorf("src/A.ql = %q; a worker rewrote the query", body)
	}
}

// forge adds outputs to an attempt's result and signs it again as the pool's identity would.
func forge(t *testing.T, f *fixture, objects ObjectStore, attempt string, extra map[string]string) {
	t.Helper()
	forgeAs(t, f, objects, attempt, KindActual, extra)
}

// forgeAs is forge, adding outputs of kind.
func forgeAs(t *testing.T, f *fixture, objects ObjectStore, attempt, kind string, extra map[string]string) {
	t.Helper()
	ctx := context.Background()
	signed, err := objects.Get(ctx, attestationKey(f.runner.prefix(), attempt), maxSigned)
	if err != nil {
		t.Fatal(err)
	}
	var env authz.Envelope
	if err := json.Unmarshal(signed, &env); err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	var st struct {
		Predicate Result `json:"predicate"`
	}
	if err := json.Unmarshal(payload, &st); err != nil {
		t.Fatal(err)
	}
	id, err := f.ca.Issue(f.identity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := st.Predicate
	r.Chain = id.ChainPEM()
	h := &held{s: Secrets{Identity: id, AttemptKey: f.keyOf(t, attempt), Objects: objects}}
	g := Grant{Attempt: attempt, Prefix: f.runner.prefix()}
	for name, body := range extra {
		if err := objects.Put(ctx, outputKey(g.Prefix, attempt, name), []byte(body)); err != nil {
			t.Fatal(err)
		}
		r.Outputs = append(r.Outputs, Output{Name: name, Kind: kind, SHA256: digest([]byte(body)), Size: int64(len(body))})
	}
	if err := h.attest(ctx, g, r); err != nil {
		t.Fatal(err)
	}
}

// A run of a custom query sends the pack it lives in, bound by digest, and the worker runs
// it from there: the coordinator's path to it means nothing in the worker.
func TestAnOperatorsQueryTravelsWithItsPack(t *testing.T) {
	root := pack(t)
	var got expand.RemoteTask
	f := newFixture(t, func(ctx context.Context, tk expand.RemoteTask, dir string) (expand.RemoteOutput, error) {
		got = tk
		return analysis(ctx, tk, dir)
	})
	var prepared string
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.Pack = func(_ context.Context, root string) error { prepared = root; return nil }
		}
		return w, err
	}
	f.runner.PackRoot = func() string { return root }
	tk := task()
	tk.Findings = []string{"/image/ql/Shipped.ql", filepath.Join(root, "src", "A.ql")}

	if _, err := f.runner.Run(context.Background(), tk, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(got.Findings) != 2 || got.Findings[0] != "/image/ql/Shipped.ql" ||
		got.Findings[1] != filepath.Join(prepared, "src", "A.ql") || prepared == "" || strings.HasPrefix(prepared, root) {
		t.Errorf("the worker ran %v with the pack prepared at %q; want the shipped query as named and the custom one from its own copy",
			got.Findings, prepared)
	}
	if g := grantOf(t, f.ledger.entries[0]); g.PackSHA256 == "" || !slices.Equal(g.Task.PackFindings, []string{"src/A.ql"}) {
		t.Errorf("grant = %+v; want the pack bound by digest and the query named inside it", g)
	}
}

// puts counts what is written under each kind of key.
type puts struct {
	ObjectStore
	mu    sync.Mutex
	packs int
}

func (p *puts) Put(ctx context.Context, key string, body []byte) error {
	if strings.Contains(key, "pack") {
		p.mu.Lock()
		p.packs++
		p.mu.Unlock()
	}
	return p.ObjectStore.Put(ctx, key, body)
}

// A run's members share one pack, so it is handed over once, under its digest, however many of
// their workers read it.
func TestAPackIsHandedOverOnceForEveryWorkerThatReadsIt(t *testing.T) {
	root := pack(t)
	f := newFixture(t, analysis)
	f.runner.PackRoot = func() string { return root }
	counted := &puts{ObjectStore: f.runner.Objects}
	f.runner.Objects = counted
	for range 3 {
		tk := task()
		tk.Findings = []string{filepath.Join(root, "src", "A.ql")}
		if _, err := f.runner.Run(context.Background(), tk, t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	if counted.packs != 1 {
		t.Errorf("the pack was written %d times for three workers reading the same one", counted.packs)
	}
}
