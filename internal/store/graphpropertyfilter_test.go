package store

import (
	"path/filepath"
	"testing"
)

// sharedGroupingFixture holds two owned repositories in different groups that depend on a third
// owned repository, and an external action one of them uses.
//
//	octo-org/app     business_id=3   ─┐
//	octo-org/web     business_id=7   ─┴─> octo-org/shared  business_id=3
//	octo-org/app                        ─> actions/checkout (external, nobody's)
//
// The shared repository is the case no other fixture holds: an owned node that one group owns and
// another group reaches. Excluding its group must not take it away from the group that is kept.
func sharedGroupingFixture(t *testing.T) *Store {
	t.Helper()
	s, err := openTestStore(t, filepath.Join(t.TempDir(), "abom.db"), Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: "octo-org", Kind: AccountOrganization, Enabled: true},
	}, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	ids := map[string]int64{}
	for repo, group := range map[string]string{
		"octo-org/app": "3", "octo-org/web": "7", "octo-org/shared": "3",
	} {
		id, err := s.UpsertRepository(repo, "")
		if err != nil {
			t.Fatalf("UpsertRepository(%s): %v", repo, err)
		}
		ids[repo] = id
		if err := s.ReplacePropertyValues(repo, []PropertyValue{
			{Name: "business_id", Value: group},
		}); err != nil {
			t.Fatalf("ReplacePropertyValues(%s): %v", repo, err)
		}
	}

	w := NewWriter(s, 0)
	add := adder(t, w)
	edge := func(e Edge) {
		t.Helper()
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.Key, err)
		}
	}

	// The shared action is addressed the way a dependency is, by owner/repo, so the closure walk
	// can reach it: it resolves targets through the node's own columns, not its repository row.
	add(Node{Key: "octo-org/shared", Kind: "action", Owner: "octo-org", Repo: "shared",
		RepositoryID: ids["octo-org/shared"]})
	add(Node{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true})

	for _, repo := range []string{"octo-org/app", "octo-org/web"} {
		add(Node{Key: repo + "/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: ids[repo],
			Path: ".github/workflows/ci.yml"})
		add(Node{Key: repo + "-step", Kind: "step", RepositoryID: ids[repo],
			ContainerKey: repo + "/.github/workflows/ci.yml"})
		edge(Edge{Key: repo + "->shared", SrcKey: repo + "-step", DstKey: "octo-org/shared",
			Kind: "step-uses-action", FilePath: ".github/workflows/ci.yml", StartLine: 1,
			RepositoryID: ids[repo]})
	}
	edge(Edge{Key: "app->checkout", SrcKey: "octo-org/app-step", DstKey: "actions/checkout",
		Kind: "step-uses-action", FilePath: ".github/workflows/ci.yml", StartLine: 2,
		RepositoryID: ids["octo-org/app"]})

	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

// drawn reports which node keys a filtered graph holds.
func drawn(t *testing.T, s *Store, opts FullGraphOptions) map[string]bool {
	t.Helper()
	opts.IncludeSteps = true
	g, err := s.FullGraph(opts)
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	out := map[string]bool{}
	for _, n := range g.Nodes {
		out[n.Key] = true
	}
	return out
}

// The point of applying the filter to a graph rather than refusing it: a group's own workflows are
// worth little without the chain leaving them, which is the question this project exists to answer.
func TestAFilteredGraphKeepsTheGroupsOwnNodesAndWhatTheyReach(t *testing.T) {
	s := sharedGroupingFixture(t)

	got := drawn(t, s, FullGraphOptions{GroupSources: []string{"octo-org/app", "octo-org/shared"}})

	for _, key := range []string{"octo-org/app/.github/workflows/ci.yml", "octo-org/shared",
		"actions/checkout"} {
		if !got[key] {
			t.Errorf("%s is missing; the group's chain was severed", key)
		}
	}
	if got["octo-org/web/.github/workflows/ci.yml"] {
		t.Error("a workflow from another group was drawn")
	}
}

// The subtle half of exclusion, and the reason it is evaluated over sources rather than over every
// node: `octo-org/shared` is in the excluded group *and* is reached by the group that was kept.
// Removing it would answer "what does this group depend on" by deleting part of the answer.
func TestExcludingAGroupDoesNotSeverADependencyAnotherGroupReaches(t *testing.T) {
	s := sharedGroupingFixture(t)

	got := drawn(t, s, FullGraphOptions{GroupSources: []string{"octo-org/web"}})

	if !got["octo-org/shared"] {
		t.Error("a dependency the kept group reaches was removed for belonging to the excluded one")
	}
	if !got["octo-org/web/.github/workflows/ci.yml"] {
		t.Error("the kept group's own workflow is missing")
	}
	if got["octo-org/app/.github/workflows/ci.yml"] {
		t.Error("the excluded group's own workflow was drawn")
	}
}

// Declaring a filter that matches nothing shows nothing, the way an undeclared scope already does.
// Falling through to the whole corpus would answer a question about one group with every other.
func TestAFilterMatchingNoRepositoryDrawsNothing(t *testing.T) {
	s := sharedGroupingFixture(t)

	if got := drawn(t, s, FullGraphOptions{GroupSources: []string{}, GroupFiltered: true}); len(got) != 0 {
		t.Errorf("a filter matching nothing drew %d nodes", len(got))
	}
}

// Grouping and filtering are different questions and must compose: a graph filtered to one group
// still labels its nodes so the browser can fold them.
func TestAFilteredGraphStillLabelsWhatItDraws(t *testing.T) {
	s := sharedGroupingFixture(t)

	g, err := s.FullGraph(FullGraphOptions{
		IncludeSteps:  true,
		GroupProperty: "business_id",
		GroupSources:  []string{"octo-org/app", "octo-org/shared"}})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Key == "octo-org/app/.github/workflows/ci.yml" && n.Grouping != "3" {
			t.Errorf("Grouping = %q, want the value the organization set", n.Grouping)
		}
		if n.Key == "actions/checkout" && n.Grouping != "" {
			t.Errorf("an external node was labeled %q; it belongs to nobody", n.Grouping)
		}
	}
}
