package store

import (
	"errors"
	"strings"
	"testing"
)

// comparedHeldRun is a finished held run whose octo/app member kept two results; its other two
// members were never analyzed.
func comparedHeldRun(t *testing.T, s *Store) int64 {
	t.Helper()
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if err := s.SetAnalysisRunCodeQL(id, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: heldEvaluation(t, s),
		ByQuery: map[string]int{"actions/unpinned-tag": 1, "actions/other": 1},
	}, twoHeld()); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}
	if err := s.SetAnalysisRunState(id, RunComplete); err != nil {
		t.Fatal(err)
	}
	return id
}

func comparisonMemberNamed(t *testing.T, c Comparison, repository string) ComparisonMember {
	t.Helper()
	for _, m := range c.Members {
		if m.Repository == repository {
			return m
		}
	}
	t.Fatalf("comparison %d has no member %s", c.RowID, repository)
	return ComparisonMember{}
}

// Default Setup builds one repository's own checkout and never stages a dependency, so a
// comparison drawn from an extended run would name alerts that will never appear. The refusal
// belongs to the comparison, not to the run, and it names the shape.
func TestAComparisonOfARunOverExtendedDatabasesIsRefusedNamingTheShape(t *testing.T) {
	s := newTestStore(t)
	req := heldRequest(s)
	req.Mode = AnalysisModeExtended
	id, err := s.CreateAnalysisRun(req)
	if err != nil {
		t.Fatalf("an extended held run was refused: %v", err)
	}
	if err := s.SetAnalysisRunState(id, RunComplete); err != nil {
		t.Fatal(err)
	}

	_, err = s.CreateComparison(id, "operator")
	if !errors.Is(err, ErrComparisonRefused) {
		t.Fatalf("comparing an extended run returned %v, want a refusal", err)
	}
	if !strings.Contains(err.Error(), AnalysisModeExtended) {
		t.Errorf("the refusal %q does not name the run's shape", err)
	}
	if n := countRows(t, s, "abom_comparison"); n != 0 {
		t.Errorf("a refused comparison left %d rows", n)
	}
}

// A run still moving has results that are about to change, and a comparison of them would be
// of a set nobody will see again.
func TestARunStillUnderWayIsNotCompared(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	if _, err := s.CreateComparison(id, "operator"); !errors.Is(err, ErrComparisonRefused) {
		t.Errorf("comparing a pending run returned %v, want a refusal", err)
	}
	if _, err := s.CreateComparison(comparedHeldRun(t, s), ""); err == nil {
		t.Error("a comparison nobody asked for was recorded")
	}
}

// Every member of the run is a member of the comparison. One nothing analyzed has no results to
// set against anything and is named with why, never dropped: a delta over an unnamed subset is
// quoted as if it covered the scope.
func TestAComparisonStartsWithEveryMemberAndNamesTheOnesNothingAnalyzed(t *testing.T) {
	s := newTestStore(t)
	runID := comparedHeldRun(t, s)

	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatalf("CreateComparison: %v", err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatalf("Comparison: %v", err)
	}
	if c.State != ComparisonRunning || c.RunID != runID || c.RequestedBy != "operator" {
		t.Errorf("the comparison reads %q of run %d by %q", c.State, c.RunID, c.RequestedBy)
	}
	if c.CLIVersion != "2.27.1" {
		t.Errorf("the comparison names CodeQL %q, want the run's 2.27.1", c.CLIVersion)
	}
	if len(c.Members) != 3 {
		t.Fatalf("the comparison has %d members, want the run's 3", len(c.Members))
	}
	app := comparisonMemberNamed(t, c, "octo/app")
	if app.Status != ComparisonPending || app.AnalyzedCommit != "sha-one" {
		t.Errorf("the analyzed member reads %q at %q, want pending at sha-one", app.Status, app.AnalyzedCommit)
	}
	for _, name := range []string{"octo/tool", "octo/unanalyzed"} {
		m := comparisonMemberNamed(t, c, name)
		if m.Status != ComparisonNotCompared || m.Reason != ReasonNotAnalyzed || m.Detail == "" {
			t.Errorf("%s reads %q %q %q, want not compared, not analyzed, with why",
				name, m.Status, m.Reason, m.Detail)
		}
		if m.Counted {
			t.Errorf("%s counts results the run never produced", name)
		}
	}
	pending, err := s.PendingComparisonMembers(id)
	if err != nil {
		t.Fatalf("PendingComparisonMembers: %v", err)
	}
	if len(pending) != 1 || pending[0] != heldAt() {
		t.Errorf("pending members are %+v, want only octo/app", pending)
	}
}

