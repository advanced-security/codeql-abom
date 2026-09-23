package store

import (
	"testing"
)

// databaseScope is matchFixture with an analysis history, which is what a scope resolves through:
// a repository nothing has ever analyzed names no commit, so it names no database.
func databaseScope(t *testing.T) *Store {
	t.Helper()
	s := matchFixture(t)
	for _, r := range []struct{ repo, commit string }{
		{"octo-org/app", "sha-app"},
		{"octo-org/tool", "sha-tool"},
	} {
		if _, err := s.RecordQueryRun(QueryRun{
			AnalysisMode: AnalysisModeRegular,
			QueryID:      "abom/edges",
			Repository:   r.repo,
			DBCommit:     r.commit,
			SourceFilter: "f0",
		}); err != nil {
			t.Fatalf("RecordQueryRun(%s): %v", r.repo, err)
		}
	}
	return s
}

func repositoriesOf(set DatabaseSet) map[string]bool {
	out := map[string]bool{}
	for _, d := range set.Databases {
		out[d.Repository] = true
	}
	return out
}

// One predicate, two directions. "What am I looking at" and "what am I about to run this against"
// must be the same question, compared rather than assumed to agree.
func TestAScopeResolvesToTheDatabasesBehindTheNodesItMatched(t *testing.T) {
	s := databaseScope(t)
	sel := FindingSelector{Rules: []string{"actions/unpinned-tag"}}

	nodes, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	wanted := map[string]bool{}
	for _, n := range nodes.Nodes {
		for _, repo := range n.Repositories {
			wanted[repo] = true
		}
	}
	if len(wanted) == 0 {
		t.Fatal("the selector matched no repositories, so the comparison proves nothing")
	}

	set, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	got := repositoriesOf(set)
	for repo := range wanted {
		if !got[repo] && !set.gapped(repo) {
			t.Errorf("%s carries a matched node and resolves to neither a database nor a gap",
				repo)
		}
	}
	for repo := range got {
		if !wanted[repo] {
			t.Errorf("%s resolved to a database without carrying a matched node", repo)
		}
	}
}

