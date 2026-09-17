package store

import (
	"errors"
	"testing"
)

// inventoryGroupingFixture adds the reach breakdown the graph builder would have written.
//
// The store cannot derive it — that walk lives in internal/graph, which imports this package — so
// the pairs are stated here. `internal/graph` holds the test that the walk and the rollup agree.
func inventoryGroupingFixture(t *testing.T) *Store {
	t.Helper()
	s := pinningGroupingFixture(t)

	// tool reaches checkout directly; app reaches it through the reusable workflow it calls.
	reach := map[string]map[string]int{
		"actions/checkout": {"octo-org/tool": 1, "octo-org/app": 3},
		"octo-org/shared/.github/workflows/build.yml": {"octo-org/app": 1},
	}
	for node, repos := range reach {
		for repo, hops := range repos {
			if _, err := s.DB().Exec(`
				INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops)
				SELECT n.row_id, r.row_id, ? FROM abom_node_live n, repository r
				WHERE n.node_key = ? AND r.repository_full_name = ?`,
				hops, node, repo); err != nil {
				t.Fatalf("recording reach of %s from %s: %v", node, repo, err)
			}
		}
	}
	// The rollup is the breakdown's cardinality, as one walk would have written both. A fixture
	// where they disagree would test a state the builder cannot produce.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT nr.node_row_id, COUNT(*), 0, 0, 1 FROM abom_node_reach nr
		GROUP BY nr.node_row_id`); err != nil {
		t.Fatalf("recording the rollup: %v", err)
	}
	return s
}

// The count is the point. A bounded inventory whose rows are narrowed but whose reach still spans
// the organization tells a reader their group depends on far more than it does.
func TestTheActionsInventoryRecountsReachWithinAGrouping(t *testing.T) {
	s := inventoryGroupingFixture(t)

	all, err := s.ActionsInventory("", 0, RepositoryScope{})
	if err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	}
	whole := byNodeKey(all)
	if whole["actions/checkout"].DependentRepos != 2 {
		t.Fatalf("unfiltered reports checkout reaching %d repositories, want 2",
			whole["actions/checkout"].DependentRepos)
	}

	three, err := s.ActionsInventory("", 0, pinningGroup("3").Scope)
	if err != nil {
		t.Fatalf("ActionsInventory(business_id:3): %v", err)
	}
	bounded := byNodeKey(three)
	got, ok := bounded["actions/checkout"]
	if !ok {
		t.Fatal("checkout is absent from business_id:3, which reaches it through tool")
	}
	if got.DependentRepos != 1 {
		t.Errorf("checkout reaches %d repositories under business_id:3, want 1 — the count "+
			"spans the organization rather than the grouping", got.DependentRepos)
	}
}

// A node nothing in the grouping reaches is not the grouping's dependency, so it is not its row.
func TestTheActionsInventoryDropsWhatTheGroupingDoesNotReach(t *testing.T) {
	s := inventoryGroupingFixture(t)

	three, err := s.ActionsInventory("", 0, pinningGroup("3").Scope)
	if err != nil {
		t.Fatalf("ActionsInventory: %v", err)
	}
	if _, present := byNodeKey(three)["octo-org/shared/.github/workflows/build.yml"]; present {
		t.Error("business_id:3 lists a reusable workflow only app reaches")
	}
}

// The groupings partition the estate, so reach counted within each accounts for the whole.
func TestReachWithinGroupingsAccountsForTheWhole(t *testing.T) {
	s := inventoryGroupingFixture(t)

	all, err := s.ActionsInventory("", 0, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	whole := byNodeKey(all)

	parts := map[string]int{}
	for _, group := range []string{"3", "7"} {
		rows, err := s.ActionsInventory("", 0, pinningGroup(group).Scope)
		if err != nil {
			t.Fatalf("ActionsInventory(%s): %v", group, err)
		}
		for _, r := range rows {
			parts[r.NodeKey] += r.DependentRepos
		}
	}
	for key, want := range whole {
		if parts[key] != want.DependentRepos {
			t.Errorf("%s: the groupings total %d against a whole of %d",
				key, parts[key], want.DependentRepos)
		}
	}
}

// The node picker answers the same question, so it narrows the same way.
func TestSearchNodesBoundedToAGrouping(t *testing.T) {
	s := inventoryGroupingFixture(t)

	all, err := s.SearchNodes("", 0, RepositoryScope{})
	if err != nil {
		t.Fatal(err)
	}
	three, err := s.SearchNodes("", 0, pinningGroup("3").Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(three) == 0 {
		t.Fatal("business_id:3 matched no node at all")
	}
	if len(three) >= len(all) {
		t.Errorf("business_id:3 returned %d of %d nodes; it narrowed nothing",
			len(three), len(all))
	}
	for _, n := range three {
		if n.Key == "octo-org/shared/.github/workflows/build.yml" {
			t.Error("business_id:3 lists a workflow only app reaches")
		}
	}
}

// A grouping nobody is in reaches nothing, rather than falling through to the organization.
func TestAnInventoryBoundedToAGroupingNobodyIsInIsEmpty(t *testing.T) {
	s := inventoryGroupingFixture(t)

	rows, err := s.ActionsInventory("", 0, pinningGroup("404").Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a grouping nobody is in listed %d actions", len(rows))
	}
	if rows == nil {
		t.Error("an empty inventory is nil, which serializes as null rather than []")
	}
}

// An upgrade finds the table empty, because reach is recomputed wholesale and the breakdown is
// only written by a rebuild. Answering "nothing" then would report a grouping as depending on
// nothing at all — an absent measurement presented as a measurement of zero.
//
// Measured on the crawled organization before this refused: the unbounded inventory returned 1,585
// actions and the bounded one returned 0, with 448 rollup rows still asserting reach.
func TestABoundedInventoryRefusesWhenTheBreakdownHasNotBeenDerived(t *testing.T) {
	s := pinningGroupingFixture(t)

	// The rollup asserts reach that no breakdown row explains, which is exactly an upgraded corpus.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT row_id, 2, 0, 0, 1 FROM abom_node_live WHERE node_key = 'actions/checkout'`,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ActionsInventory("", 0, RepositoryScope{}); err != nil {
		t.Errorf("the unbounded inventory should still answer: %v", err)
	}

	_, err := s.ActionsInventory("", 0, pinningGroup("3").Scope)
	if err == nil {
		t.Fatal("a bounded inventory answered from a breakdown that was never derived")
	}
	if !errors.Is(err, ErrReachNotDerived) {
		t.Errorf("the refusal does not name the cause: %v", err)
	}
}

func byNodeKey(rows []ActionSummary) map[string]ActionSummary {
	out := make(map[string]ActionSummary, len(rows))
	for _, r := range rows {
		out[r.NodeKey] = r
	}
	return out
}
