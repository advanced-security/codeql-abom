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
	"syscall"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

const authoringIdentity = "spiffe://abom.local/ns/abom/sa/abom-worker-authoring"

// sessionFixture is a coordinator and an in-process worker of the authoring pool, the worker
// running session as its session.
func sessionFixture(t *testing.T, session SessionFunc) *fixture {
	t.Helper()
	f := newFixture(t, analysis)
	f.identity, f.session = authoringIdentity, session
	f.runner.Pool, f.runner.Identity = "authoring", authoringIdentity
	f.runner.ReportPoll = 5 * time.Millisecond
	return f
}

// changing is a session that changes the pack as one does: it reports what it read, edits the query,
// writes a fixture and the .expected, removes a fixture, runs the test, and reports again.
func changing(seen *SessionTask, read *[]string) SessionFunc {
	return func(_ context.Context, t SessionTask, in SessionRead, report func([]byte) error) (SessionOutcome, error) {
		*seen = t
		_ = filepath.WalkDir(t.Workspace, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				rel, _ := filepath.Rel(t.Workspace, p)
				*read = append(*read, filepath.ToSlash(rel))
			}
			return err
		})
		if err := report(append([]byte(`{"read":`), append(in.Snapshot, '}')...)); err != nil {
			return SessionOutcome{}, err
		}
		write := func(rel, body string) {
			file := filepath.Join(t.Workspace, filepath.FromSlash(rel))
			_ = os.MkdirAll(filepath.Dir(file), 0o755)
			_ = os.WriteFile(file, []byte(body), 0o644)
		}
		write("src/A.ql", "select 2\n")
		write("test/A/.github/workflows/new.yml", "on: pull_request\n")
		write("test/A/A.expected", "| 2 |\n")
		_ = os.Remove(filepath.Join(t.Workspace, "test", "A", ".github", "workflows", "ci.yml"))
		write("test/A/A.actual", "| 2 |\n")
		write("test/A/A.testproj/db-actions/fresh", "db")
		return SessionOutcome{Tests: "test/A"}, report([]byte(`{"ended":"finished"}`))
	}
}

// sessionRun is what a coordinator did with one session: the attempt and grant it began, each report
// it kept in order, and what it landed, if it landed anything.
type sessionRun struct {
	mu             sync.Mutex
	attempt, grant string
	kept           []Reported
	landed         *SessionLanding
	// landedAfter is how many reports were kept when it landed.
	landedAfter int
}

func (r *sessionRun) reports() []Reported {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.kept)
}

// runSession runs session 7 on the pack under root as the coordinator does, recording what it kept and
// landed; what calls does is done as well, and an error it answers is the coordinator's.
func (f *fixture) runSession(t *testing.T, root string, calls SessionCalls) (*sessionRun, error) {
	t.Helper()
	run := &sessionRun{}
	began, keep, land := calls.Began, calls.Keep, calls.Land
	calls.Began = func(attempt, grant string) error {
		run.mu.Lock()
		run.attempt, run.grant = attempt, grant
		run.mu.Unlock()
		if began != nil {
			return began(attempt, grant)
		}
		return nil
	}
	calls.Keep = func(r Reported) error {
		if keep != nil {
			if err := keep(r); err != nil {
				return err
			}
		}
		run.mu.Lock()
		run.kept = append(run.kept, r)
		run.mu.Unlock()
		return nil
	}
	calls.Land = func(l SessionLanding) error {
		if land != nil {
			if err := land(l); err != nil {
				return err
			}
		}
		run.mu.Lock()
		run.landed, run.landedAfter = &l, len(run.kept)
		run.mu.Unlock()
		return nil
	}
	err := f.runner.RunSession(context.Background(), root, SessionTask{Session: 7, Request: 3, By: "operator",
		Model: "claude-opus-5", Workspace: filepath.Join(t.TempDir(), "custom-queries")},
		SessionInputs{Snapshot: []byte(`{"session":7}`)}, nil, calls)
	return run, err
}

// A session runs in a worker on the pack and the snapshot its grant binds, unpacked where the grant
// says, and comes back as what it kept and every file it changed, each with the file it replaced,
// together with what its test runs left beside the test.
func TestASessionRunsInAWorkerAndComesBackAsWhatItChanged(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	root := pack(t)

	run, err := f.runSession(t, root, SessionCalls{})
	if err != nil {
		t.Fatal(err)
	}
	if run.landed == nil {
		t.Fatal("nothing landed")
	}
	landed := *run.landed
	if seen.Session != 7 || seen.Request != 3 || !strings.HasSuffix(seen.Workspace, "custom-queries") ||
		seen.PackSHA256 == "" {
		t.Errorf("the worker ran %+v", seen)
	}
	slices.Sort(read)
	if want := []string{"qlpack.yml", "src/A.ql", "test/A/.github/workflows/ci.yml", "test/A/A.expected",
		"test/A/A.qlref", "test/B/B.qlref", "test/qlpack.yml"}; !slices.Equal(read, want) {
		t.Errorf("the session read %v; want the pack without outputs or caches %v", read, want)
	}
	kept := run.reports()
	if len(kept) != 2 || string(kept[0].Body) != `{"read":{"session":7}}` || string(kept[1].Body) != `{"ended":"finished"}` {
		t.Fatalf("the reports kept are %+v", kept)
	}
	if kept[0].Seq != 1 || kept[0].Prev != "" || kept[1].Seq != 2 || kept[1].Prev != kept[0].Digest {
		t.Errorf("the reports kept are not one chain: %+v", kept)
	}
	if m := landed.Manifest; m.Reports != 2 || m.Head != kept[1].Digest || run.landedAfter != 2 {
		t.Errorf("the manifest names %d reports ending %s, and it landed after %d were kept", m.Reports, m.Head,
			run.landedAfter)
	}
	if landed.Manifest.Tests != "test/A" {
		t.Errorf("the session's test is %s", landed.Manifest.Tests)
	}
	changedAsTheSessionLeftIt(t, root, landed.Manifest.Changed)
	if string(landed.Files["src/A.ql"]) != "select 2\n" || len(landed.Files) != 3 {
		t.Errorf("the files that came back are %v", keys(landed.Files))
	}
	if string(landed.Actuals["test/A/A.actual"]) != "| 2 |\n" || len(landed.Databases["test/A/A.testproj"]) == 0 {
		t.Errorf("what the session's test runs left came back as %v and %v", keys(landed.Actuals), keys(landed.Databases))
	}
	if g := grantOf(t, f.ledger.entries[0]); g.Session == nil || g.Session.SnapshotSHA256 != digest([]byte(`{"session":7}`)) {
		t.Errorf("grant = %+v; want the session and the snapshot's digest", g)
	}
	if r := receiptOf(t, f.ledger.entries[1]); r.Verdict != ReceiptAccepted || r.Session != 7 {
		t.Errorf("receipt = %+v; want the session's result accepted", r)
	}
}

