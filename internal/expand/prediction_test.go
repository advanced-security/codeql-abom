package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// predictionOver records a prediction of the fixture's query over the databases the fixture run
// holds, which is the same set a run over them would cover.
func predictionOver(t *testing.T, s *store.Store, fixtureRun int64) int64 {
	t.Helper()
	held := []store.ScopedDatabase{}
	for _, repo := range []string{"octo-app", "octo-tool"} {
		m := membersByRepository(t, s, fixtureRun)[repo]
		held = append(held, store.ScopedDatabase{
			DatabaseIdentity: m.DatabaseIdentity, Status: store.DatabaseRetained, Path: m.DatabasePath,
		})
	}
	id, err := s.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: "operator", Predicate: "repositories=octo-app%2Cocto-tool",
		Mode: store.AnalysisModeRegular, Prediction: true,
		Queries:   []store.AnalysisQuery{{QueryID: "actions/unpinned-tag"}},
		Databases: held,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	return id
}

func tableCounts(t *testing.T, s *store.Store) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{"abom_finding", "abom_finding_sighting", "abom_query_run",
		"abom_query_run_rule", "abom_finding_node"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

// A prediction evaluates each member the way a run does and keeps what each query would report
// there, and nothing it found reaches a table any other surface reads: a query nobody adopted has
// found nothing yet.
func TestAPredictionCountsWhatEachQueryWouldReportAndStoresNoFinding(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	id := predictionOver(t, s, fixture)
	before := tableCounts(t, s)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(analyzer.targets) != 2 {
		t.Errorf("the prediction analyzed %d times, want once per member", len(analyzer.targets))
	}
	for repo, m := range membersByRepository(t, s, id) {
		if m.Status != store.AnalysisSucceeded || !m.Counted || m.Results != 2 ||
			m.ByQuery["actions/unpinned-tag"] != 1 {
			t.Errorf("%s reads %q counted=%v %d %v, want the fixture's results counted per rule",
				repo, m.Status, m.Counted, m.Results, m.ByQuery)
		}
		listed, err := s.RunFindings(id, repo, "", 100)
		if err != nil {
			t.Fatalf("RunFindings(%s): %v", repo, err)
		}
		if !listed.Predicted || len(listed.Findings) != 2 {
			t.Errorf("%s lists %+v, want the 2 results it would report", repo, listed)
		}
	}
	if after := tableCounts(t, s); len(after) != len(before) {
		t.Fatal("the tables counted changed shape")
	} else {
		for table, n := range before {
			if after[table] != n {
				t.Errorf("the prediction moved %s from %d to %d rows", table, n, after[table])
			}
		}
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.RunComplete {
		t.Errorf("the prediction is %q, want %q", run.State, store.RunComplete)
	}
}

// A run's answer counts what it found and lists findings, not what the prediction would hold, so
// a prediction evaluates a member another run already answered rather than borrowing its counts.
func TestAPredictionEvaluatesWhatARunAlreadyAnswered(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	if err := e.Run(context.Background(), fixture); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ran := len(analyzer.targets)
	id := predictionOver(t, s, fixture)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(analyzer.targets) - ran; got != 2 {
		t.Errorf("the prediction analyzed %d members, want both: a run's answer holds no prediction", got)
	}
	for repo, m := range membersByRepository(t, s, id) {
		if m.Status != store.AnalysisSucceeded {
			t.Errorf("%s is %q (%q), want %q", repo, m.Status, m.Detail, store.AnalysisSucceeded)
		}
	}
}

// A prediction leaves no answer behind for a run to skip on, so a run after it records the findings
// adopting the query would record.
func TestARunAfterAPredictionStoresWhatItFound(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	prediction := predictionOver(t, s, fixture)
	if err := e.Run(context.Background(), prediction); err != nil {
		t.Fatalf("Run: %v", err)
	}
	predicted := len(analyzer.targets)

	if err := e.Run(context.Background(), fixture); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(analyzer.targets) - predicted; got != 2 {
		t.Errorf("the run analyzed %d members after the prediction, want both", got)
	}
	if n := tableCounts(t, s)["abom_finding"]; n != 4 {
		t.Errorf("the run stored %d findings, want the 4 its members reported", n)
	}
}
