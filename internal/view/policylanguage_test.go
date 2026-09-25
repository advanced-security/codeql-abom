package view

import (
	"slices"
	"strings"
	"testing"
)

// An advisory is asked for by the severity GitHub published, as a closed list: every member is
// any live advisory, and a word GitHub never publishes is refused rather than matching nothing.
func TestAdvisorySeverityTakesPublishedSeveritiesAndRefusesAnythingElse(t *testing.T) {
	spec, err := Parse("advisorySeverity=high,critical")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := spec.String(); got != "advisorySeverity=critical%2Chigh" {
		t.Errorf("String() = %q, want the members sorted", got)
	}
	if !slices.Equal(Vocabulary("advisorySeverity"),
		[]string{BandCritical, BandHigh, BandMedium, BandLow, AdvisoryUnrated}) {
		t.Errorf("vocabulary = %v", Vocabulary("advisorySeverity"))
	}
	for _, bad := range []string{"moderate", "unknown", "error"} {
		if _, err := Parse("advisorySeverity=" + bad); err == nil ||
			!strings.Contains(err.Error(), "unrated") {
			t.Errorf("Parse(advisorySeverity=%s) = %v, want a refusal naming the severities", bad, err)
		}
	}
}

// Direct and transitive stay distinct in the language: one is a reference the repository can
// change, the other waits on somebody else.
func TestReachedTakesDirectAndTransitive(t *testing.T) {
	spec, err := Parse("reached=transitive,direct")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := spec.String(); got != "reached=direct%2Ctransitive" {
		t.Errorf("String() = %q", got)
	}
	if _, err := Parse("reached=2"); err == nil {
		t.Error("reached accepted a hop count, which folds direct and transitive into one number")
	}
}

// Both terms narrow the set a policy is evaluated over and the advisories unit, and nothing else
// claims them: the graph, the listings and a run refuse them by name.
func TestTheAdvisoryAndReachTermsAreAppliedWhereTheyAreAnswered(t *testing.T) {
	spec, err := Parse("advisorySeverity=critical&reached=direct")
	if err != nil {
		t.Fatal(err)
	}
	refusals := spec.Refusals()
	for _, surface := range []string{SurfaceSet, SurfaceAdvisories, SurfacePolicy} {
		if got := refusals[surface]; len(got) != 0 {
			t.Errorf("%s refuses %v", surface, got)
		}
	}
	for _, surface := range []string{SurfaceGraph, SurfaceInventory, SurfaceRun} {
		if got := refusals[surface]; !slices.Equal(got, []string{"advisorySeverity", "reached"}) {
			t.Errorf("%s refuses %v, want both terms named", surface, got)
		}
	}
}

// A predicate is about findings or about advisories. Read as one node carrying both it matches
// almost nothing, and read as either it enforces a question nobody wrote.
func TestAPredicateAskingAboutFindingsAndAdvisoriesIsRefusedByName(t *testing.T) {
	for _, text := range []string{
		"advisorySeverity=high&rules=actions/*",
		"advisorySeverity=high&securitySeverity=critical",
		"advisorySeverity=high&mode=regular",
	} {
		spec, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		err = spec.SetConflict()
		if err == nil || !strings.Contains(err.Error(), "advisorySeverity") ||
			!strings.Contains(err.Error(), "policies=") {
			t.Errorf("%s: SetConflict() = %v, want a refusal naming the term and the way out", text, err)
		}
	}
	for _, text := range []string{
		"advisorySeverity=high&reached=direct&properties=team:a&repositories=o/r",
		"rules=actions/*&reached=transitive",
	} {
		spec, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if err := spec.SetConflict(); err != nil {
			t.Errorf("%s: SetConflict() = %v; the shared terms narrow either population", text, err)
		}
	}
}

// A superset is "refuse what any of these refuses", so every narrowing belongs to its members. A
// term beside the list would read as narrowing the union and would be applied by nothing.
func TestASupersetNamesPoliciesAndNothingElse(t *testing.T) {
	spec, err := Parse("policies=pinned,advisories")
	if err != nil {
		t.Fatal(err)
	}
	if err := spec.SetConflict(); err != nil {
		t.Errorf("SetConflict() = %v for a bare superset", err)
	}
	if got := spec.UnenforceableAsPolicy(); len(got) != 0 {
		t.Errorf("a policy refuses %v of a superset", got)
	}
	if got := spec.UnenforceableAsSet(); !slices.Equal(got, []string{"policies"}) {
		t.Errorf("the set path refuses %v, want policies: only a policy can name another", got)
	}

	mixed, err := Parse("policies=pinned&properties=team:a")
	if err != nil {
		t.Fatal(err)
	}
	if err := mixed.SetConflict(); err == nil || !strings.Contains(err.Error(), "properties") {
		t.Errorf("SetConflict() = %v, want the term beside the list named", err)
	}
}
