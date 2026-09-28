package compare

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakeReader answers per repository, and counts what it was asked.
type fakeReader struct {
	mu       sync.Mutex
	alerts   map[string][]catalog.Alert
	alertErr map[string]error
	analyses map[string]catalog.Analysis
	heads    map[string]string
	moved    map[string]catalog.Moved
	cancel   context.CancelFunc
	asked    []string
	// refs is the ref each analyses read asked for, and first runs once before the first read.
	refs  map[string]string
	first func()
}

func (f *fakeReader) CodeScanningAlerts(_ context.Context, repository string) ([]catalog.Alert, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, repository)
	if f.first != nil {
		f.first()
		f.first = nil
	}
	if f.cancel != nil {
		f.cancel()
	}
	if err := f.alertErr[repository]; err != nil {
		return nil, err
	}
	return f.alerts[repository], nil
}

func (f *fakeReader) ActionsAnalysis(_ context.Context, repository, ref string) (catalog.Analysis, bool, error) {
	f.mu.Lock()
	if f.refs == nil {
		f.refs = map[string]string{}
	}
	f.refs[repository] = ref
	f.mu.Unlock()
	a, ok := f.analyses[repository]
	return a, ok, nil
}

func (f *fakeReader) HeadSHA(_ context.Context, repository string) (string, error) {
	head, ok := f.heads[repository]
	if !ok {
		return "", errors.New("the default branch could not be read")
	}
	return head, nil
}

