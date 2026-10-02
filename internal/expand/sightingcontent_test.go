package expand

import (
	"testing"
)

// edgesRuns counts a repository's edges runs, and how many carry sighting content; every
// repository when none is named.
func (h *harness) edgesRuns(repository string) (total, marked int) {
	h.t.Helper()
	if err := h.store.DB().QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(sighted_content), 0) FROM abom_query_run
		 WHERE query_id = 'abom/edges' AND (? = '' OR LOWER(repository) = LOWER(?))`,
		repository, repository).Scan(&total, &marked); err != nil {
		h.t.Fatal(err)
	}
	return total, marked
}

// As every edges run recorded before sightings kept what each commit declared.
func (h *harness) forgetSightingContent() {
	h.t.Helper()
	if _, err := h.store.DB().Exec(`UPDATE abom_query_run SET sighted_content = 0`); err != nil {
		h.t.Fatal(err)
	}
}

// A round's edges run answers for its commit once its edges are written with what that commit
// declared, and not before: one interrupted between recording the run and flushing its edges
// would otherwise be skipped with nothing behind it.
func TestARoundMarksItsEdgesRunOnceItsSightingsAreWritten(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()

	total, marked := h.edgesRuns("")
	if total == 0 {
		t.Fatal("the round recorded no edges run, so there is nothing to have marked")
	}
	if marked != total {
		t.Errorf("%d of %d edges runs carry sighting content", marked, total)
	}
}

// An edges run recorded before sightings kept content shares its rows with every other commit
// declaring the same locations, and those rows hold the last writer's targets. Trusting it would
// leave its commit reading another commit's dependencies, so meeting it again analyzes it — once.
func TestAnEdgesRunRecordedBeforeSightingsKeptContentIsAnalyzedAgain(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	h.forgetSightingContent()

	if _, err := h.store.RequeueRepository("gradle/actions"); err != nil {
		t.Fatal(err)
	}
	built := len(h.built)
	h.run(100)
	if len(h.built) == built {
		t.Fatal("a commit whose edges run predates sighting content was settled without analysis")
	}
	if total, marked := h.edgesRuns("gradle/actions"); total == 0 || marked != total {
		t.Errorf("after re-analysis %d of gradle/actions' %d edges runs carry sighting content", marked, total)
	}

	if _, err := h.store.RequeueRepository("gradle/actions"); err != nil {
		t.Fatal(err)
	}
	built = len(h.built)
	h.run(100)
	if again := len(h.built) - built; again != 0 {
		t.Errorf("a commit already re-observed was analyzed %d more times", again)
	}
}

// Content equivalence stands a moved reference on a commit already analyzed. Standing it on one
// whose sightings carry no content would carry the same gap to the new commit.
func TestAMovedRefIsNotSettledAgainstAnEdgesRunWithoutSightingContent(t *testing.T) {
	h := newHarness(t)
	h.enqueue(item("gradle", "actions", "setup-gradle", 1))
	h.drain()
	h.forgetSightingContent()
	if n := h.requeueDirect(); n == 0 {
		t.Fatal("nothing was requeued")
	}

	h.probe.moveEverythingTo("moved-sha")
	built := len(h.built)
	h.run(100)
	if len(h.built) == built {
		t.Error("a moved ref was settled against an edges run whose sightings carry no content")
	}
}
