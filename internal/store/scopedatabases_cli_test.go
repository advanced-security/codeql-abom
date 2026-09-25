package store

import (
	"strings"
	"testing"
)

// heldBy retains octo-org/app's and octo-org/tool's databases, each built by the CodeQL named.
func heldBy(t *testing.T, s *Store, app, tool string) {
	t.Helper()
	for repo, cli := range map[string]string{"octo-org/app": app, "octo-org/tool": tool} {
		sha := "sha-" + strings.TrimPrefix(repo, "octo-org/")
		if _, err := s.RetainDatabase(RetainedDatabase{
			DatabaseIdentity: DatabaseIdentity{
				Repository: repo, Commit: sha, SourceFilter: "f0", Mode: AnalysisModeRegular,
			},
			Path: "/cache/" + repo + "/" + sha + "/db", Bytes: 8192, CLIVersion: cli,
		}); err != nil {
			t.Fatalf("RetainDatabase(%s): %v", repo, err)
		}
	}
}

func resolvedAs(set DatabaseSet) map[string]ScopedDatabase {
	out := map[string]ScopedDatabase{}
	for _, d := range set.Databases {
		out[d.Repository] = d
	}
	return out
}

// A database another CodeQL built is not what this one would run against: the executor rebuilds
// it, so counting it as held before Run states a cost that is not the cost spent.
func TestAHeldDatabaseAnotherCodeQLBuiltResolvesAsBuildableAndSaysWhy(t *testing.T) {
	s := databaseScope(t)
	heldBy(t, s, "2.27.0", "2.27.1")

	set, err := s.DatabasesMatching(FindingSelector{
		Repositories: []string{"octo-org/app", "octo-org/tool"},
	})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	got := resolvedAs(set.BuiltBy("2.27.1"))

	app := got["octo-org/app"]
	if app.Status != DatabaseBuildable {
		t.Errorf("octo-org/app resolved as %q, want %q: CodeQL 2.27.0 built it and 2.27.1 runs",
			app.Status, DatabaseBuildable)
	}
	if !strings.Contains(app.Reason, "2.27.0") {
		t.Errorf("octo-org/app does not say which CodeQL built what is held: %q", app.Reason)
	}
	if app.Path != "" || app.Bytes != 0 {
		t.Errorf("a database the run would rebuild still names the old one: %+v", app)
	}
	if app.Commit != "sha-app" || app.SourceFilter != "f0" {
		t.Errorf("the rebuild is not at the held database's identity: %+v", app)
	}
	tool := got["octo-org/tool"]
	if tool.Status != DatabaseRetained || tool.Reason != "" || tool.Path == "" {
		t.Errorf("a database this CodeQL built did not resolve as held: %+v", tool)
	}
}

// A row that records no CodeQL is not known to be this one's, so it is not counted as held either.
func TestAHeldDatabaseWithNoCodeQLRecordedIsNotCountedAsHeld(t *testing.T) {
	s := databaseScope(t)
	heldBy(t, s, "", "2.27.1")

	set, err := s.DatabasesMatching(FindingSelector{Repositories: []string{"octo-org/app"}})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	app := resolvedAs(set.BuiltBy("2.27.1"))["octo-org/app"]
	if app.Status != DatabaseBuildable || app.Reason == "" {
		t.Errorf("a database with no CodeQL recorded resolved as %+v, want buildable with a reason",
			app)
	}
}

// Where the CodeQL running cannot be read, nothing can be compared, and inventing a mismatch would
// send every run to rebuild everything.
func TestAResolutionWithNoCodeQLToCompareLeavesHeldDatabasesHeld(t *testing.T) {
	s := databaseScope(t)
	heldBy(t, s, "2.27.0", "")

	set, err := s.DatabasesMatching(FindingSelector{
		Repositories: []string{"octo-org/app", "octo-org/tool"},
	})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	for repo, d := range resolvedAs(set.BuiltBy("")) {
		if d.Status != DatabaseRetained || d.Reason != "" {
			t.Errorf("%s resolved as %+v with no CodeQL to compare against", repo, d)
		}
	}
}

// The run records what the preview said, so its member names the reason rather than claiming
// nothing was held.
func TestARunOverADatabaseAnotherCodeQLBuiltSaysItBuildsItAgain(t *testing.T) {
	s := databaseScope(t)
	heldBy(t, s, "2.27.0", "2.27.1")
	set, err := s.DatabasesMatching(FindingSelector{Repositories: []string{"octo-org/app"}})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	set = set.BuiltBy("2.27.1")

	id, err := s.CreateAnalysisRun(AnalysisRequest{
		RequestedBy: "octocat",
		Queries:     []AnalysisQuery{{QueryID: "abom/edges", QueryVersion: "v1"}},
		Predicate:   "repositories=octo-org/app", Mode: set.Mode,
		Databases: set.Databases, Gaps: set.Gaps,
	})
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	run, err := s.AnalysisRun(id)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if len(run.Members) != 1 {
		t.Fatalf("want one member, got %+v", run.Members)
	}
	m := run.Members[0]
	if m.Status != AnalysisPending || m.DatabasePath != "" {
		t.Errorf("the member would run against the old database: %+v", m)
	}
	if !strings.Contains(m.Detail, "2.27.0") {
		t.Errorf("the member does not say why it is rebuilt: %q", m.Detail)
	}
}