func (f *fakeReader) CompareCommits(_ context.Context, repository, _, _ string) (catalog.Moved, error) {
	m, ok := f.moved[repository]
	if !ok {
		return catalog.Moved{}, errors.New("no comparison")
	}
	return m, nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func unpinned(path string, line int) store.Finding {
	return store.Finding{Provider: store.ProviderCodeQL, RuleID: "actions/unpinned-tag", FilePath: path,
		StartLine: line, StartColumn: 15, Message: "Unpinned 3rd party Action", Severity: "warning",
		Fingerprint: fmt.Sprintf("fp-%s-%d", path, line)}
}

func alert(number int, state, path string, line int) catalog.Alert {
	return catalog.Alert{Number: number, RuleID: "actions/unpinned-tag", State: state, Tool: "CodeQL",
		Category: "/language:actions", CommitSHA: "c1", Path: path, StartLine: line, StartColumn: 15}
}

// heldRun records a finished held run of actions/unpinned-tag over the named repositories, each
// at commit c1 holding what results names for it.
func heldRun(t *testing.T, s *store.Store, results map[string][]store.Finding) int64 {
	t.Helper()
	var dbs []store.ScopedDatabase
	for name := range results {
		dbs = append(dbs, store.ScopedDatabase{DatabaseIdentity: store.DatabaseIdentity{
			Repository: name, Commit: "c1", Mode: store.AnalysisModeRegular,
		}, Status: store.DatabaseRetained, Path: "/db/" + name})
	}
	id, err := s.CreateAnalysisRun(store.AnalysisRequest{
		RequestedBy: "operator", Held: true, Mode: store.AnalysisModeRegular,
		Queries:   []store.AnalysisQuery{{QueryID: "actions/unpinned-tag", QueryVersion: "u1"}},
		Databases: dbs,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	for name, held := range results {
		qr, err := s.RecordQueryRun(store.QueryRun{
			QueryID: "actions/unpinned-tag", QueryVersion: "u1", CLIVersion: "2.27.1",
			Repository: name, DBCommit: "c1", AnalysisMode: store.AnalysisModeRegular,
			RecordedBy: store.RecordedByRun, Held: true,
		})
		if err != nil {
			t.Fatalf("RecordQueryRun: %v", err)
		}
		at := store.DatabaseIdentity{Repository: name, Commit: "c1", Mode: store.AnalysisModeRegular}
		if err := s.RecordHeldMemberResult(id, at, store.MemberResult{
			Status: store.AnalysisSucceeded, Counted: true, Results: len(held), QueryRunID: qr,
			ByQuery: map[string]int{"actions/unpinned-tag": len(held)},
		}, held); err != nil {
			t.Fatalf("RecordHeldMemberResult: %v", err)
		}
	}
	if err := s.SetAnalysisRunCodeQL(id, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAnalysisRunState(id, store.RunComplete); err != nil {
		t.Fatal(err)
	}
	return id
}

func compared(t *testing.T, s *store.Store, read Reader, runID int64) store.Comparison {
	t.Helper()
	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatalf("CreateComparison: %v", err)
	}
	if err := (&Comparer{Store: s, Read: read}).Compare(context.Background(), id); err != nil {
		t.Fatalf("Compare: %v", err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func memberNamed(t *testing.T, c store.Comparison, repository string) store.ComparisonMember {
	t.Helper()
	for _, m := range c.Members {
		if m.Repository == repository {
			return m
		}
	}
	t.Fatalf("no member %s", repository)
	return store.ComparisonMember{}
}

// A rule that fires where alerts already exist adds only what no alert stands for. The count that
// matters to whoever is asked about it is the delta, never the total.
func TestResultsAlreadyAlertedAddNothingAndTheRestAreNew(t *testing.T) {
	results := []store.ComparedResult{
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 10, StartColumn: 15},
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 20, StartColumn: 15},
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 30, StartColumn: 15},
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 40, StartColumn: 15},
	}
	alerts := []catalog.Alert{
		alert(1, "open", "ci.yml", 10),
		alert(2, "dismissed", "ci.yml", 20),
		alert(3, "fixed", "ci.yml", 30),
		// Another rule on the same line is another alert, and stands for nothing here.
		{Number: 4, RuleID: "actions/untrusted-checkout", State: "open", Path: "ci.yml", StartLine: 40, StartColumn: 15},
	}
	got := Match(results, alerts)
	want := []string{store.MatchOpen, store.MatchDismissed, store.MatchReopens, store.MatchNew}
	for i, r := range got {
		if r.Match != want[i] {
			t.Errorf("result at line %d is %q (alert %d), want %q", r.StartLine, r.Match, r.AlertNumber, want[i])
		}
	}
	if got[3].AlertNumber != 0 {
		t.Errorf("a result under another rule's alert names alert %d", got[3].AlertNumber)
	}
}

// One alert is one thing a developer sees, so it stands for one result; and an open alert is the
// one a developer would see first.
func TestAnAlertStandsForOneResultAndTheOpenOneFirst(t *testing.T) {
	results := []store.ComparedResult{
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 10, StartColumn: 15},
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 10, StartColumn: 15},
	}
	alerts := []catalog.Alert{alert(9, "fixed", "ci.yml", 10), alert(2, "open", "ci.yml", 10)}
	got := Match(results, alerts)
	if got[0].AlertNumber != 2 || got[0].Match != store.MatchOpen {
		t.Errorf("the first result took alert %d (%s), want the open one, 2", got[0].AlertNumber, got[0].Match)
	}
	if got[1].AlertNumber != 9 {
		t.Errorf("the second result took alert %d, want the one left, 9", got[1].AlertNumber)
	}

	lone := Match(results, []catalog.Alert{alert(2, "open", "ci.yml", 10)})
	if lone[1].Match != store.MatchNew {
		t.Errorf("one alert stood for two results: %+v", lone)
	}
}

// A CodeQL release can move where on a line a result starts without moving the alert.
func TestAResultOnTheSameLineUnderTheSameRuleIsTheSameAlert(t *testing.T) {
	results := []store.ComparedResult{
		{RuleID: "actions/unpinned-tag", FilePath: "ci.yml", StartLine: 10, StartColumn: 9},
	}
	got := Match(results, []catalog.Alert{alert(1, "open", "ci.yml", 10)})
	if got[0].AlertNumber != 1 {
		t.Errorf("a result on the alert's line under its rule took alert %d, want 1", got[0].AlertNumber)
	}
}

