package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// recordCondition attaches an `if:` to a step the same way AbomStepConditions.ql's output is
// ingested, so the join under test is the one the crawl actually produces.
func recordCondition(t *testing.T, s *store.Store, stepKey, expression string) {
	t.Helper()
	run, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
		QueryID: "abom/step-conditions", Repository: "octo-org/lib",
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}
	if _, err := s.AddObservations(run, []store.Observation{{
		SubjectKind: store.SubjectNode, SubjectKey: stepKey,
		Name: store.ObservationCondition, Value: expression,
	}}); err != nil {
		t.Fatalf("AddObservations: %v", err)
	}
}

func conditionsOf(t *testing.T, s *store.Store, node, repository string) []StepCondition {
	t.Helper()
	return reachOf(t, s, node)[repository].Conditions
}

// "Does anything you run reach this step" is only half the question; the other half is what
// stands between the entry point and it. A step reached unconditionally is a different claim
// from one behind `if: runner.os == 'Linux'`, and a route that reports neither leaves a reader
// unable to tell them apart.
func TestReachReportsTheConditionsGuardingTheRoute(t *testing.T) {
	s := fixture(t)
	recordCondition(t, s, "lib-step", "runner.os == 'Linux'")

	got := conditionsOf(t, s, "actions/cache", "octo-org/lib")

	if len(got) != 1 {
		t.Fatalf("got %+v, want the one condition on the step that names actions/cache", got)
	}
	if got[0].Condition != "runner.os == 'Linux'" {
		t.Errorf("Condition = %q, want it verbatim", got[0].Condition)
	}
	if got[0].In != "octo-org/lib/greet" {
		t.Errorf("In = %q, want the composite action holding the guarded step", got[0].In)
	}
}

// A guard on an intermediary is exactly what a consumer cannot see for itself: octo-org/app
// reaches actions/cache only through octo-org/lib/greet, so the condition on greet's step is
// part of what stands in its way.
func TestAConditionOnAnIntermediaryIsReportedToWhoeverReachesThroughIt(t *testing.T) {
	s := fixture(t)
	recordCondition(t, s, "lib-step", "runner.os == 'Linux'")

	got := conditionsOf(t, s, "actions/cache", "octo-org/app")

	if len(got) != 1 || got[0].Condition != "runner.os == 'Linux'" {
		t.Errorf("got %+v, want the intermediary's condition carried to the repository that "+
			"reaches through it", got)
	}
}

// Reporting a condition nobody wrote would be worse than reporting none: it would suggest a step
// runs sometimes when it runs always.
func TestAnUnconditionalRouteReportsNoConditions(t *testing.T) {
	s := fixture(t)

	if got := conditionsOf(t, s, "actions/cache", "octo-org/lib"); len(got) != 0 {
		t.Errorf("got %+v, want nothing: no step on the route declares an `if:`", got)
	}
}

// The line that must not be crossed. Whether `github.ref == 'refs/heads/main'` holds on a given
// run is not something static analysis settles, so the expression is carried through untouched
// and the reader judges.
func TestAConditionIsCarriedThroughWithoutBeingInterpreted(t *testing.T) {
	s := fixture(t)
	const verbatim = "${{ github.ref == 'refs/heads/main' && github.event_name == 'push' }}"
	recordCondition(t, s, "lib-step", verbatim)

	got := conditionsOf(t, s, "actions/cache", "octo-org/lib")

	if len(got) != 1 || got[0].Condition != verbatim {
		t.Errorf("got %+v, want %q exactly as the workflow wrote it", got, verbatim)
	}
}