// changedAsTheSessionLeftIt checks that changed names exactly the changes changing() makes, each
// with the digest of the file under root it replaced.
func changedAsTheSessionLeftIt(t *testing.T, root string, manifest []Changed) {
	t.Helper()
	changed := map[string]Changed{}
	for _, c := range manifest {
		changed[c.Path] = c
	}
	original := func(rel string) string {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return digest(body)
	}
	for path, want := range map[string]Changed{
		"src/A.ql":                         {Before: original("src/A.ql"), After: digest([]byte("select 2\n"))},
		"test/A/A.expected":                {Before: original("test/A/A.expected"), After: digest([]byte("| 2 |\n"))},
		"test/A/.github/workflows/new.yml": {After: digest([]byte("on: pull_request\n"))},
		"test/A/.github/workflows/ci.yml":  {Before: original("test/A/.github/workflows/ci.yml")},
	} {
		if got := changed[path]; got.Before != want.Before || got.After != want.After {
			t.Errorf("%s changed as %+v, want %+v", path, got, want)
		}
	}
	if len(changed) != 4 {
		t.Errorf("the manifest names %d changes, want only the session's 4: %+v", len(changed), manifest)
	}
}

func keys[V any](m map[string]V) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// A worker runs no session on a snapshot other than the one granted: one swapped in the object
// store is refused before the session starts.
func TestAWorkerRunsNoSessionOnASnapshotOtherThanTheOneGranted(t *testing.T) {
	ran := false
	f := sessionFixture(t, func(context.Context, SessionTask, SessionRead, func([]byte) error) (SessionOutcome, error) {
		ran = true
		return SessionOutcome{}, nil
	})
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.Objects = swappingSnapshot{w.Objects}
		}
		return w, err
	}
	_, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || !strings.Contains(err.Error(), "not the snapshot granted") || ran {
		t.Errorf("a swapped snapshot answered %v, and the session ran: %v", err, ran)
	}
}

// swappingSnapshot hands a worker another snapshot than the one the coordinator put.
type swappingSnapshot struct{ ObjectStore }

func (s swappingSnapshot) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if strings.HasSuffix(key, "/in/snapshot.json") {
		return []byte(`{"session":8}`), nil
	}
	return s.ObjectStore.Get(ctx, key, limit)
}

// A worker's identity may sign anything, so what it says a session changed is accepted only as a
// whole that agrees with itself: a file it did not list, or what a run left outside the bound test
// directory, refuses the result, and nothing is landed.
func TestASessionResultThatDisagreesWithItselfIsRefusedWhole(t *testing.T) {
	for name, forged := range map[string]struct {
		kind  string
		extra map[string]string
	}{
		"a file it did not list":          {KindFile, map[string]string{"files/src/B.ql": "select 3\n"}},
		"a run's output outside the test": {KindActual, map[string]string{"actual/test/B/B.actual": "| 3 |\n"}},
		"a second manifest":               {KindManifest, map[string]string{"session/other.json": "{}"}},
	} {
		t.Run(name, func(t *testing.T) {
			var seen SessionTask
			var read []string
			f := sessionFixture(t, changing(&seen, &read))
			f.launch.After = func(objects ObjectStore, attempt string) {
				forgeAs(t, f, objects, attempt, forged.kind, forged.extra)
			}
			run, err := f.runSession(t, pack(t), SessionCalls{})
			if err == nil || run.landed != nil {
				t.Errorf("a result with %s answered %v, landed: %v", name, err, run.landed != nil)
			}
			if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); r.Verdict != ReceiptRefused {
				t.Errorf("receipt = %+v; want it refused", r)
			}
		})
	}
}

// A session whose landing is refused is recorded so: the receipt names why.
func TestASessionLandingRefusedIsRecordedRefused(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	_, err := f.runSession(t, pack(t), SessionCalls{Land: func(SessionLanding) error { return os.ErrExist }})
	if err == nil {
		t.Fatal("a refused landing answered nothing")
	}
	if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); r.Verdict != ReceiptRefused || r.Reason == "" {
		t.Errorf("receipt = %+v; want it refused with the reason", r)
	}
}

// A worker given no session to run refuses a session grant, and runs nothing.
func TestAWorkerThatRunsNoSessionsRefusesASessionGrant(t *testing.T) {
	f := sessionFixture(t, nil)
	if _, err := f.runSession(t, pack(t), SessionCalls{}); err == nil {
		t.Error("a worker that runs no sessions ran one")
	}
}

// resignWith signs the result a worker signed for attempt again, as the pool's identity and, unless it
// is nil, with key.
func resignWith(t *testing.T, f *fixture, objects ObjectStore, attempt string, key *AttemptKey) {
	t.Helper()
	resignAs(t, f, objects, attempt, key, func(*Result) {})
}

// resign changes the result a worker signed for attempt, and signs it again as the pool's identity and
// the attempt's key would.
func resign(t *testing.T, f *fixture, objects ObjectStore, attempt string, change func(*Result)) {
	t.Helper()
	resignAs(t, f, objects, attempt, f.keyOf(t, attempt), change)
}

func resignAs(t *testing.T, f *fixture, objects ObjectStore, attempt string, key *AttemptKey, change func(*Result)) {
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
	r := st.Predicate
	change(&r)
	id, err := f.ca.Issue(f.identity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r.Chain = id.ChainPEM()
	h := &held{s: Secrets{Identity: id, AttemptKey: key, Objects: objects}}
	if err := h.attest(ctx, Grant{Attempt: attempt, Prefix: f.runner.prefix()}, r); err != nil {
		t.Fatal(err)
	}
}

// forgeManifest rewrites the manifest a session's worker signed, and signs the result again.
func forgeManifest(t *testing.T, f *fixture, objects ObjectStore, attempt string, change func(*SessionManifest)) {
	t.Helper()
	ctx := context.Background()
	key := outputKey(f.runner.prefix(), attempt, manifestName)
	body, err := objects.Get(ctx, key, maxObject)
	if err != nil {
		t.Fatal(err)
	}
	var m SessionManifest
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	change(&m)
	if body, err = json.Marshal(m); err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, key, body); err != nil {
		t.Fatal(err)
	}
	resign(t, f, objects, attempt, func(r *Result) {
		for i := range r.Outputs {
			if r.Outputs[i].Name == manifestName {
				r.Outputs[i].SHA256, r.Outputs[i].Size = digest(body), int64(len(body))
			}
		}
	})
}

