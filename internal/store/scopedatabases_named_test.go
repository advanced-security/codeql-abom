package store

import "testing"

// A repository selection is its own scope: through findings, a clean repository is unreachable.
func TestARepositoryNamedOutrightResolvesWhetherOrNotAnythingWasFoundInIt(t *testing.T) {
	s := databaseScope(t)
	if _, err := s.RecordQueryRun(QueryRun{
		AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: "octo-org/clean", DBCommit: "sha-clean", SourceFilter: "f0",
	}); err != nil {
		t.Fatal(err)
	}

	set, err := s.DatabasesMatching(FindingSelector{
		Repositories: []string{"octo-org/clean", "octo-org/app", "octo-org/never"},
	})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	got := repositoriesOf(set)
	if !got["octo-org/clean"] {
		t.Errorf("a repository with no findings resolved to nothing: %+v", set)
	}
	if !got["octo-org/app"] {
		t.Errorf("a repository with findings dropped out of a named selection: %+v", set)
	}
	if !set.gapped("octo-org/never") {
		t.Errorf("a named repository nothing analyzed is not a stated gap: %+v", set.Gaps)
	}
}

// Named by hand, a repository arrives in whatever case the reader typed.
func TestARepositoryNamedInTheWrongCaseResolvesToTheOneOnRecord(t *testing.T) {
	s := databaseScope(t)
	set, err := s.DatabasesMatching(FindingSelector{Repositories: []string{"Octo-Org/App"}})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if !repositoriesOf(set)["octo-org/app"] {
		t.Errorf("Octo-Org/App did not resolve to octo-org/app: %+v", set)
	}
}

// With a rule, a repository still narrows its findings; the shortcut is for repositories alone.
func TestARepositoryAskedWithARuleStillNarrowsThatRule(t *testing.T) {
	s := databaseScope(t)
	set, err := s.DatabasesMatching(FindingSelector{
		Rules: []string{"abom/secrets-inherit"}, Repositories: []string{"octo-org/tool"},
	})
	if err != nil {
		t.Fatalf("DatabasesMatching: %v", err)
	}
	if len(set.Databases)+len(set.Gaps) != 0 {
		t.Errorf("a rule octo-org/tool never reported resolved to %+v", set)
	}
}