// What a held member kept and what a findings member's evaluation reported are both the run's
// results, read from where each keeps them.
func TestTheResultsComparedAreWhatTheMemberReported(t *testing.T) {
	s := newTestStore(t)
	runID := comparedHeldRun(t, s)
	held, err := s.MemberResults(runID, heldAt())
	if err != nil {
		t.Fatalf("MemberResults: %v", err)
	}
	if len(held) != 2 || held[0].RuleID != "actions/unpinned-tag" || held[0].FilePath != ".github/workflows/ci.yml" ||
		held[0].StartLine != 12 || held[0].StartColumn != 15 {
		t.Errorf("the held member's results read %+v", held)
	}

	findingsRun, fired := pickedRun(t, s)
	run, err := s.AnalysisRun(findingsRun)
	if err != nil {
		t.Fatal(err)
	}
	found, err := s.MemberResults(findingsRun, memberNamed(t, run, "octo/app").DatabaseIdentity)
	if err != nil {
		t.Fatalf("MemberResults: %v", err)
	}
	if len(found) == 0 {
		t.Fatalf("a findings member with finding %d lists no results", fired)
	}
}

func recordedApp(t *testing.T, s *Store, id int64) {
	t.Helper()
	behind := 3
	if err := s.RecordComparisonMember(id, ComparisonRecord{
		DatabaseIdentity: heldAt(), Status: ComparisonCompared,
		Results: []ComparedResult{
			{RuleID: "actions/unpinned-tag", FilePath: ".github/workflows/ci.yml", StartLine: 12, StartColumn: 15,
				AlertNumber: 7, AlertState: "open"},
			{RuleID: "actions/other", FilePath: ".github/workflows/ci.yml", StartLine: 30, StartColumn: 9},
		},
		Alerts: []RuleAlerts{
			{RuleID: "actions/unpinned-tag", State: "open", Alerts: 2},
			{RuleID: "actions/unpinned-tag", State: "fixed", Alerts: 1},
		},
		AlertedCommit: "sha-gh", AlertedAt: "2026-09-19T10:00:00Z",
		HeadCommit: "sha-head", Staleness: StalenessBehind, BehindBy: &behind,
	}); err != nil {
		t.Fatalf("RecordComparisonMember: %v", err)
	}
}

