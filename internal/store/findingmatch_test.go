package store

import (
	"strings"
	"testing"
)

// seedFindings records findings against one workflow and links them, which is how a rule reaches
// a node at all: `abom_finding_node` is populated by LinkFindingsToNodes, never by the insert.
//
// The lines are deliberately clear of the fixture's steps. A finding attaches to the most specific
// node at its location, so seeding at line 10 silently lands on `step:tool:0` and the test then
// asserts against a node it did not mean.
func seedFindings(t *testing.T, s *Store, repoID int64, path string, rules ...Finding) {
	t.Helper()
	for i := range rules {
		rules[i].RepositoryID = repoID
		rules[i].FilePath = path
		rules[i].StartLine = 100 + i
	}
	if _, err := s.AddFindings(rules); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
}

func matchFixture(t *testing.T) *Store {
	t.Helper()
	s := subgraphFixture(t)
	var appID, toolID int64
	for _, r := range []struct {
		name string
		into *int64
	}{{"octo-org/app", &appID}, {"octo-org/tool", &toolID}} {
		if err := s.DB().QueryRow(
			`SELECT row_id FROM repository WHERE repository_full_name = ?`, r.name).
			Scan(r.into); err != nil {
			t.Fatalf("finding %s: %v", r.name, err)
		}
	}
	seedFindings(t, s, appID, ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", Provider: "CodeQL", Severity: "warning"},
		Finding{RuleID: "abom/secrets-inherit", Provider: "CodeQL", Severity: "error"},
	)
	seedFindings(t, s, toolID, ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", Provider: "CodeQL", Severity: "warning"},
	)
	return s
}

func matchedKeys(t *testing.T, s *Store, sel FindingSelector) []string {
	t.Helper()
	got, err := s.NodesMatching(sel)
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	keys := make([]string, 0, len(got.Nodes))
	for _, n := range got.Nodes {
		keys = append(keys, n.Key)
	}
	return keys
}

