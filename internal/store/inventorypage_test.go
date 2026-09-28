package store

import "testing"

// inventoryCorpus seeds n external actions with descending reach, so a page boundary can be
// crossed and the rows outside it can be told apart from the rows inside.
func inventoryCorpus(t *testing.T, s *Store, n int) {
	t.Helper()
	for i := range n {
		seedDependency(t, s, "octo-org/app", "vendor/action-"+string(rune('a'+i)), "v1", i+1)
	}
	// Reach descends with the index, so the seeding order and the default order agree and a
	// test asserting on a page is asserting on something predictable.
	if _, err := s.DB().Exec(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		SELECT row_id, 100 - row_id, 1, 1, 1 FROM abom_node WHERE is_external = 1`); err != nil {
		t.Fatalf("seeding blast radius: %v", err)
	}
}

// A page that does not say what it left out reads as the whole answer, and the grid above it
// pages within the truncation rather than through the match.
func TestInventoryPageReportsHowManyRowsItLeftOut(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 6)

	page, err := s.ActionsInventoryPage(InventoryQuery{Limit: 2})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("page holds %d rows, want the 2 asked for", len(page.Items))
	}
	if page.Total <= len(page.Items) {
		t.Errorf("total = %d, want the whole match rather than the page", page.Total)
	}
}

// Sorting a truncated page answers a different question from sorting the full match.
func TestInventorySortsTheWholeMatchRatherThanThePage(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 6)
	// The lowest-reaching row carries the most findings, so it cannot be on the first page by
	// reach and must be first once findings are what is being sorted on.
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	seedFindings(t, s, repoID, ".github/workflows/ci.yml",
		Finding{RuleID: "abom/a"}, Finding{RuleID: "abom/b"}, Finding{RuleID: "abom/c"})
	// Re-pointed rather than added: the helper links by file location, which lands on the
	// workflow, and the row that has to carry them here is the lowest-reaching action.
	if _, err := s.DB().Exec(`
		DELETE FROM abom_finding_node;
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id FROM abom_finding f, abom_node n
		WHERE n.node_key = 'vendor/action-f'`); err != nil {
		t.Fatalf("attributing findings: %v", err)
	}

	page, err := s.ActionsInventoryPage(InventoryQuery{Sort: "findings", Desc: true, Limit: 2})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if len(page.Items) == 0 || page.Items[0].NodeKey != "vendor/action-f" {
		t.Errorf("first row = %+v, want the row with the most findings across the whole match",
			page.Items)
	}
}

// Paging has to walk the match once. Repeating a row or skipping one is invisible on screen and
// makes every count a reader adds up wrong.
func TestInventoryPagingWalksTheMatchExactlyOnce(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 6)

	seen := map[string]int{}
	var total int
	for offset := 0; ; offset += 2 {
		page, err := s.ActionsInventoryPage(InventoryQuery{Offset: offset, Limit: 2})
		if err != nil {
			t.Fatalf("ActionsInventoryPage: %v", err)
		}
		total = page.Total
		if len(page.Items) == 0 {
			break
		}
		for _, it := range page.Items {
			seen[it.NodeKey]++
		}
	}
	if len(seen) != total {
		t.Errorf("walked %d distinct rows over a match of %d", len(seen), total)
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("row %q appeared %d times", key, n)
		}
	}
}

// A sort column is a column name, and a column name reaching SQL from a query string is an
// injection unless it is checked against what exists. Refused by name rather than ignored,
// because a silently ignored sort shows the reader a different order from the one they asked for.
func TestInventoryRefusesASortColumnItDoesNotHave(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)

	for _, sort := range []string{"node_key; DROP TABLE abom_node", "nonsense", "1"} {
		if _, err := s.ActionsInventoryPage(InventoryQuery{Sort: sort}); err == nil {
			t.Errorf("sorting by %q was accepted, want it refused by name", sort)
		}
	}
}

func TestInventoryNarrowsByKind(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)

	page, err := s.ActionsInventoryPage(InventoryQuery{Kinds: []string{"workflow"}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total == 0 {
		t.Fatal("no workflows matched, so the narrowing cannot be told from a broken query")
	}
	for _, it := range page.Items {
		if it.Kind != "workflow" {
			t.Errorf("row %q is a %s, which the kind filter excluded", it.NodeKey, it.Kind)
		}
	}
}

// Zero is not a floor anybody asked for: a minimum of nothing must not drop the rows whose reach
// has never been measured.
func TestInventoryMinReachOfZeroAsksNothing(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)

	asked, err := s.ActionsInventoryPage(InventoryQuery{MinReach: 0})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	unasked, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if asked.Total != unasked.Total {
		t.Errorf("minReach=0 matched %d rows against %d unfiltered", asked.Total, unasked.Total)
	}
}
