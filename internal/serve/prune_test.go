package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Pruning was reachable only from the command line, so the one operation that permanently changes
// what the corpus contains was the one nobody could see before running it.

// Explaining must never change anything: it is what an operator reads to decide.
func TestExplainingAPruneChangesNothing(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)

	got := decode[PruneView](t, get(t, srv, "/api/prune"))

	if got.Declared != 1 {
		t.Errorf("declared = %d, want the one declared repository", got.Declared)
	}
	if got.Removed.Nodes != 0 && liveNodes(t, s) != nodesInStore(t, s) {
		t.Error("explaining tombstoned something")
	}
	if got.Applied {
		t.Error("explaining reported itself as applied")
	}
}

// The whole point of the surface: an operator sees the cost before the irreversible half.
func TestMarkingReportsWhatItRemovedAndTakesItOutOfTheLiveViews(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	// actions/checkout belongs to nobody declared, but octo-org/app reaches it, so the closure
	// keeps it. A node outside the closure entirely is what must go.
	outside(t, s, "stranger/thing")
	before := liveNodes(t, s)

	rec := postControl(t, srv, "/api/prune/apply")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[PruneView](t, rec)

	if !got.Applied {
		t.Error("marking did not report itself as applied")
	}
	if got.Removed.Nodes == 0 {
		t.Fatal("marking removed nothing, so the surface would report a no-op as success")
	}
	if after := liveNodes(t, s); after >= before {
		t.Errorf("live nodes %d -> %d, want the marked ones gone from the live views", before, after)
	}
}

// Marking is reversible by re-analysis; compaction is not, and the two must not be one button.
func TestCompactingIsASeparateCallFromMarking(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	postControl(t, srv, "/api/prune/apply")
	marked := tombstoned(t, s)
	if marked == 0 {
		t.Fatal("nothing was marked, so compaction has nothing to prove")
	}

	if rec := postControl(t, srv, "/api/prune/compact"); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	if left := tombstoned(t, s); left != 0 {
		t.Errorf("%d tombstones survived compaction", left)
	}
}

// Both halves clone repositories' worth of work away permanently, so they carry the same guard as
// starting discovery.
func TestPruningRefusesANonLoopbackCaller(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)

	for _, path := range []string{"/api/prune/apply", "/api/prune/compact"} {
		req := controlRequest(path)
		req.Host = "example.com:8099"
		req.Header.Del(controlHeader)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("%s answered a non-loopback caller with success", path)
		}
	}
}

// An undeclared corpus must not be pruned to nothing on the strength of an empty closure.
func TestPruningRefusesWhenNothingIsDeclared(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	rec := postControl(t, srv, "/api/prune/apply")

	if rec.Code == http.StatusOK {
		t.Error("pruning an undeclared corpus must not succeed; it would remove everything")
	}
	if n := tombstoned(t, s); n != 0 {
		t.Errorf("%d nodes were tombstoned with nothing declared", n)
	}
}

// Found by pruning the real corpus and reading the page afterwards: it said "0 nodes and 0 edges
// across 838 repositories", which describes work that no longer exists. A repository with nothing
// live left in it has already been removed, whatever the closure says about it.
func TestARepositoryWithNothingLeftToRemoveIsNotCountedAgain(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	first := decode[PruneView](t, get(t, srv, "/api/prune"))
	if first.Removed.Repositories == 0 {
		t.Fatal("nothing was outside the closure, so the second reading proves nothing")
	}

	postControl(t, srv, "/api/prune/apply")
	again := decode[PruneView](t, get(t, srv, "/api/prune"))

	if again.Removed.Nodes != 0 || again.Removed.Edges != 0 {
		t.Fatalf("a second prune still removes %+v; it should have settled", again.Removed)
	}
	if again.Removed.Repositories != 0 {
		t.Errorf("repositories = %d with 0 nodes and 0 edges to remove, want 0: the count "+
			"describes work, and there is none", again.Removed.Repositories)
	}
}

