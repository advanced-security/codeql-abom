package store

import (
	"slices"
	"strings"
	"testing"
)

// tagFixture records findings of three rules in one run, each rule declaring the tags its SARIF
// declared, on two repositories so a node can carry one tagged rule and another untagged.
func tagFixture(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)
	ids := map[string]int64{}
	for _, name := range []string{"octo-org/app", "octo-org/tool"} {
		var id int64
		if err := s.DB().QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = ?`, name).
			Scan(&id); err != nil {
			t.Fatalf("finding %s: %v", name, err)
		}
		ids[name] = id
	}
	run, err := s.RecordQueryRun(QueryRun{QueryID: "fixture", Repository: "octo-org/app",
		AnalysisMode: AnalysisModeRegular})
	if err != nil {
		t.Fatal(err)
	}
	documented := func(rule, tags string) Finding {
		return Finding{RuleID: rule, QueryRunID: run, Provider: "CodeQL", Severity: "warning",
			RuleDocumentation: []byte(`{"id":"` + rule + `","tags":` + tags + `}`)}
	}
	seedFindings(t, s, ids["octo-org/app"], ".github/workflows/ci.yml",
		documented("actions/unpinned-tag", `["security","external/cwe/cwe-829"]`),
		documented("actions/code-injection/critical", `["security","external/cwe/cwe-094"]`),
	)
	seedFindings(t, s, ids["octo-org/tool"], ".github/workflows/ci.yml",
		documented("actions/missing-workflow-permissions", `null`),
	)
	return s
}

func taggedRules(t *testing.T, s *Store, sel FindingSelector) []string {
	t.Helper()
	got, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	var rules []string
	for _, n := range got.Nodes {
		rules = append(rules, n.Rules...)
	}
	slices.Sort(rules)
	return slices.Compact(rules)
}

func TestATagMatchesTheFindingsOfEveryRuleThatDeclaredIt(t *testing.T) {
	s := tagFixture(t)
	got := taggedRules(t, s, FindingSelector{Tags: []string{"external/cwe/cwe-829"}})
	if want := []string{"actions/unpinned-tag"}; !slices.Equal(got, want) {
		t.Fatalf("tag cwe-829 matched %v, want %v", got, want)
	}
	got = taggedRules(t, s, FindingSelector{Tags: []string{"security"}})
	if want := []string{"actions/code-injection/critical", "actions/unpinned-tag"}; !slices.Equal(got, want) {
		t.Fatalf("tag security matched %v, want %v", got, want)
	}
}

// A CWE report is wrong the moment cwe-82 answers for cwe-829, so a tag is never a prefix and
// never case-folded.
func TestATagMatchesExactlyAndNeverAsAPrefix(t *testing.T) {
	s := tagFixture(t)
	for _, tag := range []string{"external/cwe/cwe-82", "external/cwe/", "External/CWE/CWE-829", "external/cwe/*"} {
		if got := taggedRules(t, s, FindingSelector{Tags: []string{tag}}); len(got) != 0 {
			t.Errorf("tag %q matched %v, want nothing", tag, got)
		}
	}
}

func TestNotTagsRemovesANodeCarryingATaggedRuleAtAll(t *testing.T) {
	s := tagFixture(t)
	got := taggedRules(t, s, FindingSelector{Rules: []string{"actions/*"},
		NotTags: []string{"external/cwe/cwe-094"}})
	if want := []string{"actions/missing-workflow-permissions"}; !slices.Equal(got, want) {
		t.Fatalf("notTags matched %v, want only the node without the tagged rule %v", got, want)
	}
}

// Rule documentation recorded before the tag table existed carries the tags already; reading them
// back is how an existing corpus answers a tag without re-running a query.
func TestMigrateReadsTheTagsOfRuleDocumentationRecordedBeforeTheTable(t *testing.T) {
	s := tagFixture(t)
	if _, err := s.DB().Exec(`DELETE FROM abom_query_run_rule_tag`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	got := taggedRules(t, s, FindingSelector{Tags: []string{"external/cwe/cwe-829"}})
	if want := []string{"actions/unpinned-tag"}; !slices.Equal(got, want) {
		t.Fatalf("after migrating, tag cwe-829 matched %v, want %v", got, want)
	}
}

func TestSQLiteTagIsLookedUpByRunAndRuleRatherThanScanned(t *testing.T) {
	s := newTestStore(t)
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	query := buildFindingMatchQuery(FindingSelector{Tags: []string{"security"}})
	plan := strings.Join(explainPlan(t, s, `SELECT f.row_id FROM `+query.from+
		` WHERE `+strings.Join(query.where, " AND "), query.args...), "\n")
	if strings.Contains(plan, "SCAN t") {
		t.Fatalf("a tag scans the tag table:\n%s", plan)
	}
}

// A tag on offer counts the findings declaring it, and the findings declaring none are counted
// beside the list rather than left to read as covered.
func TestTheTagsOnOfferCountFindingsAndTheUntagged(t *testing.T) {
	s := tagFixture(t)
	set, err := s.FilterValues("tags")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, v := range set.Values {
		got[v.Value] = v.Count
	}
	want := map[string]int{"security": 2, "external/cwe/cwe-829": 1, "external/cwe/cwe-094": 1}
	if len(got) != len(want) || got["security"] != 2 || got["external/cwe/cwe-829"] != 1 ||
		got["external/cwe/cwe-094"] != 1 {
		t.Fatalf("tags on offer = %v, want %v", got, want)
	}
	if set.Uncovered != 1 {
		t.Fatalf("uncovered = %d, want the 1 finding whose rule declared no tag", set.Uncovered)
	}
}

// A run over repositories narrowed by a tag is a findings question, never a bare repository list.
func TestATagNarrowsTheRepositoriesARunCovers(t *testing.T) {
	for _, sel := range []FindingSelector{
		{Repositories: []string{"octo-org/app"}, Tags: []string{"external/cwe/cwe-829"}},
		{Repositories: []string{"octo-org/app"}, NotTags: []string{"external/cwe/cwe-829"}},
	} {
		if sel.namesOnlyRepositories() {
			t.Errorf("%+v reads as a bare repository list, so the tag is dropped", sel)
		}
	}
}

// declareTags records an evaluation whose SARIF documented `rule` with `tags`, and attributes that
// rule's findings to it.
func declareTags(t *testing.T, s *Store, rule, tags string) {
	t.Helper()
	run, err := s.RecordQueryRun(QueryRun{QueryID: rule, Repository: "octo-org/app",
		AnalysisMode: AnalysisModeRegular})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE abom_finding SET query_run_id = ? WHERE rule_id = ?`, run, rule); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO abom_query_run_rule (query_run_id, rule_id, documentation_json)
		VALUES (?, ?, ?)`, run, rule, `{"tags":`+tags+`}`); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillRuleTags(); err != nil {
		t.Fatal(err)
	}
}

// The listing and the finding review answer a tag as the set path does.
func TestTheInventoryAndTheReviewMatchATagAsTheSetPathDoes(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	findOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", "codeql")
	findOn(t, s, "vendor/action-b", "actions/code-injection", "error", "codeql")
	declareTags(t, s, "actions/unpinned-tag", `["security","external/cwe/cwe-829"]`)
	declareTags(t, s, "actions/code-injection", `["security","external/cwe/cwe-094"]`)

	page, err := s.ActionsInventoryPage(InventoryQuery{Tags: []string{"external/cwe/cwe-829"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].NodeKey != "vendor/action-a" {
		t.Fatalf("tags listed %+v, want only vendor/action-a", page.Items)
	}
	page, err = s.ActionsInventoryPage(InventoryQuery{Flags: []string{"findings"},
		NotTags: []string{"external/cwe/cwe-829"}})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].NodeKey != "vendor/action-b" {
		t.Fatalf("notTags listed %+v, want only vendor/action-b", page.Items)
	}
	review, err := s.InventoryFindingsPage(FindingReviewQuery{InventoryQuery: InventoryQuery{
		Tags: []string{"external/cwe/cwe-094"}}})
	if err != nil {
		t.Fatal(err)
	}
	if review.Total != 1 || review.Items[0].RuleID != "actions/code-injection" {
		t.Fatalf("the review matched %+v, want the one code-injection finding", review.Items)
	}
}
