package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Removing what is no longer owned once took out what the page had shown as kept: the preview a
// person read and the removal the server carried out were two separate readings of the declared
// scope, and the scope can shrink between them — an organization whose membership a later `gh`
// check could not see stops being honored. Nothing a reviewed preview showed as kept is removed.

// applyPrune posts a removal naming a preview, as the interface does.
func applyPrune(t *testing.T, srv *Server, proof string, majority bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"proof": proof, "majority": majority})
	if err != nil {
		t.Fatal(err)
	}
	r := localRequest(http.MethodPost, "/api/prune/apply", bytes.NewReader(body))
	r.Host = "127.0.0.1:8080"
	r.Header.Set(controlHeader, "1")
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec
}

// applyReviewed reads the preview and applies exactly it, acknowledging whatever it says.
func applyReviewed(t *testing.T, srv *Server) *httptest.ResponseRecorder {
	t.Helper()
	view := decode[PruneView](t, get(t, srv, "/api/prune"))
	return applyPrune(t, srv, view.Proof, view.Majority)
}

// refused reads a removal that did not happen, which answers 409 with what would be removed now.
func refused(t *testing.T, rec *httptest.ResponseRecorder) PruneConflict {
	t.Helper()
	var out PruneConflict
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding a refusal: %v: %s", err, rec.Body.String())
	}
	return out
}

func TestARemovalNamingNoReviewedPreviewRemovesNothing(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	for _, proof := range []string{"", "never-issued"} {
		rec := applyPrune(t, srv, proof, true)
		if rec.Code == http.StatusOK {
			t.Errorf("proof %q: a removal nobody reviewed was applied: %s", proof, rec.Body.String())
		}
	}
	if rec := postControl(t, srv, "/api/prune/apply"); rec.Code == http.StatusOK {
		t.Errorf("a removal with no body was applied: %s", rec.Body.String())
	}
	if n := tombstoned(t, s); n != 0 {
		t.Errorf("%d nodes were tombstoned without a reviewed preview", n)
	}
}

func TestARemovalReviewedAgainstAWiderScopeRemovesNothingAndSaysWhatChanged(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	outside(t, s, "stranger/thing")
	// Both accounts declared: the stranger's repository is kept, and the preview says so.
	declare(t, s, "octo-org", "stranger")
	reviewed := decode[PruneView](t, get(t, srv, "/api/prune"))
	if reviewed.Proof == "" || reviewed.Removed.Nodes != 0 {
		t.Fatalf("the reviewed preview should keep both accounts and name itself: %+v", reviewed)
	}

	// Between reading and pressing, the scope narrows: an account stops counting as owned.
	declareOcto(t, s)

	rec := applyPrune(t, srv, reviewed.Proof, true)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 for a scope that shrank since it was reviewed: %s", rec.Code, rec.Body.String())
	}
	if n := tombstoned(t, s); n != 0 {
		t.Fatalf("%d nodes were tombstoned that the reviewed preview kept", n)
	}
	changed := refused(t, rec)
	if !strings.Contains(changed.Error, "stranger/thing") {
		t.Errorf("the refusal does not name what it would have removed that was kept: %q", changed.Error)
	}
	if changed.View.Removed.Nodes == 0 || changed.View.Proof == "" || changed.View.Proof == reviewed.Proof {
		t.Errorf("the refusal does not carry the removal as it now stands, to review afresh: %+v", changed.View)
	}

	// What it now says can be reviewed and applied like any other preview.
	if rec := applyPrune(t, srv, changed.View.Proof, changed.View.Majority); rec.Code != http.StatusOK {
		t.Fatalf("the fresh preview could not be applied: %d %s", rec.Code, rec.Body.String())
	}
}

func TestARemovalOfMostOfTheGraphIsAppliedOnlyWhenSaidOutright(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	for _, name := range []string{"stranger/one", "stranger/two", "stranger/three", "stranger/four"} {
		outside(t, s, name)
	}
	view := decode[PruneView](t, get(t, srv, "/api/prune"))
	if !view.Majority || view.Live.Nodes == 0 || view.Removed.Nodes*2 < view.Live.Nodes {
		t.Fatalf("four of seven live nodes outside the scope should read as most of the graph: %+v", view)
	}

	rec := applyPrune(t, srv, view.Proof, false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409 for an unacknowledged majority removal: %s", rec.Code, rec.Body.String())
	}
	if n := tombstoned(t, s); n != 0 {
		t.Fatalf("%d nodes were tombstoned before the majority was acknowledged", n)
	}
	again := refused(t, rec).View
	if rec := applyPrune(t, srv, again.Proof, true); rec.Code != http.StatusOK {
		t.Fatalf("an acknowledged majority removal was refused: %d %s", rec.Code, rec.Body.String())
	}
	if n := tombstoned(t, s); n != view.Removed.Nodes {
		t.Errorf("tombstoned %d, want the %d the preview named", n, view.Removed.Nodes)
	}
}

