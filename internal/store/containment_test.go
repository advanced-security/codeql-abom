package store

import (
	"fmt"
	"testing"
)

// deriveContainment finishes a fixture the way an ingestion finishes. The canvas reads what holds
// what rather than recomputing it, so a corpus nothing derived draws no containment at all.
func deriveContainment(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DeriveContainment(); err != nil {
		t.Fatalf("DeriveContainment: %v", err)
	}
}

// storedContainment reads the table directly, so a test compares the canvas against what is on
// disk rather than against the same code path that drew it.
func storedContainment(t *testing.T, s *Store) map[[3]string]bool {
	t.Helper()
	rows, err := s.DB().Query(`SELECT owner_key, held_key, edge_kind FROM abom_containment`)
	if err != nil {
		t.Fatalf("reading abom_containment: %v", err)
	}
	defer rows.Close()

	out := map[[3]string]bool{}
	for rows.Next() {
		var owner, held, kind string
		if err := rows.Scan(&owner, &held, &kind); err != nil {
			t.Fatalf("scanning containment: %v", err)
		}
		out[[3]string{owner, held, kind}] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading containment: %v", err)
	}
	return out
}

// drawnContainment is the containment a graph actually holds, keyed the same way.
func drawnContainment(g Graph) map[[3]string]bool {
	out := map[[3]string]bool{}
	for _, e := range g.Edges {
		if e.Kind == EdgeKindRepoContains || e.Kind == EdgeKindOrgContains {
			out[[3]string{e.Source, e.Target, e.Kind}] = true
		}
	}
	return out
}

func describe(set map[[3]string]bool) string {
	out := ""
	for k := range set {
		out += fmt.Sprintf("\n  %s -[%s]-> %s", k[0], k[2], k[1])
	}
	return out
}

// The whole reason containment is stored: a list over the relationships and the canvas disagreed
// about how many exist, because one read a table and the other drew from a rule. There is one
// answer now, so the table names exactly what the canvas draws.
func TestStoredContainmentIsExactlyWhatTheCanvasDraws(t *testing.T) {
	s := edgeKindFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}

	stored, drawn := storedContainment(t, s), drawnContainment(g)
	if len(drawn) == 0 {
		t.Fatal("the canvas drew no containment, so this compares nothing")
	}
	for k := range drawn {
		if !stored[k] {
			t.Errorf("drawn and not stored: %s -[%s]-> %s", k[0], k[2], k[1])
		}
	}
	for k := range stored {
		if !drawn[k] {
			t.Errorf("stored and not drawn: %s -[%s]-> %s", k[0], k[2], k[1])
		}
	}
	if t.Failed() {
		t.Logf("stored:%s\ndrawn:%s", describe(stored), describe(drawn))
	}
}

// Filtering narrows what is drawn; it may never invent a holding.
func TestAFilteredCanvasDrawsOnlyContainmentThatIsStored(t *testing.T) {
	s := edgeKindFixture(t)
	stored := storedContainment(t, s)

	for _, opts := range []FullGraphOptions{
		{EdgeKinds: []string{EdgeKindJobNeedsJob}},
		{IncludeSteps: true},
		{Origins: []string{OriginSeed}},
	} {
		g, err := s.FullGraph(opts)
		if err != nil {
			t.Fatalf("FullGraph(%+v): %v", opts, err)
		}
		for k := range drawnContainment(g) {
			if !stored[k] {
				t.Errorf("%+v drew a holding nothing stores: %s -[%s]-> %s",
					opts, k[0], k[2], k[1])
			}
		}
	}
}

// A derivation that keeps finding work has not settled, and one that runs every cycle would
// rewrite the same rows forever.
func TestDerivingContainmentTwiceChangesNothingTheSecondTime(t *testing.T) {
	s := edgeKindFixture(t)

	held, err := s.Containment()
	if err != nil {
		t.Fatalf("Containment: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("the fixture stored no holdings, so settling proves nothing")
	}
	again, err := s.DeriveContainment()
	if err != nil {
		t.Fatalf("DeriveContainment: %v", err)
	}
	if again != 0 {
		t.Errorf("a second derivation changed %d holdings, so it does not settle", again)
	}
}

// A holding that changes without a node arriving is the case a delta cursor cannot express by
// arrivals alone: declaring an account whose repository is already on screen adds the holding and
// nothing else. Resolved against the arrivals, the edge was never sent and the open page
// disagreed with a reload for as long as it stayed open.
func TestAHoldingAddedBetweenNodesThatPredateTheCursorReachesTheDelta(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	deriveContainment(t, s)

	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	held, err := s.Containment()
	if err != nil {
		t.Fatalf("Containment: %v", err)
	}
	// Both keys predate the generation, which is the whole point: nothing arrives with it.
	added := Containment{
		OwnerKey: OrgKey("octo-org"),
		HeldKey:  "actions/checkout",
		Kind:     EdgeKindOrgContains,
	}
	if _, err := s.storeContainment(append(held, added)); err != nil {
		t.Fatalf("storeContainment: %v", err)
	}

	delta, err := s.GraphDelta(generation, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Fatal("asked for a reload after an addition, which a cursor can express")
	}
	for _, e := range delta.Edges {
		if e.Source == added.OwnerKey && e.Target == added.HeldKey && e.Kind == added.Kind {
			return
		}
	}
	t.Errorf("delta carried no edge for a holding added between nodes it already holds: %+v",
		delta.Edges)
}

// Removal is the case it cannot express at all: GraphDelta has no containment-removal field, so a
// holding that disappears has to invalidate the cursor or an open page keeps drawing it forever.
func TestRemovingAHoldingForcesOpenPagesToReload(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	deriveContainment(t, s)

	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}
	held, err := s.Containment()
	if err != nil {
		t.Fatalf("Containment: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("the fixture stored no holdings, so removing one proves nothing")
	}
	if _, err := s.storeContainment(held[1:]); err != nil {
		t.Fatalf("storeContainment: %v", err)
	}

	delta, err := s.GraphDelta(generation, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if !delta.Reset {
		t.Error("a removed holding left the cursor honored, so the page keeps drawing it")
	}
}

// And a cycle that removes nothing must not reload every open page for nothing.
func TestDerivingContainmentAgainDoesNotForceAReload(t *testing.T) {
	s := newTestStore(t)
	seedGraph(t, s, NewWriter(s, 0))
	deriveContainment(t, s)

	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}
	deriveContainment(t, s)

	delta, err := s.GraphDelta(generation, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Error("a settled derivation asked every open page to reload")
	}
}

// A drawn containment line carries every list a relationship line does, empty, never null: the
// canvas reads them as sets, and the crawled corpus drew 4,463 of these with `locked: null`.
func TestAContainmentLineCarriesEmptyListsRatherThanNull(t *testing.T) {
	s := edgeKindFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	drawn := 0
	for _, e := range g.Edges {
		if e.Kind != "org-contains" && e.Kind != "repo-contains" {
			continue
		}
		drawn++
		if e.Refs == nil || e.Locked == nil {
			t.Errorf("%s -[%s]-> %s: refs %v, locked %v; want both empty, not nil",
				e.Source, e.Kind, e.Target, e.Refs, e.Locked)
		}
	}
	if drawn == 0 {
		t.Fatal("no containment was drawn, so this checks nothing")
	}
}
