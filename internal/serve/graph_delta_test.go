package serve

import (
	"fmt"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The delta is the same view arriving in pieces, so it has to answer the same question the full
// payload answered. It did not: the handler built its options from `steps` alone and dropped the
// scope, so a reader watching an owned-scope graph received arrivals from outside their closure
// and the picture silently widened as the crawl ran.
func TestTheDeltaAnswersTheSameQuestionTheFullGraphDid(t *testing.T) {
	srv, s := newServer(t)

	// Keep the scope stable: changing ownership now deliberately resets a commit cursor.
	for _, name := range []string{"octo-org/app", "stranger/thing"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	declareOcto(t, s)
	// Taken before graph facts arrive, so the delta must exercise filtering rather than be empty.
	since, err := s.RecordGraphGeneration(store.GraphRebuild{})
	if err != nil {
		t.Fatalf("recording a generation: %v", err)
	}

	seedGraph(t, s)
	outside(t, s, "stranger/thing")

	owned := decode[store.Graph](t, get(t, srv, "/api/graph/full?scope=owned"))
	inFull := map[string]bool{}
	for _, n := range owned.Nodes {
		inFull[n.Key] = true
	}
	if inFull["stranger/thing"] {
		t.Fatal("the full graph already leaks the out-of-scope node; this test cannot mean anything")
	}

	path := fmt.Sprintf("/api/graph/delta?since=%d&scope=owned", since)
	delta := decode[store.GraphDelta](t, get(t, srv, path))
	if delta.Reset {
		t.Fatal("the cursor was refused, so the leak was never exercised")
	}
	if len(delta.Nodes) == 0 {
		t.Fatal("the delta carried no nodes, so it cannot show whether the scope was applied")
	}

	for _, n := range delta.Nodes {
		if !inFull[n.Key] {
			t.Errorf("the delta carried %q, which the same scope excluded from the full graph", n.Key)
		}
	}
}

// A cursor says where the reader got to, not what they asked for, so it must not be mistaken for a
// filter and refused by the predicate parser.
func TestTheDeltaAcceptsItsCursorAlongsideThePredicate(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	if code := get(t, srv, "/api/graph/delta?since=0&scope=owned&steps=true").Code; code != 200 {
		t.Errorf("a cursor with a predicate answered %d, want 200", code)
	}
}

// The same refusal the full graph gives, for the same reason: a typo that widened the match would
// be invisible, and the delta is the half a reader watches without looking.
func TestTheDeltaRefusesAFilterItDoesNotKnow(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	if code := get(t, srv, "/api/graph/delta?since=0&scoop=owned").Code; code != 400 {
		t.Errorf("an unknown filter answered %d, want 400", code)
	}
}