// What a session changed lands only once every report it made is kept, ending with the one its worker
// says it made last: a result claiming more reports, fewer, or another last one lands nothing, since
// landing it would keep work nobody can audit.
func TestASessionLandsOnlyOnceEveryReportItMadeIsKept(t *testing.T) {
	for name, change := range map[string]func(*SessionManifest){
		"a report nobody kept":   func(m *SessionManifest) { m.Reports++ },
		"one report fewer":       func(m *SessionManifest) { m.Reports-- },
		"another report as last": func(m *SessionManifest) { m.Head = digest([]byte("elsewhere")) },
	} {
		t.Run(name, func(t *testing.T) {
			var seen SessionTask
			var read []string
			f := sessionFixture(t, changing(&seen, &read))
			f.launch.After = func(objects ObjectStore, attempt string) {
				forgeManifest(t, f, objects, attempt, change)
			}
			run, err := f.runSession(t, pack(t), SessionCalls{})
			if err == nil || run.landed != nil {
				t.Errorf("a result with %s answered %v, landed: %v", name, err, run.landed != nil)
			}
			if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); r.Verdict != ReceiptRefused {
				t.Errorf("receipt = %+v; want it refused", r)
			}
		})
	}
}

// A report deleted before the coordinator read it leaves a gap nothing after it is read across, so
// nothing the session changed lands.
func TestASessionWhoseReportWentMissingLandsNothing(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	f.runner.ReportPoll = time.Hour
	f.launch.After = func(_ ObjectStore, attempt string) {
		if err := os.Remove(filepath.Join(string(f.objects), filepath.FromSlash(reportKey(f.runner.prefix(), attempt, 1)))); err != nil {
			t.Fatal(err)
		}
	}
	run, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || run.landed != nil || len(run.reports()) != 0 {
		t.Errorf("a session missing its first report answered %v, kept %d and landed: %v", err, len(run.reports()),
			run.landed != nil)
	}
}

// A manifest is accepted only as one change to each plain path inside the pack, each change that left
// a file having it come back: a forged one is refused before anything lands.
func TestASessionManifestThatDoesNotHoldIsRefused(t *testing.T) {
	for name, change := range map[string]func(*SessionManifest){
		"a change outside the pack": func(m *SessionManifest) {
			m.Changed = append(m.Changed, Changed{Path: "../outside", Before: digest([]byte("x"))})
		},
		"one file changed twice": func(m *SessionManifest) {
			m.Changed = append(m.Changed, Changed{Path: "src/A.ql", After: digest([]byte("select 3\n"))})
		},
		"a listed file that did not come back": func(m *SessionManifest) {
			m.Changed = append(m.Changed, Changed{Path: "src/C.ql", After: digest([]byte("x"))})
		},
		"a change that changes nothing": func(m *SessionManifest) {
			m.Changed = append(m.Changed, Changed{Path: "src/D.ql"})
		},
		"a test directory outside the pack": func(m *SessionManifest) { m.Tests = "../test" },
	} {
		t.Run(name, func(t *testing.T) {
			var seen SessionTask
			var read []string
			f := sessionFixture(t, changing(&seen, &read))
			f.launch.After = func(objects ObjectStore, attempt string) {
				forgeManifest(t, f, objects, attempt, change)
			}
			if run, err := f.runSession(t, pack(t), SessionCalls{}); err == nil || run.landed != nil {
				t.Errorf("a manifest with %s answered %v, landed: %v", name, err, run.landed != nil)
			}
		})
	}
}

// What a session's test runs left lands beside the test the coordinator bound it to, each database
// whole; anything named beside another test lands nothing.
func TestWhatASessionsTestRunsLeftLandsBesideItsTest(t *testing.T) {
	root := pack(t)
	db := filepath.Join(t.TempDir(), "A.testproj")
	if err := os.MkdirAll(filepath.Join(db, "db-actions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(db, "db-actions", "fresh"), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := tarGz(db, []string{filepath.Join(db, "db-actions", "fresh")}, databaseLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err := LandBeside(root, "test/A", map[string][]byte{"test/B/B.actual": []byte("x")}, nil); err == nil {
		t.Error("a run's output beside another test landed")
	}
	if err := LandBeside(root, "test/A", map[string][]byte{"test/A/A.actual": []byte("| 2 |\n")},
		map[string][]byte{"test/A/A.testproj": archive}); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "test", "A", "A.actual")); string(body) != "| 2 |\n" {
		t.Errorf("A.actual = %q", body)
	}
	if _, err := os.Stat(filepath.Join(root, "test", "A", "A.testproj", "db-actions", "fresh")); err != nil {
		t.Errorf("the database did not land: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "test", "A", "A.testproj", "db-actions", "old")); err == nil {
		t.Error("the previous database survived beside the new one")
	}
}

