package store

import "testing"

// These pin the two pure passes `fillComponents` makes over its edge list. Both were previously
// reachable only through a full document build, so a change to either was verifiable only by
// diffing thousands of components.

func TestAReferenceNothingNamesIsNotInvented(t *testing.T) {
	depth := map[string]int{"root": 0, "actions/checkout": 1}
	edges := []bomEdge{
		{source: "root", target: "actions/checkout", refRaw: "v4", refValue: "v4"},
	}

	refs := referencesByComponent(depth, edges, nil)

	if got := len(refs["actions/checkout"]); got != 1 {
		t.Fatalf("references = %d, want 1", got)
	}
	// A component no edge points at has no entry at all, rather than an empty one that would
	// read as "we looked and found no version".
	if _, ok := refs["root"]; ok {
		t.Error("root got a reference map and nothing names it")
	}
}

// The same version named by two consumers is one version of one component.
func TestAReferenceNamedTwiceIsCountedOnce(t *testing.T) {
	depth := map[string]int{"a": 0, "b": 0, "actions/checkout": 1}
	edges := []bomEdge{
		{source: "a", target: "actions/checkout", refRaw: "v4", refValue: "v4"},
		{source: "b", target: "actions/checkout", refRaw: "v4", refValue: "v4"},
	}

	if got := len(referencesByComponent(depth, edges, nil)["actions/checkout"]); got != 1 {
		t.Errorf("references = %d, want 1", got)
	}
}

// Two distinct versions of one component stay distinct, which is the whole reason the map is
// keyed by the reference rather than counted.
func TestTwoVersionsOfOneComponentBothSurvive(t *testing.T) {
	depth := map[string]int{"a": 0, "actions/checkout": 1}
	edges := []bomEdge{
		{source: "a", target: "actions/checkout", refRaw: "v4", refValue: "v4"},
		{source: "a", target: "actions/checkout", refRaw: "v3", refValue: "v3"},
	}

	if got := len(referencesByComponent(depth, edges, nil)["actions/checkout"]); got != 2 {
		t.Errorf("references = %d, want 2", got)
	}
}

// An edge with either end outside the walked closure describes something this document does not
// claim to cover, so it contributes nothing rather than dragging its endpoint in.
func TestAnEdgeLeavingTheClosureContributesNothing(t *testing.T) {
	depth := map[string]int{"root": 0, "inside": 1}
	edges := []bomEdge{
		{source: "root", target: "outside", refRaw: "v1", refValue: "v1"},
		{source: "outside", target: "inside", refRaw: "v2", refValue: "v2"},
	}

	refs := referencesByComponent(depth, edges, nil)

	if len(refs) != 0 {
		t.Errorf("references = %v, want none", refs)
	}
	if got := len(relationshipsFrom(depth, edges)); got != 0 {
		t.Errorf("relationships = %d, want 0", got)
	}
}

func TestARelationshipSeenTwiceIsReportedOnce(t *testing.T) {
	depth := map[string]int{"a": 0, "b": 1}
	edges := []bomEdge{
		{source: "a", target: "b", kind: "uses", boundary: "external"},
		{source: "a", target: "b", kind: "uses", boundary: "external"},
	}

	if got := len(relationshipsFrom(depth, edges)); got != 1 {
		t.Errorf("relationships = %d, want 1", got)
	}
}

// The kind is part of a relationship's identity: one component can both use another and depend on
// it for ordering, and collapsing those would lose a distinction the graph draws.
func TestTwoKindsBetweenTheSamePairAreTwoRelationships(t *testing.T) {
	depth := map[string]int{"a": 0, "b": 1}
	edges := []bomEdge{
		{source: "a", target: "b", kind: "uses"},
		{source: "a", target: "b", kind: "needs"},
	}

	if got := len(relationshipsFrom(depth, edges)); got != 2 {
		t.Errorf("relationships = %d, want 2", got)
	}
}

// A document is compared against its predecessor, so the order has to come from the content
// rather than from map iteration.
func TestRelationshipsComeOutInAStableOrder(t *testing.T) {
	depth := map[string]int{"a": 0, "b": 1, "c": 1}
	edges := []bomEdge{
		{source: "b", target: "c", kind: "uses"},
		{source: "a", target: "c", kind: "uses"},
		{source: "a", target: "b", kind: "needs"},
		{source: "a", target: "b", kind: "uses"},
	}

	got := relationshipsFrom(depth, edges)
	want := [][3]string{
		{"a", "b", "needs"}, {"a", "b", "uses"}, {"a", "c", "uses"}, {"b", "c", "uses"},
	}
	if len(got) != len(want) {
		t.Fatalf("relationships = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Source != w[0] || got[i].Target != w[1] || got[i].Kind != w[2] {
			t.Errorf("relationship %d = %v/%v/%v, want %v", i,
				got[i].Source, got[i].Target, got[i].Kind, w)
		}
	}
}

// Coverage is what turns a reference into one somebody actually analyzed; without a match the
// reference is still reported, and reported as unanalyzed rather than omitted.
func TestAReferenceCarriesItsCoverageWhenThereIsSome(t *testing.T) {
	depth := map[string]int{"a": 0, "actions/checkout": 1}
	edges := []bomEdge{
		{source: "a", target: "actions/checkout", refRaw: "v4", refValue: "v4"},
	}
	coverage := map[string]refCoverage{
		refCoverageKey("actions/checkout", "v4"): {resolvedSHA: "abc123", analyzed: true},
	}

	for _, r := range referencesByComponent(depth, edges, coverage) {
		for _, ref := range r {
			if ref.ResolvedSHA != "abc123" || !ref.Analyzed {
				t.Errorf("reference = %+v, want the recorded coverage", ref)
			}
		}
	}

	for _, r := range referencesByComponent(depth, edges, nil) {
		for _, ref := range r {
			if ref.Analyzed {
				t.Error("a reference with no coverage was reported as analyzed")
			}
		}
	}
}
