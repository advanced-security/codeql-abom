package view

import (
	"strings"
	"testing"
)

// A `uses:` and a `needs:` are both relationships and mean different things, so a reader has to be
// able to ask for one of them. Ordering is not use — reach, blast radius and the bill of materials
// all exclude `job-needs-job` — and a language that cannot name the difference leaves the reader
// unable to see what the aggregates are excluding.
func TestAPredicateCanNameTheKindOfRelationship(t *testing.T) {
	spec, err := Parse("edgeKinds=job-needs-job,step-uses-action")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(spec.EdgeKinds) != 2 {
		t.Fatalf("EdgeKinds = %v, want both kinds", spec.EdgeKinds)
	}
	// The canonical text round-trips, or a saved view and a shared link ask different questions.
	again, err := Parse(spec.String())
	if err != nil {
		t.Fatalf("reparsing %q: %v", spec.String(), err)
	}
	if !again.Equal(spec) {
		t.Errorf("%q does not round-trip: %q", spec.String(), again.String())
	}
}

// The kinds and the classes are two questions about one relationship: `edges` is what a line
// crosses as the browser draws it, and `edgeKinds` is what the line is. One term answering both
// would make "every cross-owner `needs:`" unaskable.
func TestTheKindOfARelationshipIsNotItsClass(t *testing.T) {
	spec, err := Parse("edgeKinds=job-needs-job&edges=crossOwner")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(spec.EdgeKinds) != 1 || len(spec.EdgeClass) != 1 {
		t.Fatalf("EdgeKinds = %v and EdgeClass = %v, want one of each",
			spec.EdgeKinds, spec.EdgeClass)
	}
}

// A term nothing applies is text that looks like a question and is answerable nowhere. The graph
// draws relationships, so it is the surface that has to answer this one.
func TestTheGraphIsDeclaredAsApplyingTheKindOfRelationship(t *testing.T) {
	spec, err := Parse("edgeKinds=job-needs-job")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if refused := spec.UnenforceableAsGraph(); len(refused) > 0 {
		t.Errorf("the graph refuses %v, and it is the surface that draws relationships", refused)
	}
	// And a list of nodes says so rather than dropping it, because a list has no relationships.
	if refused := spec.UnenforceableAsInventory(); len(refused) != 1 {
		t.Errorf("the node listing refuses %v, want it to name edgeKinds", refused)
	}
	if !AppliedByExplorer(paramEdgeKinds) {
		t.Error("a refusal would not name the Explorer, which is where the term is answered")
	}
}

// The four kinds are the vocabulary, and naming them is what lets a reader see the choice at all.
func TestEveryKindOfRelationshipIsNamed(t *testing.T) {
	want := []string{
		EdgeKindStepUsesAction, EdgeKindJobNeedsJob,
		EdgeKindJobUsesReusableWorkflow, EdgeKindCompositeStepUsesAction,
	}
	got := strings.Join(EdgeKinds(), ",")
	for _, kind := range want {
		if !strings.Contains(got, kind) {
			t.Errorf("EdgeKinds() is %q, which does not offer %q", got, kind)
		}
	}
	if len(EdgeKinds()) != len(want) {
		t.Errorf("EdgeKinds() is %q, want exactly the four kinds", got)
	}
}
