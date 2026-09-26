package store

import "testing"

// An answer narrowed by a provider says how many matches it could not speak for, the way a source
// answer does. On the crawled organization one provider is on offer and 10,676 of 12,317 findings
// recorded none, so filtering by the only value answers for an eighth of the corpus in silence.
func TestAProviderAnswerCountsTheMatchesThatRecordedNoProvider(t *testing.T) {
	s := matchFixture(t)
	var sharedID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, "octo-org/shared").
		Scan(&sharedID); err != nil {
		t.Fatal(err)
	}
	seedFindings(t, s, sharedID, ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", Severity: "warning"})

	asked, err := s.NodesMatching(FindingSelector{
		Rules: []string{"actions/unpinned-tag"}, Provider: "CodeQL"})
	if err != nil {
		t.Fatal(err)
	}
	if asked.Unprovided != 1 {
		t.Errorf("Unprovided = %d, want the one unpinned finding nobody attributed", asked.Unprovided)
	}

	// Only when a provider is asked, which is the only time they are left out.
	unasked, err := s.NodesMatching(FindingSelector{Rules: []string{"actions/unpinned-tag"}})
	if err != nil {
		t.Fatal(err)
	}
	if unasked.Unprovided != 0 {
		t.Errorf("Unprovided = %d with no provider asked, want 0", unasked.Unprovided)
	}
}

// The dependencies listing says the same, counted in rows rather than findings.
func TestAProviderListingCountsTheRowsWhoseFindingsRecordedNoProvider(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "CodeQL")
	findOn(t, s, "vendor/action-b", "actions/unpinned-tag", "warning", "")

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Rules: []string{"actions/unpinned-tag"}, Provider: "CodeQL"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Unprovided != 1 {
		t.Errorf("total %d with %d unprovided, want 1 and the row nobody attributed counted beside it",
			page.Total, page.Unprovided)
	}
}