// A finding attached to two nodes is one finding.
func TestAFindingOnTwoNodesIsCountedOnceInTheTotal(t *testing.T) {
	s := matchFixture(t)
	var toolID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, "octo-org/tool").
		Scan(&toolID); err != nil {
		t.Fatalf("finding octo-org/tool: %v", err)
	}

	// Two steps naming a dependency on the same line attach one finding to both sources.
	w := NewWriter(s, 0)
	// Repeated because a writer resolves only the keys it has been given, and the edge below
	// names a node the fixture added through a different one.
	if err := w.AddNode(Node{
		Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
		IsExternal: true,
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddNode(Node{
		Key: "step:tool:shared", Kind: "step", RepositoryID: toolID,
		Path: ".github/workflows/ci.yml", ContainerKey: "octo-org/tool/.github/workflows/ci.yml",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.AddEdge(Edge{
		Key: "e-tool-shared", SrcKey: "step:tool:shared", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefValue: "v4", RepositoryID: toolID,
		FilePath: ".github/workflows/ci.yml", StartLine: 10,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		RuleID: "abom/shared-line", RepositoryID: toolID,
		FilePath: ".github/workflows/ci.yml", StartLine: 10,
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := s.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}

	var links, distinct int
	if err := s.DB().QueryRow(`SELECT COUNT(*), COUNT(DISTINCT finding_row_id)
		FROM abom_finding_node fn JOIN abom_finding f ON f.row_id = fn.finding_row_id
		WHERE f.rule_id = 'abom/shared-line'`).Scan(&links, &distinct); err != nil {
		t.Fatalf("counting links: %v", err)
	}
	if links < 2 {
		t.Fatalf("the finding linked to %d node(s); the fixture must put it on two", links)
	}

	got, err := s.NodesMatching(FindingSelector{Rules: []string{"abom/shared-line"}})
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	if got.Findings != distinct {
		t.Errorf("Findings = %d, want %d: a finding on %d nodes is still one finding",
			got.Findings, distinct, links)
	}
}

func TestMatchingSelectsTheNodesARuleWasFoundOn(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{Rules: []string{"abom/secrets-inherit"}})

	if len(got) != 1 || got[0] != "octo-org/app/.github/workflows/ci.yml" {
		t.Errorf("matched %v, want only the workflow that rule was found on", got)
	}
}

// A rule id is a CodeQL query id, and query ids are hierarchical, so a pack or a family has to be
// nameable without listing every query in it.
func TestARulePrefixMatchesEveryRuleBeneathIt(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{Rules: []string{"actions/*"}})

	if len(got) != 2 {
		t.Errorf("matched %v, want both workflows carrying an actions/ rule", got)
	}
	for _, key := range got {
		if !strings.Contains(key, "/.github/workflows/ci.yml") {
			t.Errorf("%s does not carry an actions/ finding", key)
		}
	}
}

// "A or B, but not C" describes the *node*: an action or workflow that has A and does not have C.
//
// The alternative reading — drop C's findings and keep the node if A survives — is a different
// question and would make exclusion a no-op wherever a node carries both. Named here because the
// two are indistinguishable on any corpus where no node carries an excluded rule alongside an
// included one, so a test that did not say which it meant would pass either way.
func TestExclusionRemovesANodeThatCarriesTheExcludedRuleAtAll(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{
		Rules:   []string{"actions/unpinned-tag"},
		Exclude: []string{"abom/secrets-inherit"},
	})

	if len(got) != 1 || got[0] != "octo-org/tool/.github/workflows/ci.yml" {
		t.Errorf("matched %v, want only the workflow that does not also carry the excluded rule", got)
	}
}

// Provider is recorded verbatim and NULL means nobody recorded one. Selecting a provider must not
// sweep up the unattributed, which would invent the provenance the column exists to hold.
func TestSelectingAProviderNeverMatchesTheUnattributed(t *testing.T) {
	s := matchFixture(t)
	var sharedID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM repository WHERE repository_full_name = ?`, "octo-org/shared").
		Scan(&sharedID); err != nil {
		t.Fatalf("finding octo-org/shared: %v", err)
	}
	seedFindings(t, s, sharedID, ".github/workflows/ci.yml",
		Finding{RuleID: "actions/unpinned-tag", Severity: "warning"})

	byProvider := matchedKeys(t, s, FindingSelector{Provider: "CodeQL"})

	for _, key := range byProvider {
		if strings.HasPrefix(key, "octo-org/shared/") {
			t.Error("a finding nobody attributed was matched as CodeQL's")
		}
	}
	if len(byProvider) == 0 {
		t.Fatal("no node matched the provider, so the exclusion proves nothing")
	}
}

func TestSelectingASeverityKeepsOnlyThatSeverity(t *testing.T) {
	s := matchFixture(t)

	got := matchedKeys(t, s, FindingSelector{Severity: "error"})

	if len(got) != 1 || got[0] != "octo-org/app/.github/workflows/ci.yml" {
		t.Errorf("matched %v, want only the node carrying an error", got)
	}
}

// A selector that asks nothing must not answer "everything". A gate whose policy failed to parse
// would otherwise fail every pull request rather than none.
func TestAnEmptySelectorMatchesNothing(t *testing.T) {
	s := matchFixture(t)

	if got := matchedKeys(t, s, FindingSelector{}); len(got) != 0 {
		t.Errorf("an empty selector matched %d nodes; it must match none", len(got))
	}
}

// The answer has to explain itself: a gate that says "3 findings" without naming the rules gives
// nobody a way to disagree with it.
func TestAMatchNamesTheRulesItMatchedOn(t *testing.T) {
	s := matchFixture(t)

	got, err := s.NodesMatching(FindingSelector{Rules: []string{"*"}})
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	var app *MatchedNode
	for i := range got.Nodes {
		if got.Nodes[i].Key == "octo-org/app/.github/workflows/ci.yml" {
			app = &got.Nodes[i]
		}
	}
	if app == nil {
		t.Fatal("the workflow with two findings was not matched")
	}
	if app.Findings != 2 {
		t.Errorf("Findings = %d, want both", app.Findings)
	}
	if len(app.Rules) != 2 || app.Rules[0] != "abom/secrets-inherit" {
		t.Errorf("Rules = %v, want both rules in a stable order", app.Rules)
	}
}
