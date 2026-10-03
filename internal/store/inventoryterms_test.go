package store

import (
	"slices"
	"testing"
)

// Every built-in view is written in terms the listing did not apply — `scope`, `flags` and `ref` —
// so a tab built from the catalog would have shown a refusal rather than rows. These pin the terms
// that unblock it.
func TestInventoryScopeOwnedKeepsOnlyWhatTheDeclarationHolds(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 4)
	for _, name := range []string{"vendor/action-a", "vendor/action-b", "vendor/action-c"} {
		if _, err := s.UpsertRepository(name, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "vendor", Kind: AccountOrganization,
		Enabled: true, IncludesOnly: true, Includes: []string{"action-a", "action-b"}}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 2 {
		t.Fatalf("matched %d rows, want the 2 the declaration holds", page.Total)
	}
	for _, it := range page.Items {
		if it.NodeKey != "vendor/action-a" && it.NodeKey != "vendor/action-b" {
			t.Errorf("row %q is outside the declaration", it.NodeKey)
		}
	}
}

// Declaring nothing shows nothing. Falling through to the whole corpus would answer a question
// about the operator's own supply chain with somebody else's.
func TestInventoryOwnedScopeWithNoDeclarationShowsNothing(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)

	page, err := s.ActionsInventoryPage(InventoryQuery{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 0 {
		t.Errorf("matched %d rows with nothing declared, want none", page.Total)
	}
}

func TestInventoryFindingsFlagKeepsOnlyRowsCarryingOne(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 4)
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	seedFindings(t, s, repoID, ".github/workflows/ci.yml", Finding{RuleID: "abom/a"})
	if _, err := s.DB().Exec(`
		DELETE FROM abom_finding_node;
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
		WHERE n.node_key = 'vendor/action-c'`); err != nil {
		t.Fatalf("attributing findings: %v", err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{Flags: []string{"findings"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 1 || page.Items[0].NodeKey != "vendor/action-c" {
		t.Errorf("matched %+v, want only the row carrying a finding", page.Items)
	}
}

// "Not analyzed" is a question about the dependency, not about its findings: a row nothing has
// queued is a coverage gap whether or not a rule ever looked at it.
func TestInventoryUnanalyzedFlagIsAboutTheDependency(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, status, depth, score, enqueued_at)
		VALUES ('vendor/action-a', 'v1', 'done', 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seeding frontier: %v", err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{Flags: []string{"unanalyzed"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	for _, it := range page.Items {
		if it.NodeKey == "vendor/action-a" {
			t.Errorf("row %q is analyzed and should not be a coverage gap", it.NodeKey)
		}
		if it.Analyzed {
			t.Errorf("row %q reports analyzed under the unanalyzed flag", it.NodeKey)
		}
	}
	if page.Total == 0 {
		t.Fatal("nothing matched, so the flag cannot be told from a broken query")
	}
}

// Lexical, and about how something is referenced rather than which reference was analyzed.
func TestInventoryRefStyleMatchesHowSomethingIsReferenced(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "vendor/by-tag", "v1", 1)
	seedDependency(t, s, "octo-org/app", "vendor/by-sha",
		"5a4ac9002d0be2fb38bd78e4b4dbde5606d7042f", 2)

	tagged, err := s.ActionsInventoryPage(InventoryQuery{RefStyle: "tag"})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if tagged.Total != 1 || tagged.Items[0].NodeKey != "vendor/by-tag" {
		t.Errorf("ref=tag matched %+v, want only the tag-referenced row", tagged.Items)
	}
	pinned, err := s.ActionsInventoryPage(InventoryQuery{RefStyle: "sha"})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if pinned.Total != 1 || pinned.Items[0].NodeKey != "vendor/by-sha" {
		t.Errorf("ref=sha matched %+v, want only the commit-pinned row", pinned.Items)
	}
}

// A reference is matched exactly as a workflow wrote it. `v4` is not `v4.2.2`, even where both name
// one commit today, and a dependency named at several references matches any one asked for.
func TestInventoryAtMatchesTheReferenceExactlyAsWritten(t *testing.T) {
	s := advisoryStore(t)
	seedDependency(t, s, "octo-org/app", "vendor/at-v4", "v4", 1)
	seedDependency(t, s, "octo-org/app", "vendor/at-v4-patch", "v4.2.2", 2)
	seedDependency(t, s, "octo-org/app", "vendor/at-main", "main", 3)

	page, err := s.ActionsInventoryPage(InventoryQuery{At: []string{"v4", "main"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.NodeKey)
	}
	slices.Sort(got)
	if want := []string{"vendor/at-main", "vendor/at-v4"}; !slices.Equal(got, want) || page.Total != 2 {
		t.Errorf("at=v4,main matched %v (total %d), want %v", got, page.Total, want)
	}
}

// The default is a default, not a cage: naming a kind reaches it. Without this a Repositories tab
// is a predicate that matches nothing, because the listing's own WHERE excluded the kind.
func TestInventoryReachesRepositoriesWhenTheKindIsNamed(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	// A project is two nodes — `repo:owner/name` and `owner/name` — and only the first is what a
	// Repositories tab is asking for.
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := NewWriter(s, 0)
	if err := w.AddNode(Node{Key: "repo:octo-org/app", Kind: "repo", RepositoryID: repoID}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{Kinds: []string{"repo"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total == 0 {
		t.Fatal("no repositories matched, so a Repositories tab would show an empty grid")
	}
	for _, it := range page.Items {
		if it.Kind != "repo" {
			t.Errorf("row %q is a %s", it.NodeKey, it.Kind)
		}
	}
}

// Steps and jobs are implementation detail and there are tens of thousands of them, so they stay
// out unless a reader names them.
func TestInventoryDefaultLeavesOutStepsAndJobs(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)

	page, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	for _, it := range page.Items {
		if it.Kind == "step" || it.Kind == "job" || it.Kind == "repo" {
			t.Errorf("row %q is a %s, which the default listing leaves out", it.NodeKey, it.Kind)
		}
	}
}