// Comparing a query the repository already runs adds nothing: every result it reports is an alert
// developers already see.
func TestComparingAQueryAlreadyRunningAddsNothing(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{
		"octo/app": {unpinned("ci.yml", 10), unpinned("ci.yml", 20)},
	})
	read := &fakeReader{
		alerts:   map[string][]catalog.Alert{"octo/app": {alert(1, "open", "ci.yml", 10), alert(2, "open", "ci.yml", 20)}},
		analyses: map[string]catalog.Analysis{"octo/app": {CommitSHA: "c1", CreatedAt: "2026-09-19T10:00:00Z", ToolVersion: "2.27.1"}},
		heads:    map[string]string{"octo/app": "c1"},
	}
	c := compared(t, s, read, runID)
	if c.State != store.ComparisonComplete {
		t.Fatalf("the comparison is %q", c.State)
	}
	if c.Totals.Results != 2 || c.Totals.Adds != 0 || c.Totals.Open != 2 || c.Totals.Missed != 0 {
		t.Errorf("totals %+v, want 2 results both already open and nothing added", c.Totals)
	}
	app := memberNamed(t, c, "octo/app")
	if app.Staleness != store.StalenessCurrent || !app.Exact || app.AlertedCommit != "c1" || app.AlertedCLI != "2.27.1" {
		t.Errorf("octo/app reads %q exact=%v alerted at %q under %q, want current and exact at c1 under 2.27.1",
			app.Staleness, app.Exact, app.AlertedCommit, app.AlertedCLI)
	}
}

// Code scanning never having analyzed a repository, holding no Actions analysis there, and
// refusing to say are three different reasons nothing was compared, and each is named rather
// than counted, because a delta over an unnamed subset is quoted as if it covered the scope.
func TestARepositoryNotComparedIsNamedWithWhyAndLeftOutOfTheTotals(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{
		"octo/app":       {unpinned("ci.yml", 10)},
		"octo/never":     {unpinned("ci.yml", 10)},
		"octo/js-only":   {unpinned("ci.yml", 10), unpinned("ci.yml", 20)},
		"octo/forbidden": {unpinned("ci.yml", 10)},
	})
	read := &fakeReader{
		alerts: map[string][]catalog.Alert{"octo/js-only": {}},
		alertErr: map[string]error{
			"octo/never":     &catalog.NoAnalysisError{Repository: "octo/never", Reason: "no analysis found"},
			"octo/forbidden": &catalog.StatusError{Code: 403, Message: "Advanced Security must be enabled"},
		},
		analyses: map[string]catalog.Analysis{"octo/app": {CommitSHA: "c0"}},
		heads:    map[string]string{"octo/app": "c9"},
		moved:    map[string]catalog.Moved{"octo/app": {Status: "ahead", AheadBy: 4}},
	}
	c := compared(t, s, read, runID)

	for name, reason := range map[string]string{
		"octo/never":     store.ReasonNoCodeScanning,
		"octo/js-only":   store.ReasonNoActionsAnalysis,
		"octo/forbidden": store.ReasonUnread,
	} {
		m := memberNamed(t, c, name)
		if m.Status != store.ComparisonNotCompared || m.Reason != reason || m.Detail == "" {
			t.Errorf("%s reads %q %q %q, want not compared, %q, with why", name, m.Status, m.Reason, m.Detail, reason)
		}
		if !m.Counted || m.Counts.Results == 0 {
			t.Errorf("%s does not keep how many results the run had there", name)
		}
	}
	if m := memberNamed(t, c, "octo/forbidden"); !strings.Contains(m.Detail, "Advanced Security must be enabled") {
		t.Errorf("the unread member's detail %q does not carry GitHub's reason", m.Detail)
	}
	if m := memberNamed(t, c, "octo/never"); strings.Count(m.Detail, "no analysis found") != 1 || strings.Contains(m.Detail, "octo/never") {
		t.Errorf("the never-analyzed member's detail %q does not say GitHub's reason once, plainly", m.Detail)
	}
	if c.Compared != 1 || c.NotCompared != 3 || c.Totals.Results != 1 || c.Totals.New != 1 {
		t.Errorf("%d compared, %d not, totals %+v; want only octo/app's 1 new result counted",
			c.Compared, c.NotCompared, c.Totals)
	}
	app := memberNamed(t, c, "octo/app")
	if app.Staleness != store.StalenessBehind || app.BehindBy == nil || *app.BehindBy != 4 || app.Exact {
		t.Errorf("octo/app reads %q behind %v exact=%v, want behind by 4 and an inference",
			app.Staleness, app.BehindBy, app.Exact)
	}
}

