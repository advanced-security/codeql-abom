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

	// tool reaches checkout directly from two steps of one job; app reaches it through the reusable
	// workflow it calls, from one step.
	type reached struct{ hops, steps, jobs, workflows int }
	reach := map[string]map[string]reached{
		"actions/checkout": {"octo-org/tool": {1, 2, 1, 1}, "octo-org/app": {3, 1, 1, 1}},
		"octo-org/shared/.github/workflows/build.yml": {"octo-org/app": {1, 1, 1, 1}},
	}
	for node, repos := range reach {
		for repo, r := range repos {
			if _, err := s.DB().Exec(`
				INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops, steps, jobs, workflows)
				SELECT n.row_id, r.row_id, ?, ?, ?, ? FROM abom_node_live n, repository r
				WHERE n.node_key = ? AND r.repository_full_name = ?`,
				r.hops, r.steps, r.jobs, r.workflows, node, repo); err != nil {
				t.Fatalf("recording reach of %s from %s: %v", node, repo, err)
			}
		}
	}
	// The rollup is the breakdown's sums, as one walk would have written both. A fixture where
	// they disagree would test a state the builder cannot produce.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_steps, dependent_jobs,
			dependent_workflows, max_depth)
		SELECT nr.node_row_id, COUNT(*), SUM(nr.steps), SUM(nr.jobs), SUM(nr.workflows), 1
		  FROM abom_node_reach nr GROUP BY nr.node_row_id`); err != nil {
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
	if value(whole["actions/checkout"].DependentRepos) != 2 {
		t.Fatalf("unfiltered reports checkout reaching %d repositories, want 2",
			value(whole["actions/checkout"].DependentRepos))
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
	if value(got.DependentRepos) != 1 {
		t.Errorf("checkout reaches %d repositories under business_id:3, want 1 — the count "+
			"spans the organization rather than the grouping", value(got.DependentRepos))
	}
}

// Every level below the repository is counted as the repositories are: stored totals for the whole
// organization, recounted from the repositories in a grouping when one narrows the list. Groups are
// the values of the configured grouping property among them, and none are counted where no
// property is configured, which is not the same as none reached.
func TestTheActionsInventoryCountsEveryLevelAndEveryGroupWithinAGrouping(t *testing.T) {
	s := inventoryGroupingFixture(t)
	checkout := func(q InventoryQuery) ActionSummary {
		t.Helper()
		page, err := s.ActionsInventoryPage(q)
		if err != nil {
			t.Fatalf("ActionsInventoryPage: %v", err)
		}
		if page.GroupProperty != q.GroupProperty {
			t.Errorf("the page groups by %q, asked %q", page.GroupProperty, q.GroupProperty)
		}
		return byNodeKey(page.Items)["actions/checkout"]
	}
	type counted struct{ repos, steps, jobs, workflows, groups int }
	of := func(a ActionSummary) counted {
		return counted{value(a.DependentRepos), value(a.DependentSteps), value(a.DependentJobs),
			value(a.DependentWorkflows), value(a.DependentGroups)}
	}

	if got, want := of(checkout(InventoryQuery{GroupProperty: "business_id"})),
		(counted{2, 3, 2, 2, 2}); got != want {
		t.Errorf("across the organization checkout is %+v, want %+v", got, want)
	}
	if got, want := of(checkout(InventoryQuery{GroupProperty: "business_id",
		Grouping: pinningGroup("3").Scope})), (counted{1, 2, 1, 1, 1}); got != want {
		t.Errorf("within business_id:3 checkout is %+v, want tool's %+v", got, want)
	}
	if got := checkout(InventoryQuery{}).DependentGroups; got != nil {
		t.Errorf("with no grouping property checkout counts %d groups, want none counted", *got)
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
			parts[r.NodeKey] += value(r.DependentRepos)
		}
	}
	for key, want := range whole {
		if parts[key] != value(want.DependentRepos) {
			t.Errorf("%s: the groupings total %d against a whole of %d",
				key, parts[key], value(want.DependentRepos))
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

// A bounded inventory must refuse rollup reach with no grouping breakdown.
func TestABoundedInventoryRefusesWhenTheBreakdownHasNotBeenDerived(t *testing.T) {
	s := pinningGroupingFixture(t)

	// The rollup asserts reach that no breakdown row explains, which is exactly an upgraded corpus.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, dependent_steps, max_depth)
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

// value reads a count the inventory may not have measured, as -1 where it has not.
func value(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}