// A repository nobody has analyzed is the one a reader most needs told about. Returning only what
// is on record makes a scope quietly mean something narrower than it says.
func TestARepositoryWithNoAnalysisIsAStatedGapNotAQuietOmission(t *testing.T) {
	s := matchFixture(t)
	// No query runs at all, so every matched repository is unanalyzed.
	set, err := s.DatabasesMatching(FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if len(set.Databases) != 0 {
		t.Errorf("resolved %d databases against a corpus nothing has analyzed", len(set.Databases))
	}
	if len(set.Gaps) == 0 {
		t.Fatal("a scope over unanalyzed repositories reported no gap")
	}
	for _, g := range set.Gaps {
		if g.Repository == "" || g.Reason == "" {
			t.Errorf("gap %+v names neither a repository nor a reason", g)
		}
	}
}

// The gap plus the resolved set has to account for every repository the scope named, or the
// answer is smaller than the question and nothing says so.
func TestTheResolvedSetAndTheGapsAccountForEveryRepositoryInScope(t *testing.T) {
	s := databaseScope(t)
	// One repository analyzed, one not, so the answer has to be made of both halves.
	if _, err := s.DB().Exec(
		`DELETE FROM abom_query_run WHERE repository = ?`, "octo-org/tool"); err != nil {
		t.Fatalf("removing the analysis history: %v", err)
	}
	sel := FindingSelector{Rules: []string{"actions/unpinned-tag"}}

	nodes, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	named := map[string]bool{}
	for _, n := range nodes.Nodes {
		for _, repo := range n.Repositories {
			named[repo] = true
		}
	}

	set, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	accounted := repositoriesOf(set)
	for _, g := range set.Gaps {
		accounted[g.Repository] = true
	}
	if len(accounted) != len(named) {
		t.Errorf("the scope named %d repositories and the answer accounts for %d",
			len(named), len(accounted))
	}
	if len(set.Databases) == 0 || len(set.Gaps) == 0 {
		t.Errorf("want both halves, got %d databases and %d gaps",
			len(set.Databases), len(set.Gaps))
	}
}

// Running against a database already on disk costs nothing to prepare, and running against one
// that has to be extracted does. A reader deciding whether to press anything needs them apart.
func TestADatabaseOnDiskResolvesApartFromOneThatWouldHaveToBeBuilt(t *testing.T) {
	s := databaseScope(t)
	if _, err := s.RetainDatabase(RetainedDatabase{
		DatabaseIdentity: DatabaseIdentity{
			Repository: "octo-org/app", Commit: "sha-app",
			SourceFilter: "f0", Mode: AnalysisModeRegular,
		},
		Path:  "/cache/octo-org/app/sha-app/db",
		Bytes: 8192,
	}); err != nil {
		t.Fatalf("RetainDatabase: %v", err)
	}

	set, err := s.DatabasesMatching(FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	states := map[string]string{}
	for _, d := range set.Databases {
		states[d.Repository] = d.Status
		if d.Commit == "" {
			t.Errorf("%s resolved without naming the commit it would be analyzed at", d.Repository)
		}
	}
	if states["octo-org/app"] != DatabaseRetained {
		t.Errorf("octo-org/app resolved as %q, want %q: its database is on disk",
			states["octo-org/app"], DatabaseRetained)
	}
	if states["octo-org/tool"] != DatabaseBuildable {
		t.Errorf("octo-org/tool resolved as %q, want %q: it has been analyzed but is not held",
			states["octo-org/tool"], DatabaseBuildable)
	}
	for _, d := range set.Databases {
		if d.Status == DatabaseRetained && d.Path == "" {
			t.Errorf("%s is on disk and names no path", d.Repository)
		}
		if d.Status == DatabaseBuildable && d.Path != "" {
			t.Errorf("%s is not held and names a path at %q", d.Repository, d.Path)
		}
	}
}

// The two shapes answer different questions with the same rule ids, so a scope asking for the one
// that does not exist must be told, never handed the other.
func TestAnExtendedScopeNeverFallsBackToTheRegularDatabases(t *testing.T) {
	s := databaseScope(t)
	sel := FindingSelector{Rules: []string{"actions/unpinned-tag"}}

	// The same predicate in the shape that does exist, so an empty extended answer is about the
	// mode rather than about the predicate matching nothing.
	regular, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if len(regular.Databases) == 0 {
		t.Fatal("the predicate resolves to nothing in regular mode, so the comparison is empty")
	}

	sel.Mode = AnalysisModeExtended
	set, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if len(set.Databases) != 0 {
		t.Errorf("an extended scope resolved to %d databases against a corpus that holds none",
			len(set.Databases))
	}
	if set.Mode != AnalysisModeExtended {
		t.Errorf("the answer reports mode %q, want the one that was asked for", set.Mode)
	}
	if set.Unresolved == "" {
		t.Error("an extended scope resolved to nothing and did not say why, which reads as " +
			"a clean result rather than an unanswerable question")
	}
}

// A resolution nothing acted on has to answer the same way twice, or a reader cannot trust the
// size they were shown before pressing anything.
func TestResolvingTwiceWithNothingChangedNamesTheSameDatabases(t *testing.T) {
	s := databaseScope(t)
	sel := FindingSelector{Rules: []string{"actions/unpinned-tag"}}

	first, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	second, err := s.DatabasesMatching(sel)
	if err != nil {
		t.Fatalf("DatabasesMatching again: %v", err)
	}
	if len(first.Databases) != len(second.Databases) {
		t.Fatalf("resolved %d databases and then %d", len(first.Databases), len(second.Databases))
	}
	for i := range first.Databases {
		if first.Databases[i].DatabaseIdentity != second.Databases[i].DatabaseIdentity {
			t.Errorf("database %d resolved as %+v and then %+v",
				i, first.Databases[i].DatabaseIdentity, second.Databases[i].DatabaseIdentity)
		}
	}
}

// The same safest-possible default NodesMatching holds: a policy that failed to parse must run
// against nothing rather than against the whole organization.
func TestAnEmptySelectorResolvesToNoDatabases(t *testing.T) {
	set, err := databaseScope(t).DatabasesMatching(FindingSelector{})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if len(set.Databases) != 0 || len(set.Gaps) != 0 {
		t.Errorf("an empty selector resolved to %d databases and %d gaps, want none",
			len(set.Databases), len(set.Gaps))
	}
}
