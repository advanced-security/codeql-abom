package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// Nothing has been checked yet is not the same answer as nothing needs removing. A Settings page
// that showed the second for the first would tell an operator their corpus is tidy before anything
// had looked at it.
func TestStalenessIsUnknownUntilTheFirstCheckHasRun(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	got := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if got.Checked {
		t.Error("the corpus reports itself checked before any check ran")
	}
	if got.CheckedAt != "" {
		t.Errorf("CheckedAt = %q before any check ran", got.CheckedAt)
	}
}

// The endpoint answers whether or not a check has finished, because the page that reads it renders
// on first paint and a pending check must not hold it there.
func TestTheStalenessEndpointAnswersWhileNoCheckHasFinished(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	rec := get(t, srv, "/api/prune/staleness")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even with nothing checked", rec.Code)
	}
}

// What the check found is what the modal offers to act on, so it has to survive from the
// background check to the endpoint rather than being recomputed per request.
func TestAFinishedCheckIsReportedWithTheTimeItWasMade(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	srv.CheckStaleness()

	got := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if !got.Checked {
		t.Fatal("a check ran and the corpus still reports itself unchecked")
	}
	if got.CheckedAt == "" {
		t.Error("a check was made and did not say when")
	}
}

// An empty closure is an answer, not a fault: on a corpus nobody has enumerated there is nothing
// to be outside of, and reporting zero removable rows would read as a corpus already tidy.
func TestAnUnenumeratedCorpusSaysSoRatherThanReportingNothingToRemove(t *testing.T) {
	srv, _ := newServer(t)

	srv.CheckStaleness()

	got := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if !got.Checked {
		t.Fatal("the check did not record that it ran")
	}
	if got.Unavailable == "" {
		t.Error("nothing is enumerated, and the check reported a measurable corpus")
	}
}

// A prune changes the answer the startup check cached, and the page that raises the modal reads
// that cache rather than the corpus. Left stale it offers the same removal a second time, over
// numbers that no longer hold, and reports nothing tombstoned when something just was.
func TestApplyingAPruneRefreshesTheCachedCheck(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	srv.CheckStaleness()
	before := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if before.View.Removed.Nodes == 0 {
		t.Fatal("the fixture holds nothing outside the closure, so this proves nothing")
	}

	if rec := postControl(t, srv, "/api/prune/apply"); rec.Code != http.StatusOK {
		t.Fatalf("applying: status %d", rec.Code)
	}

	after := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if after.View.Removed.Nodes != 0 {
		t.Errorf("the cached check still offers %d nodes that were just removed",
			after.View.Removed.Nodes)
	}
	if after.View.Tombstoned == 0 {
		t.Error("something was tombstoned, and the cached check reports none")
	}
}

// checkoutDir gives the server a work directory holding the named working copies.
func checkoutDir(t *testing.T, srv *Server, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(dir, n, "src"), 0o755); err != nil {
			t.Fatalf("creating %s: %v", n, err)
		}
	}
	srv.WorkDir = dir
	return dir
}

// A prune tombstones the rows and leaves the working copy that produced them on disk. Nothing
// else ever removes it, so the check that offers the prune has to account for it too — otherwise
// the corpus shrinks and the disk does not.
func TestTheCheckNamesWorkingCopiesTheClosureNoLongerReaches(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	checkoutDir(t, srv, "stranger_thing", "stranger_thing@v4", "octo-org_app")

	srv.CheckStaleness()

	got := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if len(got.Checkouts) != 2 {
		t.Errorf("checkouts = %v, want the two trees of the repository outside the closure",
			got.Checkouts)
	}
	for _, c := range got.Checkouts {
		if c == "octo-org_app" {
			t.Error("a working copy the closure still reaches was offered for removal")
		}
	}
}

// Accepting the prune takes them off disk, because a removal that leaves the disk exactly as full
// as it was has not done the thing the operator approved.
func TestApplyingAPruneRemovesTheWorkingCopiesToo(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	dir := checkoutDir(t, srv, "stranger_thing", "octo-org_app")

	if rec := postControl(t, srv, "/api/prune/apply"); rec.Code != http.StatusOK {
		t.Fatalf("applying: status %d", rec.Code)
	}

	if _, err := os.Stat(filepath.Join(dir, "stranger_thing")); !os.IsNotExist(err) {
		t.Error("the working copy outside the closure is still on disk")
	}
	if _, err := os.Stat(filepath.Join(dir, "octo-org_app")); err != nil {
		t.Errorf("a working copy inside the closure was removed: %v", err)
	}
}

// With no work directory configured there is nothing to say about the disk, and saying nothing is
// the honest answer rather than reporting that no working copies are stale.
func TestWithNoWorkDirectoryNoClaimIsMadeAboutTheDisk(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	srv.CheckStaleness()

	got := decode[Staleness](t, get(t, srv, "/api/prune/staleness"))
	if len(got.Checkouts) != 0 {
		t.Errorf("checkouts = %v with no work directory configured", got.Checkouts)
	}
}
