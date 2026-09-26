package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// heldRunOver records a run with findings off of the fixture's query over the databases the
// fixture run holds, which is the same set a run over them would cover.
func heldRunOver(t *testing.T, s *store.Store, fixtureRun int64) int64 {
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
		Mode: store.AnalysisModeRegular, Held: true,
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
	for _, table := range []string{"abom_finding", "abom_finding_sighting", "abom_finding_node"} {
		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatalf("counting %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}

// A held run evaluates each member the way a run does, keeps what each query reported there under
// an evaluation marked as held, and writes nothing any other surface reads.
func TestAHeldRunKeepsWhatEachQueryReportedUnderAHeldEvaluationAndStoresNoFinding(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	id := heldRunOver(t, s, fixture)
	before := tableCounts(t, s)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(analyzer.targets) != 2 {
		t.Errorf("the held run analyzed %d times, want once per member", len(analyzer.targets))
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
		if !listed.Held || len(listed.Findings) != 2 {
			t.Errorf("%s lists %+v, want the 2 results it held", repo, listed)
		}
		var held bool
		if err := s.DB().QueryRow(`
			SELECT q.held FROM abom_analysis_member m JOIN abom_query_run q ON q.row_id = m.query_run_id
			WHERE m.run_id = ? AND m.repository = ?`, id, repo).Scan(&held); err != nil || !held {
			t.Errorf("%s links held=%v (%v), want the evaluation that reported it, marked as held",
				repo, held, err)
		}
	}
	for table, n := range before {
		if after := tableCounts(t, s)[table]; after != n {
			t.Errorf("the held run moved %s from %d to %d rows", table, n, after)
		}
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatal(err)
	}
	if run.State != store.RunComplete {
		t.Errorf("the held run is %q, want %q", run.State, store.RunComplete)
	}
}

// A run's answer lists findings, not what a held run keeps, so a held run evaluates a member
// another run already answered rather than borrowing its counts.
func TestAHeldRunEvaluatesWhatARunAlreadyAnswered(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	if err := e.Run(context.Background(), fixture); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ran := len(analyzer.targets)
	id := heldRunOver(t, s, fixture)

	if err := e.Run(context.Background(), id); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(analyzer.targets) - ran; got != 2 {
		t.Errorf("the held run analyzed %d members, want both: a run's answer holds nothing", got)
	}
	for repo, m := range membersByRepository(t, s, id) {
		if m.Status != store.AnalysisSucceeded {
			t.Errorf("%s is %q (%q), want %q", repo, m.Status, m.Detail, store.AnalysisSucceeded)
		}
	}
}

// A held evaluation answers no skip, so a run after it records the findings adopting the query
// would record; converting what the held run kept then adds nothing the run did not already store.
func TestARunAfterAHeldRunStoresWhatItFound(t *testing.T) {
	s, e, fixture, analyzer := executorFixture(t)
	held := heldRunOver(t, s, fixture)
	if err := e.Run(context.Background(), held); err != nil {
		t.Fatalf("Run: %v", err)
	}
	kept := len(analyzer.targets)

	if err := e.Run(context.Background(), fixture); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(analyzer.targets) - kept; got != 2 {
		t.Errorf("the run analyzed %d members after the held run, want both", got)
	}
	if n := tableCounts(t, s)["abom_finding"]; n != 4 {
		t.Errorf("the run stored %d findings, want the 4 its members reported", n)
	}
	converted, err := s.ConvertHeld(held, "", nil, "operator")
	if err != nil {
		t.Fatalf("ConvertHeld: %v", err)
	}
	if converted.Converted != 4 {
		t.Errorf("converting the held run reads %+v, want its 4 results recorded as converted", converted)
	}
	if n := tableCounts(t, s)["abom_finding"]; n != 4 {
		t.Errorf("converting what the run already stored left %d findings, want the same 4", n)
	}
}