// A default branch nobody could read says nothing about whether the analysis is current, so the
// staleness is unknown rather than current.
func TestADefaultBranchThatCouldNotBeReadIsUnknownNotCurrent(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{"octo/app": {unpinned("ci.yml", 10)}})
	read := &fakeReader{
		alerts: map[string][]catalog.Alert{"octo/app": {alert(1, "open", "ci.yml", 10)}},
	}
	c := compared(t, s, read, runID)
	app := memberNamed(t, c, "octo/app")
	if app.Status != store.ComparisonCompared {
		t.Fatalf("an Actions alert did not count as evidence of Actions analysis: %+v", app)
	}
	if app.Staleness != store.StalenessUnknown || app.HeadCommit != "" {
		t.Errorf("octo/app reads %q at %q, want unknown with no head", app.Staleness, app.HeadCommit)
	}
}

// Stopped part way, a comparison keeps what it read and says it did not finish.
func TestAComparisonStoppedPartWayIsInterrupted(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{
		"octo/a": {unpinned("ci.yml", 10)}, "octo/b": {unpinned("ci.yml", 10)},
	})
	ctx, cancel := context.WithCancel(context.Background())
	read := &fakeReader{cancel: cancel}
	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Comparer{Store: s, Read: read, Parallel: 1}).Compare(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatalf("Compare returned %v, want the cancellation", err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != store.ComparisonInterrupted || c.Pending == 0 {
		t.Errorf("the stopped comparison reads %q with %d still to read", c.State, c.Pending)
	}
}

// The analysis read is the default branch's: the ref the alerts' own instances name, or the one
// enumeration recorded where there is no alert to name it.
func TestTheAnalysisIsReadForTheDefaultBranch(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{
		"octo/app": {unpinned("ci.yml", 10)}, "octo/quiet": {unpinned("ci.yml", 10)},
	})
	if _, err := s.UpsertRepository("octo/quiet", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBranch("octo/quiet", "trunk"); err != nil {
		t.Fatal(err)
	}
	withRef := alert(1, "open", "ci.yml", 10)
	withRef.Ref = "refs/heads/main"
	read := &fakeReader{
		alerts:   map[string][]catalog.Alert{"octo/app": {withRef}, "octo/quiet": {}},
		analyses: map[string]catalog.Analysis{"octo/app": {CommitSHA: "c1"}, "octo/quiet": {CommitSHA: "c1"}},
	}
	compared(t, s, read, runID)
	if read.refs["octo/app"] != "refs/heads/main" || read.refs["octo/quiet"] != "trunk" {
		t.Errorf("the analyses were read for %v, want refs/heads/main and trunk", read.refs)
	}
}

// What a comparison sets against the alerts is the run's results as they were when it began:
// analyzing the run again part way through would otherwise mix two runs under one CodeQL.
func TestTheResultsComparedAreTheRunsAsTheComparisonBegan(t *testing.T) {
	s := newStore(t)
	runID := heldRun(t, s, map[string][]store.Finding{
		"octo/a": {unpinned("ci.yml", 10)}, "octo/b": {unpinned("ci.yml", 10), unpinned("ci.yml", 20)},
	})
	read := &fakeReader{
		alerts:   map[string][]catalog.Alert{"octo/a": {}, "octo/b": {}},
		analyses: map[string]catalog.Analysis{"octo/a": {CommitSHA: "c1"}, "octo/b": {CommitSHA: "c1"}},
	}
	read.first = func() {
		for _, name := range []string{"octo/a", "octo/b"} {
			at := store.DatabaseIdentity{Repository: name, Commit: "c1", Mode: store.AnalysisModeRegular}
			if err := s.RecordHeldMemberResult(runID, at, store.MemberResult{
				Status: store.AnalysisSucceeded, Counted: true, ByQuery: map[string]int{},
			}, nil); err != nil {
				t.Errorf("analyzing the run again: %v", err)
			}
		}
	}
	id, err := s.CreateComparison(runID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err := (&Comparer{Store: s, Read: read, Parallel: 1}).Compare(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	c, err := s.Comparison(id)
	if err != nil {
		t.Fatal(err)
	}
	if c.Totals.Results != 3 {
		t.Errorf("the comparison counted %d results, want the 3 the run had when it began", c.Totals.Results)
	}
}