// A session's reports are kept while it works, not when it ends: a session waits on its first report
// being kept before it goes on.
func TestASessionsReportsAreKeptWhileItWorks(t *testing.T) {
	first := make(chan struct{})
	var once sync.Once
	f := sessionFixture(t, func(_ context.Context, t SessionTask, _ SessionRead, report func([]byte) error) (SessionOutcome, error) {
		if err := report([]byte(`{"n":1}`)); err != nil {
			return SessionOutcome{}, err
		}
		select {
		case <-first:
		case <-time.After(10 * time.Second):
			return SessionOutcome{}, errors.New("the first report was not kept while the session worked")
		}
		return SessionOutcome{}, report([]byte(`{"n":2}`))
	})
	run, err := f.runSession(t, pack(t), SessionCalls{Keep: func(Reported) error {
		once.Do(func() { close(first) })
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if kept := run.reports(); len(kept) != 2 || run.landed == nil {
		t.Errorf("kept %d reports, landed: %v", len(kept), run.landed != nil)
	}
}

// working is a session that edits the query, reports, and works on until it is stopped, reporting that
// it was.
func working(t *testing.T, during func(attempt string)) (SessionFunc, *string) {
	attempt := new(string)
	return func(ctx context.Context, s SessionTask, _ SessionRead, report func([]byte) error) (SessionOutcome, error) {
		if err := os.WriteFile(filepath.Join(s.Workspace, "src", "A.ql"), []byte("select 2\n"), 0o644); err != nil {
			return SessionOutcome{}, err
		}
		if err := report([]byte(`{"n":1}`)); err != nil {
			return SessionOutcome{}, err
		}
		if during != nil {
			during(*attempt)
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			return SessionOutcome{}, errors.New("the session was never stopped")
		}
		return SessionOutcome{}, report([]byte(`{"ended":"stopped"}`))
	}, attempt
}

// Stopping a session asks its worker to stop it, and what it changed lands with every report it made,
// the last saying it was stopped, as a session stopped in the single binary keeps what it wrote.
func TestAStoppedSessionLandsWhatItChanged(t *testing.T) {
	session, _ := working(t, nil)
	f := sessionFixture(t, session)
	stop := make(chan struct{})
	var once sync.Once
	run, err := f.runSession(t, pack(t), SessionCalls{Stop: stop, Keep: func(Reported) error {
		once.Do(func() { close(stop) })
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	kept := run.reports()
	if len(kept) != 2 || string(kept[1].Body) != `{"ended":"stopped"}` || run.landed == nil {
		t.Fatalf("kept %d reports, landed: %v", len(kept), run.landed != nil)
	}
	if body := run.landed.Files["src/A.ql"]; string(body) != "select 2\n" {
		t.Errorf("the stopped session landed %q", body)
	}
}

// killing is a launcher whose workers are killed once they have worked: whatever they signed is gone,
// and waiting on one answers how it died.
type killing struct{ *InProcess }

func (k killing) Wait(ctx context.Context, attempt string) error {
	_ = k.InProcess.Wait(ctx, attempt)
	return errors.New("job failed: OOMKilled")
}

// A worker killed before it signs what its session left lands nothing, and what it reported before it
// died is still kept as the record of what the session did.
func TestAKilledWorkerLandsNothingAndItsReportsStay(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	f.launch.After = func(_ ObjectStore, attempt string) {
		if err := os.Remove(filepath.Join(string(f.objects), filepath.FromSlash(attestationKey(f.runner.prefix(), attempt)))); err != nil {
			t.Fatal(err)
		}
	}
	f.runner.Launch = killing{f.launch}
	run, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || !strings.Contains(err.Error(), "OOMKilled") || run.landed != nil {
		t.Errorf("a killed worker answered %v, landed: %v", err, run.landed != nil)
	}
	if kept := run.reports(); len(kept) != 2 {
		t.Errorf("kept %d of the killed session's reports, want both it wrote", len(kept))
	}
}

// A restarted coordinator holds no attempt's key, so it reads the grant it kept beside the attempt back
// by the digest its store holds, and from it the key each report must be signed with. A worker can
// write that grant too, so one that is not the grant of that digest is refused, and nothing is kept.
func TestARestartedCoordinatorReadsAnAttemptsKeyFromTheGrantItKept(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	var attempt, grant string
	if _, err := f.runSession(t, pack(t), SessionCalls{Began: func(a, g string) error {
		attempt, grant = a, g
		return nil
	}}); err != nil {
		t.Fatal(err)
	}
	restarted := func() *Runner {
		return &Runner{Point: f.runner.Point, Objects: f.objects, Launch: f.launch, Roots: f.runner.Roots,
			Pool: f.runner.Pool, Identity: f.runner.Identity, Timeout: time.Minute}
	}
	var kept []Reported
	if err := restarted().Reports(context.Background(), attempt, grant, 0, "", func(r Reported) error {
		kept = append(kept, r)
		return nil
	}); err != nil || len(kept) != 2 {
		t.Fatalf("a restarted coordinator kept %d reports: %v", len(kept), err)
	}

	// A worker swaps the grant for one naming a key it holds, and signs every report again with it.
	another, _, err := NewAttemptKey()
	if err != nil {
		t.Fatal(err)
	}
	g := grantIn(t, mustGet(t, f.objects, grantKey(f.runner.prefix(), attempt)))
	g.AttemptKey = another.PublicPEM()
	swapped, _, err := f.runner.grant(context.Background(), g, "a swapped grant")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(swapped)
	if err := f.objects.Put(context.Background(), grantKey(f.runner.prefix(), attempt), body); err != nil {
		t.Fatal(err)
	}
	pool, err := f.ca.Issue(authoringIdentity, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	prev := ""
	for seq := int64(1); seq <= 2; seq++ {
		forged := reportAs(t, pool, another, attempt, grant, seq, prev, `{}`)
		if err := f.objects.Put(context.Background(), reportKey(f.runner.prefix(), attempt, seq), forged); err != nil {
			t.Fatal(err)
		}
		prev = digest(forged)
	}
	kept = nil
	err = restarted().Reports(context.Background(), attempt, grant, 0, "", func(r Reported) error {
		kept = append(kept, r)
		return nil
	})
	if err == nil || len(kept) != 0 {
		t.Errorf("with its grant swapped, a restarted coordinator answered %v and kept %d reports", err, len(kept))
	}
}

func mustGet(t *testing.T, objects ObjectStore, key string) []byte {
	t.Helper()
	body, err := objects.Get(context.Background(), key, maxSigned)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// reportAs is a report for attempt signed by id and, unless it is nil, the attempt key key, for grant,
// at seq after prev. It runs in a worker's goroutine, so it fails the test without stopping it.
func reportAs(t *testing.T, id *Identity, key *AttemptKey, attempt, grant string, seq int64, prev string, body string) []byte {
	t.Helper()
	predicate, err := json.Marshal(SessionReport{Attempt: attempt, Grant: grant, Identity: id.ID, Chain: id.ChainPEM(),
		Seq: seq, Prev: prev, Body: json.RawMessage(body)})
	if err != nil {
		t.Error(err)
		return nil
	}
	signers := []authz.Signer{id}
	if key != nil {
		signers = append(signers, key)
	}
	env, err := authz.SealBy(context.Background(), authz.Statement{PredicateType: authz.PredicateSessionReport,
		Predicate: predicate, Subject: []authz.Subject{{Name: attempt, Digest: map[string]string{"sha256": grant}}}}, signers...)
	if err != nil {
		t.Error(err)
		return nil
	}
	signed, _ := json.Marshal(env)
	return signed
}

// A worker's identity can sign anything, and every authoring worker shares it, so a report is kept only
// as the next one of this attempt's chain, signed by the pool's identity and this attempt's key for this
// grant. Any other stops the worker, lands nothing, and is recorded refused; what was kept before it
// stays.
func TestAReportThatIsNotTheNextOfThisAttemptsChainIsRefused(t *testing.T) {
	// signers are who may sign a forged report: the pool's own identity, and another pool's, both
	// issued by the roots the coordinator trusts; this attempt's key, and another attempt's.
	type signers struct {
		pool, another *Identity
		key, other    *AttemptKey
	}
	for name, forge := range map[string]func(t *testing.T, by signers, first []byte, attempt, grant string) []byte{
		"well formed, as a control": func(t *testing.T, by signers, first []byte, attempt, grant string) []byte {
			return reportAs(t, by.pool, by.key, attempt, grant, 2, digest(first), `{}`)
		},
		"signed by another pool's identity": func(t *testing.T, by signers, first []byte, attempt, grant string) []byte {
			return reportAs(t, by.another, by.key, attempt, grant, 2, digest(first), `{}`)
		},
		"signed by the pool's identity alone": func(t *testing.T, by signers, first []byte, attempt, grant string) []byte {
			return reportAs(t, by.pool, nil, attempt, grant, 2, digest(first), `{}`)
		},
		"signed with another attempt's key": func(t *testing.T, by signers, first []byte, attempt, grant string) []byte {
			return reportAs(t, by.pool, by.other, attempt, grant, 2, digest(first), `{}`)
		},
		"for another attempt": func(t *testing.T, by signers, first []byte, _, grant string) []byte {
			return reportAs(t, by.pool, by.key, "elsewhere", grant, 2, digest(first), `{}`)
		},
		"for another grant": func(t *testing.T, by signers, first []byte, attempt, _ string) []byte {
			return reportAs(t, by.pool, by.key, attempt, digest([]byte("another grant")), 2, digest(first), `{}`)
		},
		"out of its place": func(t *testing.T, by signers, first []byte, attempt, grant string) []byte {
			return reportAs(t, by.pool, by.key, attempt, grant, 3, digest(first), `{}`)
		},
		"following another report": func(t *testing.T, by signers, _ []byte, attempt, grant string) []byte {
			return reportAs(t, by.pool, by.key, attempt, grant, 2, digest([]byte("another report")), `{}`)
		},
		"a replay of the first": func(_ *testing.T, _ signers, first []byte, _, _ string) []byte { return first },
		"longer than a report may be": func(*testing.T, signers, []byte, string, string) []byte {
			return make([]byte, maxReport+1)
		},
		"not signed at all": func(*testing.T, signers, []byte, string, string) []byte {
			return []byte(`{"payload":"e30=","signatures":[]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var f *fixture
			var grant string
			var by signers
			stopped := false
			session, attempt := working(t, func(attempt string) {
				ctx := context.Background()
				first, err := f.objects.Get(ctx, reportKey(f.runner.prefix(), attempt, 1), maxReport)
				if err != nil {
					t.Error(err)
					return
				}
				by.key = f.keyOf(t, attempt)
				if err := f.objects.Put(ctx, reportKey(f.runner.prefix(), attempt, 2), forge(t, by, first, attempt, grant)); err != nil {
					t.Error(err)
				}
			})
			f = sessionFixture(t, func(ctx context.Context, s SessionTask, snap SessionRead, report func([]byte) error) (SessionOutcome, error) {
				return session(ctx, s, snap, func(body []byte) error {
					if string(body) == `{"ended":"stopped"}` {
						// What it would report next takes the forged report's place, so it is not written.
						stopped = true
						return nil
					}
					return report(body)
				})
			})
			var err error
			if by.pool, err = f.ca.Issue(authoringIdentity, time.Hour); err != nil {
				t.Fatal(err)
			}
			if by.another, err = f.ca.Issue(discovery, time.Hour); err != nil {
				t.Fatal(err)
			}
			if by.other, _, err = NewAttemptKey(); err != nil {
				t.Fatal(err)
			}
			// A report 2 that is kept stops the session, which is all a well-formed one leads to here.
			stop := make(chan struct{})
			run, err := f.runSession(t, pack(t), SessionCalls{Stop: stop, Began: func(a, g string) error {
				*attempt, grant = a, g
				return nil
			}, Keep: func(r Reported) error {
				if r.Seq == 2 {
					close(stop)
				}
				return nil
			}})
			if name == "well formed, as a control" {
				// The forgery is accepted as the session's report, and what it went on to report is not.
				if kept := run.reports(); err == nil || len(kept) != 2 || string(kept[1].Body) != `{}` {
					t.Errorf("a well-formed report 2 answered %v and kept %+v", err, kept)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "report 2") || run.landed != nil {
				t.Errorf("a report %s answered %v, landed: %v", name, err, run.landed != nil)
			}
			if name == "longer than a report may be" && !errors.Is(err, ErrTooLarge) {
				t.Errorf("a report longer than may be was refused as %v, not for its length", err)
			}
			if kept := run.reports(); len(kept) != 1 || kept[0].Seq != 1 {
				t.Errorf("kept %+v; want the first report alone", kept)
			}
			if !stopped {
				t.Error("the worker whose report was refused was not stopped")
			}
			if r := receiptOf(t, f.ledger.entries[len(f.ledger.entries)-1]); r.Verdict != ReceiptRefused ||
				!strings.Contains(r.Reason, "report 2") {
				t.Errorf("receipt = %+v; want the report refused", r)
			}
		})
	}
}

// A worker signs each report with its identity as it is now, renewed under a session that outlasts the
// certificate it began with, and never with another identity than the one it began as.
func TestAWorkerSignsAsItsIdentityRenewed(t *testing.T) {
	ca, err := newLocalCA("abom.local")
	if err != nil {
		t.Fatal(err)
	}
	began, _ := ca.Issue(authoringIdentity, time.Hour)
	renewed, _ := ca.Issue(authoringIdentity, time.Hour)
	other, _ := ca.Issue(discovery, time.Hour)
	for name, c := range map[string]struct {
		renew func() (*Identity, error)
		want  *Identity
	}{
		"renewed":               {func() (*Identity, error) { return renewed, nil }, renewed},
		"not renewed":           {nil, began},
		"renewed as another":    {func() (*Identity, error) { return other, nil }, began},
		"failing to be renewed": {func() (*Identity, error) { return nil, errors.New("no SVID") }, began},
	} {
		h := &held{s: Secrets{Identity: began, Renew: c.renew}}
		if got := h.identity(); got != c.want {
			t.Errorf("%s: the worker signs as %v", name, got.ID)
		}
	}
}

// What a session left is read from the pack it shares with the agent, which can still write once the
// session has ended: through no link, from nothing but a regular file, within the pack's bound, and
// only as exactly what the session left.
func TestWhatASessionLeftIsReadOnlyAsItLeftIt(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := func(t *testing.T, target, name string) {
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		make  func(t *testing.T, root string)
		limit int64
		want  string
	}{
		"a file changed since": {func(t *testing.T, root string) {
			write(t, root, "src/A.ql", "select 3\n")
		}, packLimit, "changed after the session ended"},
		"a link to what it left": {func(t *testing.T, root string) {
			write(t, root, "src/B.ql", "select 2\n")
			link(t, "B.ql", filepath.Join(root, "src", "A.ql"))
		}, packLimit, "not a regular file"},
		"a directory linked out of the pack": {func(t *testing.T, root string) {
			write(t, filepath.Dir(outside), "A.ql", "select 2\n")
			link(t, filepath.Dir(outside), filepath.Join(root, "src"))
		}, packLimit, "escapes"},
		"a pipe the agent holds open": {func(t *testing.T, root string) {
			fifo := filepath.Join(root, "src", "A.ql")
			if err := os.MkdirAll(filepath.Dir(fifo), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(fifo, 0o644); err != nil {
				t.Fatal(err)
			}
			held, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { held.Close() })
		}, packLimit, "not a regular file"},
		"more than the bound": {func(t *testing.T, root string) {
			write(t, root, "src/A.ql", "select 2\n")
		}, 4, "longer than"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			c.make(t, root)
			pack, err := os.OpenRoot(root)
			if err != nil {
				t.Fatal(err)
			}
			defer pack.Close()
			done := make(chan error, 1)
			go func() {
				files, err := readLeft(pack, []Changed{{Path: "src/A.ql", After: digest([]byte("select 2\n"))}}, c.limit)
				if err == nil {
					err = fmt.Errorf("read %q", files["src/A.ql"])
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !strings.Contains(err.Error(), c.want) {
					t.Errorf("what was left with %s answered %v; want it refused as %q", name, err, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("reading what was left with %s never returned", name)
			}
		})
	}
	root := t.TempDir()
	write(t, root, "src/A.ql", "select 2\n")
	pack, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer pack.Close()
	files, err := readLeft(pack, []Changed{{Path: "src/A.ql", After: digest([]byte("select 2\n"))},
		{Path: "src/B.ql", Before: digest([]byte("gone"))}}, packLimit)
	if err != nil || len(files) != 1 || string(files["src/A.ql"]) != "select 2\n" {
		t.Errorf("what the session left reads %v, %v", files, err)
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	file := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The files a session left land in the pack, so a result whose files come to more than the pack holds
// is refused before any of them is read.
func TestASessionLeavingMoreThanThePackHoldsIsRefusedUnread(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	f.launch.After = func(objects ObjectStore, attempt string) {
		resign(t, f, objects, attempt, func(r *Result) {
			for i := range r.Outputs {
				if r.Outputs[i].Kind == KindFile {
					r.Outputs[i].Size = packLimit / 2
				}
			}
		})
	}
	counted := &reads{ObjectStore: f.objects}
	f.runner.Objects = counted
	run, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || !strings.Contains(err.Error(), "more than the pack") || run.landed != nil {
		t.Errorf("a session leaving more than the pack holds answered %v, landed: %v", err, run.landed != nil)
	}
	for _, name := range counted.read() {
		if strings.HasPrefix(name, "files/") {
			t.Errorf("read %s of a session leaving more than the pack holds", name)
		}
	}
}

// A session that leaves more files changed than one ever needs is refused before any is uploaded.
func TestASessionLeavingMoreFilesChangedThanOneNeedsIsRefused(t *testing.T) {
	f := sessionFixture(t, func(_ context.Context, s SessionTask, _ SessionRead, report func([]byte) error) (SessionOutcome, error) {
		for i := range maxChanged + 1 {
			if err := os.WriteFile(filepath.Join(s.Workspace, "src", fmt.Sprintf("F%d.ql", i)), nil, 0o644); err != nil {
				return SessionOutcome{}, err
			}
		}
		return SessionOutcome{}, report([]byte(`{}`))
	})
	var uploaded []string
	f.launch.After = func(_ ObjectStore, attempt string) {
		dir := filepath.Join(string(f.objects), filepath.FromSlash(outputKey(f.runner.prefix(), attempt, "files")))
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			uploaded = append(uploaded, e.Name())
		}
	}
	run, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || !strings.Contains(err.Error(), "more than") || run.landed != nil || len(uploaded) != 0 {
		t.Errorf("a session leaving %d files changed answered %v, landed: %v, uploaded %d", maxChanged+1, err,
			run.landed != nil, len(uploaded))
	}
}

// A worker may write reports as fast as the object store takes them, and each is kept in the store, so
// a session's reports come to no more than its budget: past it the worker is stopped, nothing lands,
// and what it reported before stays.
func TestASessionReportingPastItsBudgetIsStopped(t *testing.T) {
	written := 0
	f := sessionFixture(t, func(ctx context.Context, _ SessionTask, _ SessionRead, report func([]byte) error) (SessionOutcome, error) {
		body := []byte(`{"pad":"` + strings.Repeat("x", 1000) + `"}`)
		for deadline := time.Now().Add(10 * time.Second); ctx.Err() == nil; time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				return SessionOutcome{}, errors.New("the session was never stopped")
			}
			if err := report(body); err != nil {
				return SessionOutcome{}, err
			}
			written++
		}
		return SessionOutcome{}, nil
	})
	f.runner.ReportBudget = 16 << 10
	run, err := f.runSession(t, pack(t), SessionCalls{})
	if err == nil || !strings.Contains(err.Error(), "budget") || run.landed != nil {
		t.Errorf("a session reporting past its budget answered %v, landed: %v", err, run.landed != nil)
	}
	if kept := len(run.reports()); kept == 0 || kept >= written {
		t.Errorf("kept %d of the %d reports written; want those within the budget", kept, written)
	}
}

// A pack is unpacked into the directory the agent shares, which can hold a link before the worker
// writes there: nothing unpacked follows one out of the directory.
func TestUnpackingFollowsNoLinkOutOfItsDirectory(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}
	archive, err := archive("pack", []string{"src/A.ql"}, func(int, int64) ([]byte, error) {
		return []byte("select 1\n"), nil
	}, packLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err := untarGz(archive, dir, packLimit); err == nil {
		t.Error("an archive was unpacked through a link out of its directory")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("unpacking wrote %d entries outside its directory", len(entries))
	}
}

// Preparing the pack runs the CLI over the pack's own files, and the CLI quotes a file it cannot read as
// the file it should be, a link to the worker's key included; so the pack is prepared where the agent
// beside the worker cannot write, and what preparing it made is then shared, less a cache.
func TestASessionsPackIsPreparedWhereTheAgentCannotWriteThenShared(t *testing.T) {
	var seen SessionTask
	var read []string
	f := sessionFixture(t, changing(&seen, &read))
	var prepared string
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.Pack = func(_ context.Context, root string) error {
				prepared = root
				return errors.Join(os.WriteFile(filepath.Join(root, "codeql-pack.lock.yml"), []byte("lockVersion: 1.0.0\n"), 0o644),
					os.MkdirAll(filepath.Join(root, ".codeql", "cache"), 0o755),
					os.WriteFile(filepath.Join(root, ".codeql", "cache", "big"), []byte("cache"), 0o644))
			}
		}
		return w, err
	}
	if _, err := f.runSession(t, pack(t), SessionCalls{}); err != nil {
		t.Fatal(err)
	}
	if prepared == "" || prepared == seen.Workspace || strings.HasPrefix(prepared, seen.Workspace+string(filepath.Separator)) ||
		!strings.HasPrefix(prepared, f.private) {
		t.Errorf("the pack was prepared at %q; want it in %s, not where the agent shares %s", prepared, f.private, seen.Workspace)
	}
	if !slices.Contains(read, "codeql-pack.lock.yml") || slices.Contains(read, ".codeql/cache/big") {
		t.Errorf("the session found %v; want what preparing the pack wrote, and no cache", read)
	}
	if _, err := os.Stat(prepared); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the pack prepared for the session is still there: %v", err)
	}
}

// testsPack is a pack holding one query with one test, and what an earlier run left beside it.
func testsPack(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	write(t, ws, "src/A.ql", "select 1\n")
	write(t, ws, "test/A/A.qlref", "A.ql\n")
	write(t, ws, "test/A/.github/workflows/ci.yml", "on: push\n")
	write(t, ws, "test/A/A.actual", "| earlier |\n")
	write(t, ws, "test/A/A.testproj/earlier", "x")
	write(t, ws, ".codeql/cache", "x")
	return ws
}

// The CLI follows a link in a test's code and copies whatever it names into the database it builds,
// which the agent beside a session's worker reads. So a session's tests run on a copy of the pack that
// the agent cannot write, holding the pack's regular files and none of what a run leaves; what the run
// leaves is put back beside each test as the run left it, dated as it dated it; and what the run says
// names the pack.
func TestASessionsTestsRunOnACopyOfThePackTheAgentCannotWrite(t *testing.T) {
	ws := testsPack(t)
	ran := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var copied string
	var read []string
	tests := PrivateTests(ws, filepath.Join(t.TempDir(), "tests"), func(_ context.Context, pack string, tests []string) (runner.TestResult, error) {
		copied = pack
		if want := filepath.Join(pack, "test", "A", "A.qlref"); !slices.Equal(tests, []string{want}) {
			t.Errorf("the run was handed %v, want %v", tests, []string{want})
		}
		err := filepath.WalkDir(pack, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(pack, p)
				read = append(read, filepath.ToSlash(rel))
			}
			return err
		})
		write(t, pack, "test/A/A.actual", "| now |\n")
		write(t, pack, "test/A/A.testproj/codeql-database.yml", "primaryLanguage: actions\n")
		if err := os.Chtimes(filepath.Join(pack, "test", "A", "A.testproj", "codeql-database.yml"), ran, ran); err != nil {
			t.Fatal(err)
		}
		real, _ := filepath.EvalSymlinks(pack)
		return runner.TestResult{Detail: "0 of 1 passed; " + filepath.Join(real, "test", "A", "A.qlref") + " failed"}, err
	})
	result, err := tests(context.Background(), []string{filepath.Join(ws, "test", "A", "A.qlref")})
	if err != nil {
		t.Fatal(err)
	}
	if copied == "" || copied == ws {
		t.Fatalf("the tests ran in %q, not a copy of %s", copied, ws)
	}
	slices.Sort(read)
	if want := []string{"src/A.ql", "test/A/.github/workflows/ci.yml", "test/A/A.qlref"}; !slices.Equal(read, want) {
		t.Errorf("the run could read %v, want %v", read, want)
	}
	if want := "0 of 1 passed; " + filepath.Join(ws, "test", "A", "A.qlref") + " failed"; result.Detail != want {
		t.Errorf("the run says %q, want %q", result.Detail, want)
	}
	if body, err := os.ReadFile(filepath.Join(ws, "test", "A", "A.actual")); err != nil || string(body) != "| now |\n" {
		t.Errorf("the .actual beside the test is %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(ws, "test", "A", "A.testproj", "earlier")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("what an earlier run left in the database is still there: %v", err)
	}
	info, err := os.Stat(filepath.Join(ws, "test", "A", "A.testproj", "codeql-database.yml"))
	if err != nil || !info.ModTime().Equal(ran) {
		t.Errorf("the database put back is dated %v, %v; the run dated it %v", info, err, ran)
	}
	if _, err := os.Stat(copied); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the copy is still there: %v", err)
	}
}

// A link or a pipe in the pack is the agent's doing, and the run reads neither: it is refused, naming
// what was there, before the CLI is handed anything.
func TestASessionsTestsRefuseALinkOrAPipeInThePack(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "tls.key")
	write(t, filepath.Dir(secret), "tls.key", "PRIVATE KEY")
	for what, plant := range map[string]func(ws string) error{
		"a link to a file":      func(ws string) error { return os.Symlink(secret, filepath.Join(ws, "test", "A", "leak.yml")) },
		"a link to a directory": func(ws string) error { return os.Symlink(filepath.Dir(secret), filepath.Join(ws, "src", "lib")) },
		"a pipe":                func(ws string) error { return syscall.Mkfifo(filepath.Join(ws, "test", "A", "ci.yml"), 0o600) },
	} {
		ws := testsPack(t)
		if err := plant(ws); err != nil {
			t.Fatal(err)
		}
		ran := false
		tests := PrivateTests(ws, filepath.Join(t.TempDir(), "tests"), func(context.Context, string, []string) (runner.TestResult, error) {
			ran = true
			return runner.TestResult{}, nil
		})
		done := make(chan error, 1)
		go func() {
			_, err := tests(context.Background(), []string{filepath.Join(ws, "test", "A", "A.qlref")})
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "not a regular file") || ran {
				t.Errorf("with %s in the pack the tests answered %v, ran: %v", what, err, ran)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("with %s in the pack the tests waited", what)
		}
	}
}

// The agent can plant a link where the run's leavings go back while the run is under way: what goes
// back replaces the link, and nothing is written where it leads.
func TestWhatASessionsTestsLeftGoesBackThroughNoLink(t *testing.T) {
	ws := testsPack(t)
	outside := t.TempDir()
	write(t, outside, "tls.key", "PRIVATE KEY")
	tests := PrivateTests(ws, filepath.Join(t.TempDir(), "tests"), func(_ context.Context, pack string, _ []string) (runner.TestResult, error) {
		for name, target := range map[string]string{"A.testproj": outside, "A.actual": filepath.Join(outside, "tls.key")} {
			if err := os.RemoveAll(filepath.Join(ws, "test", "A", name)); err != nil {
				return runner.TestResult{}, err
			}
			if err := os.Symlink(target, filepath.Join(ws, "test", "A", name)); err != nil {
				return runner.TestResult{}, err
			}
		}
		write(t, pack, "test/A/A.actual", "| now |\n")
		write(t, pack, "test/A/A.testproj/codeql-database.yml", "primaryLanguage: actions\n")
		return runner.TestResult{}, nil
	})
	if _, err := tests(context.Background(), []string{filepath.Join(ws, "test", "A", "A.qlref")}); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(outside, "tls.key")); err != nil || string(body) != "PRIVATE KEY" {
		t.Errorf("the file a planted link names reads %q, %v", body, err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 {
		t.Errorf("the directory a planted link names holds %d entries", len(entries))
	}
	for _, name := range []string{"A.actual", "A.testproj/codeql-database.yml"} {
		if info, err := os.Lstat(filepath.Join(ws, "test", "A", filepath.FromSlash(name))); err != nil || !info.Mode().IsRegular() {
			t.Errorf("%s did not go back as a file: %v, %v", name, info, err)
		}
	}
}

// One copy serves every run of a session, so its runs take turns.
func TestASessionsTestsRunOneAtATime(t *testing.T) {
	ws := testsPack(t)
	var mu sync.Mutex
	running, most := 0, 0
	tests := PrivateTests(ws, filepath.Join(t.TempDir(), "tests"), func(context.Context, string, []string) (runner.TestResult, error) {
		mu.Lock()
		running++
		most = max(most, running)
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return runner.TestResult{}, nil
	})
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			if _, err := tests(context.Background(), []string{filepath.Join(ws, "test", "A", "A.qlref")}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if most != 1 {
		t.Errorf("%d runs shared the copy at once", most)
	}
}

// database is a CodeQL database as a draft reads it: its manifest and its source archive, beside what
// a draft does not read.
func database(t *testing.T) string {
	t.Helper()
	db := t.TempDir()
	write(t, db, "codeql-database.yml", "sourceLocationPrefix: /src\n")
	write(t, db, "src.zip", "PK-source")
	write(t, db, "db-actions/default/cache/pages", "what a draft never reads")
	return db
}

// A draft reads what its grant binds of each picked repository's database, by digest, unpacked where
// the agent beside the worker does not share, and only what the draft tools read of it.
func TestADraftReadsTheDatabasesItsGrantBindsWhereTheAgentDoesNot(t *testing.T) {
	var seen SessionTask
	var got map[string]string
	var held []string
	f := sessionFixture(t, func(_ context.Context, s SessionTask, in SessionRead, report func([]byte) error) (SessionOutcome, error) {
		seen, got = s, in.Databases
		for _, dir := range in.Databases {
			_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
				if err == nil && d.Type().IsRegular() {
					rel, _ := filepath.Rel(dir, p)
					held = append(held, filepath.ToSlash(rel))
				}
				return err
			})
		}
		return SessionOutcome{}, report([]byte(`{}`))
	})
	archive, err := draftDatabase(database(t), databaseLimit)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runner.RunSession(context.Background(), pack(t), SessionTask{Session: 7, Run: 3, By: "operator",
		Model: "claude-opus-5", Workspace: filepath.Join(t.TempDir(), "custom-queries")},
		SessionInputs{Snapshot: []byte(`{"session":7}`), Databases: map[string][]byte{"octo/app": archive}}, nil,
		SessionCalls{Land: func(SessionLanding) error { return nil }}); err != nil {
		t.Fatal(err)
	}
	if seen.Databases["octo/app"] != digest(archive) {
		t.Errorf("the grant binds %v; want octo/app's database by its digest", seen.Databases)
	}
	dir := got["octo/app"]
	if rel, err := filepath.Rel(f.private, dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("octo/app's database was unpacked at %s, outside what the worker keeps from the agent", dir)
	}
	slices.Sort(held)
	if want := []string{"codeql-database.yml", "src.zip"}; !slices.Equal(held, want) {
		t.Errorf("the draft was handed %v; want only %v", held, want)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("octo/app's database is still at %s after the session: %v", dir, err)
	}
}

// launches is a launcher that keeps each launch it is handed.
type launches struct {
	Launcher
	mu   sync.Mutex
	seen []Launch
}

func (l *launches) Start(ctx context.Context, launch Launch) error {
	l.mu.Lock()
	l.seen = append(l.seen, launch)
	l.mu.Unlock()
	return l.Launcher.Start(ctx, launch)
}

// A draft's agent is offered ABOM's tools and nothing else, so its pod is sealed; a request's session's
// agent writes the query, so its pod is not.
func TestOnlyADraftsPodIsSealed(t *testing.T) {
	for _, draft := range []bool{false, true} {
		f := sessionFixture(t, func(_ context.Context, _ SessionTask, _ SessionRead, report func([]byte) error) (SessionOutcome, error) {
			return SessionOutcome{}, report([]byte(`{}`))
		})
		launched := &launches{Launcher: f.runner.Launch}
		f.runner.Launch = launched
		task := SessionTask{Session: 7, Request: 3, By: "operator", Model: "claude-opus-5",
			Workspace: filepath.Join(t.TempDir(), "custom-queries")}
		if draft {
			task.Request, task.Run = 0, 3
		}
		if err := f.runner.RunSession(context.Background(), pack(t), task, SessionInputs{Snapshot: []byte(`{"session":7}`)},
			nil, SessionCalls{Land: func(SessionLanding) error { return nil }}); err != nil {
			t.Fatal(err)
		}
		if len(launched.seen) != 1 || launched.seen[0].Sealed != draft {
			t.Errorf("a session drafting %v was launched %+v", draft, launched.seen)
		}
	}
}

// swappingDatabase hands a worker another database than the one the coordinator put.
type swappingDatabase struct{ ObjectStore }

func (s swappingDatabase) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if strings.Contains(key, "/in/databases/") {
		return archive("swapped", []string{"codeql-database.yml"}, func(int, int64) ([]byte, error) {
			return []byte("sourceLocationPrefix: /elsewhere\n"), nil
		}, databaseLimit)
	}
	return s.ObjectStore.Get(ctx, key, limit)
}

// A worker runs no draft on a database other than the one granted.
func TestADraftDatabaseSwappedInTheStoreIsRefused(t *testing.T) {
	ran := false
	f := sessionFixture(t, func(context.Context, SessionTask, SessionRead, func([]byte) error) (SessionOutcome, error) {
		ran = true
		return SessionOutcome{}, nil
	})
	worker := f.launch.Worker
	f.launch.Worker = func(secrets map[string]string) (*Worker, error) {
		w, err := worker(secrets)
		if w != nil {
			w.Objects = swappingDatabase{w.Objects}
		}
		return w, err
	}
	archive, err := draftDatabase(database(t), databaseLimit)
	if err != nil {
		t.Fatal(err)
	}
	err = f.runner.RunSession(context.Background(), pack(t), SessionTask{Session: 7, Run: 3, By: "operator",
		Model: "claude-opus-5", Workspace: filepath.Join(t.TempDir(), "custom-queries")},
		SessionInputs{Snapshot: []byte(`{"session":7}`), Databases: map[string][]byte{"octo/app": archive}}, nil,
		SessionCalls{Land: func(SessionLanding) error { return nil }})
	if err == nil || !strings.Contains(err.Error(), "not the database granted") || ran {
		t.Errorf("a swapped database answered %v, and the draft ran: %v", err, ran)
	}
}

// A coordinator holds every database it hands one draft at once, so together they are bounded, and
// the repository whose database passes the bound is named.
func TestADraftsDatabasesTogetherAreBounded(t *testing.T) {
	where := map[string]string{"octo/app": database(t), "octo/lib": database(t)}
	one, err := draftDatabase(where["octo/app"], databaseLimit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := draftDatabasesWithin(where, int64(len(one))*2+16); err != nil {
		t.Errorf("two databases within the bound answered %v", err)
	}
	_, err = draftDatabasesWithin(where, int64(len(one))+16)
	if err == nil || !strings.Contains(err.Error(), "octo/") {
		t.Errorf("two databases past the bound answered %v; want the one that passed it named", err)
	}
	if _, err := DraftDatabases(map[string]string{"octo/gone": filepath.Join(t.TempDir(), "gone")}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a database that is gone answered %v", err)
	}
}