// Pruning the graph without pruning the queue does not settle: queued rows can reintroduce nodes.
func TestPruningTakesTheQueueWithIt(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	queue(t, s, "octo-org", "app")
	queue(t, s, "stranger", "thing")

	before := decode[PruneView](t, get(t, srv, "/api/prune"))
	if before.Removed.Queued == 0 {
		t.Fatal("explaining reported no queue rows to remove, so the apply proves nothing")
	}

	postControl(t, srv, "/api/prune/apply")

	if left := queuedFor(t, s, "stranger", "thing"); left != 0 {
		t.Errorf("%d queue rows survive for a repository outside the closure", left)
	}
	if kept := queuedFor(t, s, "octo-org", "app"); kept == 0 {
		t.Error("pruning removed queued work for a repository inside the closure")
	}
}

// Explaining must not empty the queue: it is what an operator reads before deciding.
func TestExplainingDoesNotTouchTheQueue(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	queue(t, s, "stranger", "thing")

	get(t, srv, "/api/prune")

	if left := queuedFor(t, s, "stranger", "thing"); left == 0 {
		t.Error("explaining removed queue rows")
	}
}

// The state every new install passes through: an account is declared and enumeration has not
// produced a repository yet, so the closure is empty. Asking what a prune would cost changes
// nothing and must not be a fault — it was answered with a 500, which the Discovery page reported
// as "could not reach the server" on the one visit where nothing has gone wrong at all.
func TestAskingWhatAPruneCostsBeforeAnythingIsEnumerated(t *testing.T) {
	srv, s := newServer(t)
	declareOcto(t, s)

	rec := get(t, srv, "/api/prune")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	got := decode[PruneView](t, rec)

	if got.Unavailable == "" {
		t.Error("an empty closure was reported as a measurement rather than as unavailable")
	}
	// The trap this guards: a zero report reads as "nothing is outside your scope", which on an
	// unmeasured corpus is a false reassurance rather than a missing one.
	if got.Removed.Nodes != 0 || got.Removed.Edges != 0 || got.Removed.Repositories != 0 {
		t.Errorf("removal counted against an empty closure: %+v", got.Removed)
	}
}

// The destructive half keeps refusing outright: there is nothing to say no to on a read, and
// everything to say no to on a write that would tombstone the whole corpus.
func TestPruningRefusesAgainstAnEmptyClosure(t *testing.T) {
	srv, s := newServer(t)
	declareOcto(t, s)

	if rec := postControl(t, srv, "/api/prune/apply"); rec.Code == http.StatusOK {
		t.Fatalf("applying a prune against an empty closure was allowed: %s", rec.Body.String())
	}
}

func queue(t *testing.T, s *store.Store, owner, repo string) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at)
		VALUES (?, '', ?, ?, 1, 'pending', ?)`,
		owner+"/"+repo, owner, repo, "2026-09-07T00:00:00Z"); err != nil {
		t.Fatalf("queueing %s/%s: %v", owner, repo, err)
	}
}

func queuedFor(t *testing.T, s *store.Store, owner, repo string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE owner = ? AND repo = ?`,
		owner, repo).Scan(&n); err != nil {
		t.Fatalf("counting queue rows: %v", err)
	}
	return n
}

func postControl(t *testing.T, srv *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, controlRequest(path))
	return rec
}

func declareOcto(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo-org", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

// outside adds a repository and a node in it that the declared closure cannot reach.
func outside(t *testing.T, s *store.Store, fullName string) {
	t.Helper()
	id, err := s.UpsertRepository(fullName, "")
	if err != nil {
		t.Fatalf("UpsertRepository(%q): %v", fullName, err)
	}
	w := store.NewWriter(s, 0)
	if err := w.AddNode(store.Node{
		Key: fullName + "/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: id, Path: ".github/workflows/ci.yml",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

func liveNodes(t *testing.T, s *store.Store) int { return count(t, s, `abom_node_live`) }

func nodesInStore(t *testing.T, s *store.Store) int { return count(t, s, `abom_node`) }

func tombstoned(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_node WHERE removed_at IS NOT NULL`).Scan(&n); err != nil {
		t.Fatalf("counting tombstones: %v", err)
	}
	return n
}

func count(t *testing.T, s *store.Store, table string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}
