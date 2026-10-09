package store

import (
	"slices"
	"strings"
	"testing"
)

func TestUpstreamCINeverBecomesAConsumersRelationships(t *testing.T) {
	s := startingViewsFixture(t)
	if _, err := s.db.Exec(`UPDATE repository SET origin = 'upstream'
		WHERE repository_full_name = 'acme/tool';
		DELETE FROM abom_frontier WHERE node_key = 'acme/tool' AND depth = 0`); err != nil {
		t.Fatal(err)
	}
	page, err := s.RelationshipsPage(RelationshipsQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 {
		t.Errorf("relationships = %d, want only the consumer's action call, not upstream CI: %+v",
			page.Total, page.Items)
	}
	g, err := s.FullGraph(FullGraphOptions{IncludeSteps: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range g.Edges {
		if e.Target == "actions/checkout" || e.Target == "actions/cache" {
			t.Errorf("upstream CI draws %s -> %s", e.Source, e.Target)
		}
		for _, n := range g.Nodes {
			if strings.HasPrefix(n.Key, "acme/tool/.github/workflows/ci.yml") {
				t.Errorf("upstream CI node is still drawn: %s", n.Key)
			}
		}
		workflows, err := s.WorkflowsPage(WorkflowsQuery{Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range workflows.Items {
			if w.Repository == "acme/tool" {
				t.Errorf("upstream CI is still listed: %s", w.NodeKey)
			}
		}
	}
}

func TestABOMContinuesOnlyThroughTheReachedComponent(t *testing.T) {
	edges := []bomEdge{
		{source: "acme/app/ci.yml", sourceRepository: "acme/app", target: "vendor/action"},
		{source: "vendor/action", sourceRepository: "vendor/action", target: "vendor/nested/sub"},
		{source: "vendor/nested/sub", sourceRepository: "vendor/nested", target: "actions/cache"},
		{source: "vendor/nested/other", sourceRepository: "vendor/nested", target: "unrelated/action"},
		{source: "vendor/action/test.yml", sourceRepository: "vendor/action", target: "ci/only"},
		{source: "actions/cache", sourceRepository: "actions/cache", target: "vendor/action"},
	}
	got := walkOutward(edges, []string{"acme/app"})
	want := map[string]int{
		"acme/app/ci.yml": 0, "vendor/action": 1, "vendor/nested/sub": 2, "actions/cache": 3,
	}
	if len(got) != len(want) {
		t.Errorf("components = %v, want %v", got, want)
	}
	for key, depth := range want {
		if actual, ok := got[key]; !ok || actual != depth {
			t.Errorf("%s depth = %d (present %v), want %d", key, actual, ok, depth)
		}
	}
}

func TestSupplyChainMembershipUsesStartingDeclarationsNotTheLastWriter(t *testing.T) {
	s := startingViewsFixture(t)
	declaredAt(t, s, "acme/app", "history", ".github/workflows/ci.yml", 9, "vendor/history", "v9")
	g, err := s.FullGraph(FullGraphOptions{Scope: ScopeSupplyChain})
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, n := range g.Nodes {
		keys = append(keys, n.Key)
	}
	if slices.Contains(keys, "vendor/history") {
		t.Error("a target named only by a history commit entered the supply chain")
	}
	if !slices.Contains(keys, "acme/tool@v1") {
		t.Error("the starting commit's target is missing from the supply chain")
	}
}

func TestEveryScopedReaderSharesStartingMembershipAndInvalidatesIt(t *testing.T) {
	readers := map[string]func(*Store) error{
		"graph": func(s *Store) error {
			_, err := s.FullGraph(FullGraphOptions{Scope: ScopeSupplyChain})
			return err
		},
		"inventory": func(s *Store) error {
			_, err := s.ActionsInventoryPage(InventoryQuery{Scope: ScopeSupplyChain})
			return err
		},
		"repositories": func(s *Store) error {
			_, err := s.RepositoriesPage(RepositoriesQuery{Scope: ScopeSupplyChain})
			return err
		},
		"workflows": func(s *Store) error {
			_, err := s.WorkflowsPage(WorkflowsQuery{Scope: ScopeSupplyChain})
			return err
		},
		"jobs": func(s *Store) error {
			_, err := s.JobsPage(JobsQuery{Scope: ScopeSupplyChain})
			return err
		},
		"relationships": func(s *Store) error {
			_, err := s.RelationshipsPage(RelationshipsQuery{Scope: ScopeSupplyChain})
			return err
		},
		"node findings": func(s *Store) error {
			_, err := s.InventoryFindingsPage(FindingReviewQuery{
				InventoryQuery: InventoryQuery{Scope: ScopeSupplyChain}})
			return err
		},
		"repository findings": func(s *Store) error {
			_, err := s.InventoryFindingsPage(FindingReviewQuery{
				InventoryQuery: InventoryQuery{Scope: ScopeSupplyChain},
				AssetKind:      "repository", AssetKey: "acme/service"})
			return err
		},
	}
	for name, read := range readers {
		t.Run(name, func(t *testing.T) {
			s := protectedFixture(t)
			for _, commit := range []string{"history", "m1"} {
				declaredAt(t, s, "acme/service", commit, ".github/workflows/ci.yml", 10,
					"vendor/changed", "v1")
				state, err := s.connectionState()
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := read(s); err != nil {
						t.Fatal(err)
					}
				}
				after, err := s.connectionState()
				if err != nil || after != state {
					t.Fatalf("a scoped read changed database state: %v", err)
				}
				var target string
				if err := s.db.QueryRow(`SELECT n.node_key FROM ` + startingNodeTable("") + ` e
					JOIN abom_node_live n ON n.row_id = e.dst_node_row_id`).Scan(&target); err != nil {
					t.Fatal(err)
				}
				want := "actions/checkout"
				if commit == "m1" {
					want = "vendor/changed"
				}
				if target != want {
					t.Errorf("starting membership = %s after %s, want %s", target, commit, want)
				}
			}
			if got := s.liveAnswerLoads(); got != 2 {
				t.Errorf("loaded starting membership %d times across two states, want 2", got)
			}
		})
	}
}
