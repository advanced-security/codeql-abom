package store

import (
	"slices"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

func score(v float64) *float64 { return &v }

// scoredOn records a finding on a node with the score its rule declared, nil meaning none.
func scoredOn(t *testing.T, s *Store, nodeKey, rule, level string, security *float64) {
	t.Helper()
	var nodeID int64
	if err := s.DB().QueryRow(
		`SELECT row_id FROM abom_node WHERE node_key = ?`, nodeKey).Scan(&nodeID); err != nil {
		t.Fatalf("resolving %s: %v", nodeKey, err)
	}
	repoID, err := s.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	res, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, severity, security_severity,
		                          provider, fingerprint)
		VALUES (?, ?, ?, ?, 'CodeQL', ?)`,
		rule, repoID, level, security, nodeKey+rule+level)
	if err != nil {
		t.Fatalf("recording a finding: %v", err)
	}
	findingID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	if _, err := s.DB().Exec(
		`INSERT INTO abom_finding_node (finding_row_id, node_row_id) VALUES (?, ?)`,
		findingID, nodeID); err != nil {
		t.Fatalf("attaching the finding to %s: %v", nodeKey, err)
	}
}

// SQL bands a score exactly as the language does, including a stored zero, which a finding
// ingested before absence was written as NULL still carries and which is never a score of nothing.
func TestTheStoreBandsAScoreExactlyAsTheLanguageDoes(t *testing.T) {
	s := newTestStore(t)
	for _, v := range []*float64{nil, score(0), score(0.1), score(3.9), score(4), score(6.9),
		score(7), score(8.9), score(9), score(10)} {
		var got string
		if err := s.DB().QueryRow(`SELECT `+bandSQL("x")+` FROM (SELECT ? AS x)`, v).
			Scan(&got); err != nil {
			t.Fatalf("banding %v: %v", v, err)
		}
		if want := viewspec.BandOf(v); got != want {
			t.Errorf("SQL banded %v as %q, the language as %q", v, got, want)
		}
	}
}

func bandedInventory(t *testing.T) *Store {
	t.Helper()
	s := advisoryStore(t)
	inventoryCorpus(t, s, 3)
	scoredOn(t, s, "vendor/action-a", "actions/code-injection/critical", "error", score(9.3))
	scoredOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", score(5.0))
	scoredOn(t, s, "vendor/action-b", "actions/unpinned-tag", "warning", score(5.0))
	scoredOn(t, s, "vendor/action-c", "actions/unnecessary-use-of-advanced-config",
		"recommendation", nil)
	return s
}

func listed(t *testing.T, s *Store, q InventoryQuery) []string {
	t.Helper()
	page, err := s.ActionsInventoryPage(q)
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	keys := []string{}
	for _, it := range page.Items {
		keys = append(keys, it.NodeKey)
	}
	slices.Sort(keys)
	return keys
}

// `securitySeverity` keeps what carries a finding in one of the bands asked for. `unknown` is a
// band a reader can ask for, and it is the rule that declared no score rather than the lowest one.
func TestInventorySecuritySeverityKeepsNodesCarryingAFindingInThatBand(t *testing.T) {
	s := bandedInventory(t)
	for _, c := range []struct {
		bands []string
		want  []string
	}{
		{[]string{viewspec.BandCritical}, []string{"vendor/action-a"}},
		{[]string{viewspec.BandMedium}, []string{"vendor/action-a", "vendor/action-b"}},
		{[]string{viewspec.BandUnknown}, []string{"vendor/action-c"}},
		{[]string{viewspec.BandLow}, []string{}},
		{[]string{viewspec.BandCritical, viewspec.BandUnknown},
			[]string{"vendor/action-a", "vendor/action-c"}},
	} {
		if got := listed(t, s, InventoryQuery{SecuritySeverities: c.bands}); !slices.Equal(got, c.want) {
			t.Errorf("securitySeverity=%v listed %v, want %v", c.bands, got, c.want)
		}
	}
}

// A band beside a rule names one finding that is both, as every other finding term does: a node
// with a critical injection and a medium pinning warning is not a critical pinning warning.
func TestABandAndARuleDescribeOneFindingOnEverySurface(t *testing.T) {
	s := bandedInventory(t)
	rules := []string{"actions/unpinned-tag"}
	bands := []string{viewspec.BandCritical}
	if got := listed(t, s, InventoryQuery{Rules: rules, SecuritySeverities: bands}); len(got) != 0 {
		t.Errorf("the listing matched %v, but no one finding is both", got)
	}
	match, err := s.NodesMatching(FindingSelector{Rules: rules, SecuritySeverities: bands})
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	if len(match.Nodes) != 0 || match.Findings != 0 {
		t.Errorf("the set path matched %d nodes and %d findings, want none", len(match.Nodes),
			match.Findings)
	}

	match, err = s.NodesMatching(FindingSelector{SecuritySeverities: bands})
	if err != nil {
		t.Fatalf("NodesMatching: %v", err)
	}
	if len(match.Nodes) != 1 || match.Nodes[0].Key != "vendor/action-a" || match.Findings != 1 {
		t.Errorf("securitySeverity=critical on the set path = %+v, want the one critical finding",
			match)
	}
}

// A policy is the set path, so a rule about critical findings is one a policy can say.
func TestAPolicyCarriesTheBandItWasWrittenWith(t *testing.T) {
	spec, err := viewspec.Parse("securitySeverity=critical,high")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sel := SelectorFor(spec)
	if !slices.Equal(sel.SecuritySeverities, []string{"critical", "high"}) {
		t.Errorf("SelectorFor dropped the band: %v", sel.SecuritySeverities)
	}
	if !sel.asked() {
		t.Error("a selector naming only a band reads as asking nothing, which matches nothing")
	}
}

// A findings count says at what severity, and the bands sum to the count: 36 critical findings
// counted in the same column as 11,998 medium ones is a blast radius with no blast.
func TestEveryListedFindingsCountSaysAtWhatSeverity(t *testing.T) {
	s := bandedInventory(t)
	page, err := s.ActionsInventoryPage(InventoryQuery{})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	want := map[string]SeverityCounts{
		"vendor/action-a": {Critical: 1, Medium: 1},
		"vendor/action-b": {Medium: 1},
		"vendor/action-c": {Unknown: 1},
	}
	for _, it := range page.Items {
		if it.Severity != want[it.NodeKey] {
			t.Errorf("%s carries %+v, want %+v", it.NodeKey, it.Severity, want[it.NodeKey])
		}
		if it.Severity.total() != it.Findings {
			t.Errorf("%s: the bands sum to %d of %d findings", it.NodeKey, it.Severity.total(),
				it.Findings)
		}
	}
}

// A node's rules say at what severity each reported, the most severe first whatever the counts.
func TestANodesRulesSayAtWhatSeverityMostSevereFirst(t *testing.T) {
	s := bandedInventory(t)
	scoredOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", score(5.0))
	d, err := s.NodeDetailOf("vendor/action-a")
	if err != nil {
		t.Fatalf("NodeDetailOf: %v", err)
	}
	if len(d.Rules) != 2 {
		t.Fatalf("rules %+v, want two", d.Rules)
	}
	if d.Rules[0].RuleID != "actions/code-injection/critical" ||
		d.Rules[0].Severity != (SeverityCounts{Critical: 1}) {
		t.Errorf("first rule %+v, want the one critical finding ahead of two medium ones",
			d.Rules[0])
	}
	if d.Rules[1].Severity != (SeverityCounts{Medium: 2}) {
		t.Errorf("second rule %+v, want its two medium findings", d.Rules[1])
	}

	repo, err := s.RepositoryDetailOf("octo-org/app")
	if err != nil {
		t.Fatalf("RepositoryDetailOf: %v", err)
	}
	if first := repo.FindingsByRule[0]; first.RuleID != "actions/code-injection/critical" {
		t.Errorf("the repository lists %s first, want the critical rule ahead of commoner ones",
			first.RuleID)
	}
}

// Severity outranks reach, and reach outranks nothing: a critical finding nothing reaches sorts
// after every finding something does, because unreached is what matters most about it.
func TestSortingBySeverityPutsReachedSevereFirstAndUnreachedAfter(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 6)
	scoredOn(t, s, "vendor/action-a", "actions/unpinned-tag", "warning", score(5.0))
	scoredOn(t, s, "vendor/action-b", "actions/untrusted-checkout/critical", "error", score(9.3))
	scoredOn(t, s, "vendor/action-c", "actions/code-injection/critical", "error", score(9.0))
	scoredOn(t, s, "vendor/action-e", "actions/code-injection/critical", "error", score(9.0))
	scoredOn(t, s, "vendor/action-f", "actions/unpinned-tag", "warning", score(5.0))
	// a reaches the most, b fewer, c nothing; d reaches more than any and carries no finding; e is
	// critical like b and reaches more; f reaches nothing, as c does, at a lower band.
	for key, reach := range map[string]int{
		"vendor/action-a": 50, "vendor/action-b": 3, "vendor/action-c": 0, "vendor/action-d": 90,
		"vendor/action-e": 20, "vendor/action-f": 0,
	} {
		if _, err := s.DB().Exec(`UPDATE abom_blast_radius SET dependent_repos = ?
			WHERE node_row_id = (SELECT row_id FROM abom_node WHERE node_key = ?)`,
			reach, key); err != nil {
			t.Fatal(err)
		}
	}
	// Most workflows have no reach row at all, and unmeasured must still order as reaching nothing
	// rather than landing wherever NULL sorts: on the crawled organization the one critical
	// workflow sorted after every medium one that had a row.
	if _, err := s.DB().Exec(`DELETE FROM abom_blast_radius
		WHERE node_row_id = (SELECT row_id FROM abom_node WHERE node_key = 'vendor/action-c')`); err != nil {
		t.Fatal(err)
	}
	page, err := s.ActionsInventoryPage(InventoryQuery{Sort: "severity", Desc: true})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.NodeKey)
	}
	want := []string{"vendor/action-e", "vendor/action-b", "vendor/action-a", "vendor/action-c",
		"vendor/action-f", "vendor/action-d"}
	if !slices.Equal(got, want) {
		t.Errorf("ordered %v, want %v", got, want)
	}
}

// Within a grouping reach is a counted expression with its own arguments, and ordering by it
// binds them again.
func TestSortingBySeverityWithinAGroupingOrdersByItsReach(t *testing.T) {
	s := inventoryGroupingFixture(t)
	scoredOn(t, s, "actions/checkout", "actions/unpinned-tag", "warning", score(5.0))
	scoredOn(t, s, "octo-org/shared/.github/workflows/build.yml",
		"actions/code-injection/critical", "error", score(9.0))

	page, err := s.ActionsInventoryPage(InventoryQuery{
		Sort: "severity", Desc: true, Grouping: pinningGroup("7").Scope,
		Kinds: []string{"action", "reusable_workflow"},
	})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	got := []string{}
	for _, it := range page.Items {
		got = append(got, it.NodeKey)
	}
	want := []string{"octo-org/shared/.github/workflows/build.yml", "actions/checkout"}
	if !slices.Equal(got, want) {
		t.Errorf("ordered %v, want the critical reusable workflow ahead of the medium action", got)
	}
}

// A result a run reported says what band its rule's score falls in, held or stored.
func TestARunsResultSaysItsBand(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAnalysisRun(heldRequest(s))
	if err != nil {
		t.Fatalf("CreateAnalysisRun: %v", err)
	}
	if err := s.RecordHeldMemberResult(id, heldAt(), MemberResult{
		Status: AnalysisSucceeded, Results: 2, Counted: true, QueryRunID: heldEvaluation(t, s),
		ByQuery: map[string]int{"actions/unpinned-tag": 1, "actions/other": 1},
	}, twoHeld()); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}
	listed, err := s.RunFindings(id, "octo/app", "", 100)
	if err != nil {
		t.Fatalf("RunFindings: %v", err)
	}
	bands := map[string]string{}
	for _, f := range listed.Findings {
		bands[f.RuleID] = f.Band
	}
	if bands["actions/unpinned-tag"] != "medium" || bands["actions/other"] != "unknown" {
		t.Errorf("bands %v, want medium for the 5.5 and unknown for no score", bands)
	}
}

// A finding term the listed kinds do not answer says where it did match. On the crawled
// organization the one live critical finding sits on a workflow, and the dependencies listing
// answered `securitySeverity=critical` with 0 and no word about it: workflows left this unit's
// kinds and stayed on the list of kinds it treats as already on screen.
func TestTheListingNamesAWorkflowAFindingTermMatchedOutsideItsKinds(t *testing.T) {
	s := advisoryStore(t)
	inventoryCorpus(t, s, 2)
	workflowWithJobs(t, s, "octo-org/app", ".github/workflows/release.yml", "build", 1)
	scoredOn(t, s, "octo-org/app/.github/workflows/release.yml",
		"actions/untrusted-checkout/critical", "error", score(9.3))

	page, err := s.ActionsInventoryPage(InventoryQuery{
		SecuritySeverities: []string{viewspec.BandCritical}})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("matched %d dependencies, want none: the finding is on a workflow", page.Total)
	}
	if len(page.Elsewhere) != 1 || page.Elsewhere[0] != (KindCount{Kind: "workflow", Count: 1}) {
		t.Errorf("Elsewhere = %+v, want the one workflow the band matched", page.Elsewhere)
	}
}
