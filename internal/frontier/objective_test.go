package frontier

import (
	"testing"
)

// The crawl had no objective: `score DESC` first meant it did whatever looked cheapest, so it
// could start depth 3 while depth 1 was still incomplete. Completeness at a depth is a claim the
// queue can prove; "worked on the cheapest thing available" never is.
func TestTheShallowestOutstandingWorkIsClaimedFirst(t *testing.T) {
	q, _ := newQueue(t)

	deep := item("deep/valuable", 3)
	deep.Ref = "v1"
	shallow := item("shallow/humble", 1)
	shallow.Ref = "v1"
	if _, err := q.Enqueue([]Item{deep, shallow}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// The deep one looks far more valuable, which under the old ordering was enough to jump it.
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET score = 1000 WHERE node_key = 'deep/valuable'`); err != nil {
		t.Fatalf("scoring: %v", err)
	}

	claimed, err := q.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d items, want 1", len(claimed))
	}
	if claimed[0].NodeKey != "shallow/humble" {
		t.Errorf("claimed %s at depth %d; depth 1 is still incomplete",
			claimed[0].NodeKey, claimed[0].Depth)
	}
}

// Within a depth, what the organization most depends on still comes first. Depth orders the
// stages; score orders the work inside one.
func TestScoreStillDecidesWithinOneDepth(t *testing.T) {
	q, _ := newQueue(t)

	for _, key := range []string{"a/humble", "b/valuable"} {
		it := item(key, 1)
		it.Ref = "v1"
		if _, err := q.Enqueue([]Item{it}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET score = 1000 WHERE node_key = 'b/valuable'`); err != nil {
		t.Fatalf("scoring: %v", err)
	}

	claimed, err := q.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed[0].NodeKey != "b/valuable" {
		t.Errorf("claimed %s; within a depth the most depended-on comes first",
			claimed[0].NodeKey)
	}
}

// An operator asking for one repository is stating an objective the crawl cannot infer, so it
// outranks every stage of the ordering including depth.
func TestAnExpeditedItemOutranksTheObjective(t *testing.T) {
	q, _ := newQueue(t)

	for _, spec := range []struct {
		key   string
		depth int
	}{{"shallow/thing", 1}, {"deep/urgent", 4}} {
		it := item(spec.key, spec.depth)
		it.Ref = "v1"
		if _, err := q.Enqueue([]Item{it}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if _, err := q.Store.DB().Exec(
		`UPDATE abom_frontier SET expedited_at = '2026-09-07T00:00:00Z'
		  WHERE node_key = 'deep/urgent'`); err != nil {
		t.Fatalf("expediting: %v", err)
	}

	claimed, err := q.Claim(1)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed[0].NodeKey != "deep/urgent" {
		t.Errorf("claimed %s; an operator asked for the other one", claimed[0].NodeKey)
	}
}

// "Complete at depth 1" has to be answerable, or the objective is a claim nobody can check.
func TestCompletenessIsReportedPerDepth(t *testing.T) {
	q, _ := newQueue(t)

	for _, spec := range []struct {
		key   string
		depth int
	}{{"a/one", 1}, {"b/one", 1}, {"c/two", 2}} {
		it := item(spec.key, spec.depth)
		it.Ref = "v1"
		if _, err := q.Enqueue([]Item{it}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if err := q.Complete(Key{NodeKey: "a/one", Ref: "v1"},
		Resolution{Ref: "v1", SHA: "sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d depths, want 2", len(got))
	}
	if got[0].Depth != 1 || got[0].Total != 2 || got[0].Done != 1 {
		t.Errorf("depth 1 = %+v, want 1 of 2 done", got[0])
	}
	if got[1].Depth != 2 || got[1].Total != 1 || got[1].Done != 0 {
		t.Errorf("depth 2 = %+v, want 0 of 1 done", got[1])
	}
	// Shallowest incomplete is what the crawl is working on, and what the page should say.
	if d, ok := ShallowestIncomplete(got); !ok || d != 1 {
		t.Errorf("shallowest incomplete = %d (%t), want depth 1", d, ok)
	}
}

// A closure that is complete everywhere has no shallowest incomplete depth. Reporting 0 would
// read as "depth 0 is outstanding", which is a different and wrong statement.
func TestACompleteClosureHasNoOutstandingDepth(t *testing.T) {
	q, _ := newQueue(t)

	it := item("a/one", 1)
	it.Ref = "v1"
	if _, err := q.Enqueue([]Item{it}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(Key{NodeKey: "a/one", Ref: "v1"},
		Resolution{Ref: "v1", SHA: "sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if _, ok := ShallowestIncomplete(got); ok {
		t.Error("a fully analyzed closure reported outstanding work")
	}
}

// Rows nothing can act on are not incompleteness. A dependency GitHub has no database for stays
// `unavailable` forever, and counting it as outstanding would make every depth permanently
// incomplete and the objective permanently unsatisfiable.
func TestUnavailableWorkDoesNotMakeADepthPermanentlyIncomplete(t *testing.T) {
	q, _ := newQueue(t)

	for _, spec := range []struct{ key, status string }{
		{"a/fine", "done"}, {"b/gone", "unavailable"},
	} {
		it := item(spec.key, 1)
		it.Ref = "v1"
		if _, err := q.Enqueue([]Item{it}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		if _, err := q.Store.DB().Exec(
			`UPDATE abom_frontier SET status = ? WHERE node_key = ?`,
			spec.status, spec.key); err != nil {
			t.Fatalf("setting status: %v", err)
		}
	}

	got, err := q.Completeness()
	if err != nil {
		t.Fatalf("Completeness: %v", err)
	}
	if _, ok := ShallowestIncomplete(got); ok {
		t.Error("a depth holding only settled and unavailable work reported as incomplete")
	}
}