// The count that matters is the delta: a result an alert already stands for adds nothing. Open
// alerts the run did not report stay where they are, from what the repository already runs.
func TestARecordedMemberCountsWhatIsNewAndWhatIsAlreadyAlerted(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateComparison(comparedHeldRun(t, s), "operator")
	if err != nil {
		t.Fatal(err)
	}
	recordedApp(t, s, id)
	if err := s.FinishComparison(id); err != nil {
		t.Fatalf("FinishComparison: %v", err)
	}

	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != ComparisonComplete || c.FinishedAt == "" {
		t.Errorf("the finished comparison reads %q finished %q", c.State, c.FinishedAt)
	}
	app := comparisonMemberNamed(t, c, "octo/app")
	if !app.Counted || app.Counts.Results != 2 || app.Counts.Open != 1 || app.Counts.New != 1 {
		t.Errorf("octo/app counts %+v, want 2 results: 1 already open, 1 new", app.Counts)
	}
	if app.Counts.OpenAlerts != 2 || app.Counts.Missed != 1 {
		t.Errorf("octo/app counts %d open alerts and %d unreported, want 2 and 1",
			app.Counts.OpenAlerts, app.Counts.Missed)
	}
	if app.Staleness != StalenessBehind || app.BehindBy == nil || *app.BehindBy != 3 || app.HeadCommit != "sha-head" {
		t.Errorf("octo/app staleness reads %q %v at %q", app.Staleness, app.BehindBy, app.HeadCommit)
	}
	if app.Exact {
		t.Error("a member analyzed at another commit than GitHub's reads as exact")
	}
	if len(app.Rules) != 2 {
		t.Fatalf("octo/app splits into %+v, want both rules", app.Rules)
	}
	// Totals span what was compared, and nothing that was not.
	if c.Totals.Results != 2 || c.Totals.New != 1 || c.Totals.Open != 1 || c.Compared != 1 || c.NotCompared != 2 {
		t.Errorf("totals %+v over %d compared, %d not, want 2 results, 1 new, 1 compared, 2 not",
			c.Totals, c.Compared, c.NotCompared)
	}
	if c.Condition == "" || c.Merge == "" || c.Boundary == "" {
		t.Error("a comparison carries no condition, merge or boundary statement")
	}
	listed, err := s.ComparisonResults(id, "octo/app")
	if err != nil {
		t.Fatalf("ComparisonResults: %v", err)
	}
	if len(listed) != 2 || listed[0].Match != MatchOpen || listed[1].Match != MatchNew {
		t.Errorf("the listed results are %+v, want one already open then one new", listed)
	}
}