// A preview names one decision. Applied once, it is spent, and the next removal is read afresh.
func TestAReviewedPreviewIsAppliedOnce(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	view := decode[PruneView](t, get(t, srv, "/api/prune"))
	if rec := applyPrune(t, srv, view.Proof, view.Majority); rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if rec := applyPrune(t, srv, view.Proof, view.Majority); rec.Code == http.StatusOK {
		t.Errorf("a spent preview was applied a second time: %s", rec.Body.String())
	}
}

// A removal changes what the next preview reads. Offering the same rows again, or reporting nothing
// tombstoned when something just was, would ask for the same decision twice over stale numbers.
func TestAfterARemovalThePreviewOffersNothingAndCountsWhatIsTombstoned(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	before := decode[PruneView](t, get(t, srv, "/api/prune"))
	if before.Removed.Nodes == 0 {
		t.Fatal("the fixture holds nothing outside the closure, so this proves nothing")
	}
	if rec := applyPrune(t, srv, before.Proof, before.Majority); rec.Code != http.StatusOK {
		t.Fatalf("applying: status %d: %s", rec.Code, rec.Body.String())
	}

	after := decode[PruneView](t, get(t, srv, "/api/prune"))
	if after.Removed.Nodes != 0 {
		t.Errorf("the preview still offers %d nodes that were just removed", after.Removed.Nodes)
	}
	if after.Tombstoned == 0 {
		t.Error("something was tombstoned, and the preview reports none")
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
// else ever removes it, so the preview has to name it too — otherwise the corpus shrinks and the
// disk does not, and the deletion goes unlisted.
func TestThePreviewNamesWorkingCopiesTheClosureNoLongerReaches(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	checkoutDir(t, srv, "stranger_thing", "stranger_thing@v4", "octo-org_app")

	got := decode[PruneView](t, get(t, srv, "/api/prune"))
	if len(got.Checkouts) != 2 {
		t.Errorf("checkouts = %v, want the two trees of the repository outside the closure", got.Checkouts)
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

	if rec := applyReviewed(t, srv); rec.Code != http.StatusOK {
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
func TestWithNoWorkDirectoryThePreviewMakesNoClaimAboutTheDisk(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")

	got := decode[PruneView](t, get(t, srv, "/api/prune"))
	if len(got.Checkouts) != 0 {
		t.Errorf("checkouts = %v with no work directory configured", got.Checkouts)
	}
}

// markingFails is a store whose removal fails part way, and readsFailAfterMarking one that removes
// and then cannot read what is left.
type markingFails struct{ *store.Store }

func (markingFails) MarkOutsideClosure([]string) (store.PruneReport, error) {
	return store.PruneReport{}, errors.New("disk I/O error")
}

type readsFailAfterMarking struct {
	*store.Store
	marked bool
}

func (r *readsFailAfterMarking) MarkOutsideClosure(keep []string) (store.PruneReport, error) {
	r.marked = true
	return r.Store.MarkOutsideClosure(keep)
}

func (r *readsFailAfterMarking) LiveGraph() (store.LiveSize, error) {
	if r.marked {
		return store.LiveSize{}, errors.New("disk I/O error")
	}
	return r.Store.LiveGraph()
}

// A refusal is a 4xx and means nothing changed; a failure once removing has begun is the server's,
// and says what may have happened, so the reader is never told nothing was removed when some was.
func TestARemovalThatFailsPartWaySaysSomeMayBeGone(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	srv.Store = markingFails{s}

	rec := applyReviewed(t, srv)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "may already be out of view") {
		t.Fatalf("a removal that failed part way answered %d: %s", rec.Code, rec.Body.String())
	}
}

func TestARemovalThatSucceededSaysSoEvenIfWhatIsLeftCannotBeRead(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	declareOcto(t, s)
	outside(t, s, "stranger/thing")
	srv.Store = &readsFailAfterMarking{Store: s}

	rec := applyReviewed(t, srv)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "removed 1 node") {
		t.Fatalf("a removal that succeeded answered %d: %s", rec.Code, rec.Body.String())
	}
	if n := tombstoned(t, s); n != 1 {
		t.Fatalf("tombstoned %d, want the one node outside the scope", n)
	}
}

type compactFails struct{ *store.Store }

func (compactFails) Compact() (store.PruneReport, error) {
	return store.PruneReport{}, errors.New("compacting: disk I/O error")
}

// Deleting is one transaction, but marking the change for open pages follows it, so a failure can
// come after a deletion that happened. It is the server's failure, never a refusal that would say
// nothing changed.
func TestADeletionThatFailsIsTheServersFailureNotARefusal(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)
	srv.Store = compactFails{s}

	rec := postControl(t, srv, "/api/prune/compact")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "disk I/O error") {
		t.Fatalf("a deletion that failed answered %d: %s", rec.Code, rec.Body.String())
	}
}