// A result whose alert was fixed reopens it, which a developer sees; one whose alert was dismissed
// stays dismissed. Neither is a result nothing stands for, and they are counted apart.
func TestAFixedAlertReopensAndADismissedOneStaysDismissed(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateComparison(comparedHeldRun(t, s), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordComparisonMember(id, ComparisonRecord{
		DatabaseIdentity: heldAt(), Status: ComparisonCompared,
		Results: []ComparedResult{
			{RuleID: "actions/unpinned-tag", FilePath: "a.yml", StartLine: 1, AlertNumber: 1, AlertState: "fixed"},
			{RuleID: "actions/unpinned-tag", FilePath: "b.yml", StartLine: 1, AlertNumber: 2, AlertState: "dismissed"},
		},
		Staleness: StalenessCurrent, AlertedCommit: "sha-one", AlertedCLI: "2.27.1", HeadCommit: "sha-one",
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	app := comparisonMemberNamed(t, c, "octo/app")
	if app.Counts.Reopens != 1 || app.Counts.Dismissed != 1 || app.Counts.New != 0 || app.Counts.Adds != 1 {
		t.Errorf("counts %+v, want 1 reopened, 1 dismissed, and the reopened one added", app.Counts)
	}
	if !app.Exact {
		t.Error("a member analyzed at the commit and under the CodeQL GitHub analyzed with reads as an inference")
	}
}

// Measured on has-ghas/build-applications-w-copilot-agent-mode: the same commit, analyzed by
// GitHub under CodeQL 2.23.1 and by the run under 2.27.1, gave 7 results no alert stood for, all
// of them reusable workflows the newer query reports. Same code, different analyzer: an inference.
func TestAMemberAnalyzedUnderAnotherCodeQLThanGitHubsIsAnInference(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateComparison(comparedHeldRun(t, s), "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordComparisonMember(id, ComparisonRecord{
		DatabaseIdentity: heldAt(), Status: ComparisonCompared, Staleness: StalenessCurrent,
		AlertedCommit: "sha-one", AlertedCLI: "2.23.1", HeadCommit: "sha-one",
	}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	app := comparisonMemberNamed(t, c, "octo/app")
	if app.Exact {
		t.Error("a member GitHub analyzed under another CodeQL reads as a measurement")
	}
	if app.AlertedCLI != "2.23.1" {
		t.Errorf("the member names GitHub's CodeQL as %q, want 2.23.1", app.AlertedCLI)
	}
}

// A comparison is written down so it can be checked afterwards, so analyzing the run again,
// which replaces what it held, must not change what the comparison says.
func TestAComparisonIsKeptWhenItsRunIsAnalyzedAgain(t *testing.T) {
	s := newTestStore(t)
	runID := comparedHeldRun(t, s)
	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	recordedApp(t, s, id)

	if err := s.RecordHeldMemberResult(runID, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 0, Counted: true, QueryRunID: heldEvaluation(t, s),
		ByQuery: map[string]int{},
	}, nil); err != nil {
		t.Fatalf("analyzing the member again: %v", err)
	}
	listed, err := s.ComparisonResults(id, "octo/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 {
		t.Errorf("the comparison lists %d results after its run changed, want the 2 it compared", len(listed))
	}
}

// A comparison is not recorded twice for one member, which would count its results twice.
func TestAMemberIsComparedOnce(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateComparison(comparedHeldRun(t, s), "operator")
	if err != nil {
		t.Fatal(err)
	}
	recordedApp(t, s, id)
	if err := s.RecordComparisonMember(id, ComparisonRecord{
		DatabaseIdentity: heldAt(), Status: ComparisonCompared,
	}); err == nil {
		t.Error("a member already compared was compared again")
	}
}

// A server that stopped mid-comparison leaves members nobody read. Reporting it running would say
// work is happening, and reporting it complete would count what was never read.
func TestAComparisonLeftRunningByAStoppedServerIsInterrupted(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateComparison(comparedHeldRun(t, s), "operator")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.InterruptAbandonedComparisons()
	if err != nil || n != 1 {
		t.Fatalf("InterruptAbandonedComparisons = %d, %v; want 1", n, err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != ComparisonInterrupted {
		t.Errorf("the abandoned comparison reads %q, want %q", c.State, ComparisonInterrupted)
	}
	if comparisonMemberNamed(t, c, "octo/app").Status != ComparisonPending {
		t.Error("a member nobody read reads as anything but still to read")
	}
	listed, err := s.ComparisonsOf(c.RunID)
	if err != nil || len(listed) != 1 || listed[0].RowID != id {
		t.Errorf("the run lists comparisons %+v (%v), want this one", listed, err)
	}
	if _, err := s.Comparison(id + 99); !errors.Is(err, ErrNoComparison) {
		t.Errorf("reading a comparison that does not exist returned %v", err)
	}
}

// A findings member counted before members kept their evaluation has a count and nothing to list
// it from. Compared, it would read as a repository with no results, which is the zero it is not.
func TestAMemberCountedBeforeItsEvaluationWasKeptIsNotComparedAsEmpty(t *testing.T) {
	s := newTestStore(t)
	id := startedRun(t, s)
	at := DatabaseIdentity{Repository: "octo/app", Commit: "sha-one", SourceFilter: "f0", Mode: AnalysisModeRegular}
	if err := s.RecordAnalysisMemberResult(id, at, MemberResult{
		Status: AnalysisSucceeded, Counted: true, Results: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnalysisRunState(id, RunComplete); err != nil {
		t.Fatal(err)
	}
	cid, err := s.CreateComparison(id, "operator")
	if err != nil {
		t.Fatalf("CreateComparison: %v", err)
	}
	c, err := s.Comparison(cid)
	if err != nil {
		t.Fatal(err)
	}
	app := comparisonMemberNamed(t, c, "octo/app")
	if app.Status != ComparisonNotCompared || app.Reason != ReasonUnlinked || !app.Counted || app.Counts.Results != 3 {
		t.Errorf("the unlinked member reads %q %q counted=%v %d, want not compared, unlinked, keeping its 3",
			app.Status, app.Reason, app.Counted, app.Counts.Results)
	}
}
