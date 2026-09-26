package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func seedFacetFinding(t *testing.T, s *Store, stepKey, rule, provider, severity string, security float64) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding (rule_id, repository_row_id, severity, security_severity, provider, message)
		SELECT ?, n.repository_row_id, ?, ?, NULLIF(?, ''), 'seeded'
		FROM abom_node n WHERE n.node_key = ?`, rule, severity, security, provider, stepKey); err != nil {
		t.Fatalf("inserting finding: %v", err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT (SELECT MAX(row_id) FROM abom_finding),
		       row_id FROM abom_node WHERE node_key = ?`, stepKey); err != nil {
		t.Fatalf("linking finding: %v", err)
	}
}

func graphNodeNamed(t *testing.T, g Graph, key string) GraphNode {
	t.Helper()
	for _, n := range g.Nodes {
		if n.Key == key {
			return n
		}
	}
	t.Fatalf("%s is missing from the graph", key)
	return GraphNode{}
}

// A node carries each kind of finding it holds whole — rule, producer and severity together — so
// the browser can ask for a rule and a severity at once and mean one finding that is both. Three
// separate lists would let a node holding a warning for one rule and an error for another answer
// `rules=A&severity=error`, which no finding does and the set path never would.
func TestAGraphNodeCarriesEachKindOfFindingItHoldsWhole(t *testing.T) {
	s := subgraphFixture(t)
	seedFacetFinding(t, s, "step:tool:0", "actions/unpinned-tag", "CodeQL", "warning", 5.0)
	seedFacetFinding(t, s, "step:tool:0", "actions/unpinned-tag", "CodeQL", "warning", 5.0)
	seedFacetFinding(t, s, "step:tool:0", "actions/code-injection/critical", "", "error", 9.3)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	n := graphNodeNamed(t, g, "octo-org/tool/.github/workflows/ci.yml")

	if len(n.Facets) != 2 {
		t.Fatalf("Facets = %+v, want one per rule, producer and severity", n.Facets)
	}
	critical, unpinned := n.Facets[0], n.Facets[1]
	if critical.Rule != "actions/code-injection/critical" || critical.Severity != "error" ||
		critical.Provider != "" || critical.Count != 1 ||
		critical.SecuritySeverity == nil || *critical.SecuritySeverity != 9.3 {
		t.Errorf("the critical facet = %+v", critical)
	}
	if unpinned.Rule != "actions/unpinned-tag" || unpinned.Severity != "warning" ||
		unpinned.Provider != "CodeQL" || unpinned.Count != 2 {
		t.Errorf("the unpinned facet = %+v", unpinned)
	}
	// The band is read here, once, so the browser never keeps a copy of the thresholds.
	if critical.Band != "critical" || unpinned.Band != "medium" {
		t.Errorf("bands %q and %q, want critical and medium", critical.Band, unpinned.Band)
	}

	total := 0
	for _, f := range n.Facets {
		total += f.Count
	}
	if total != n.Findings {
		t.Errorf("the facets count %d findings and the node %d", total, n.Findings)
	}
}

// A rule that declares no security severity is not a rule scored at zero. CodeQL's scale starts at
// 0.1, and the ingest writes 0 where the metadata is absent, so a zero is read as absent rather
// than rendered as the lowest score there is.
func TestAFacetWithNoDeclaredSecuritySeveritySaysSoRatherThanZero(t *testing.T) {
	s := subgraphFixture(t)
	seedFacetFinding(t, s, "step:tool:0", "actions/unnecessary-use-of-advanced-config", "CodeQL",
		"recommendation", 0)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	n := graphNodeNamed(t, g, "octo-org/tool/.github/workflows/ci.yml")
	if len(n.Facets) != 1 || n.Facets[0].SecuritySeverity != nil || n.Facets[0].Band != "unknown" {
		t.Fatalf("Facets = %+v, want one with no security severity, banded unknown", n.Facets)
	}
	body, err := json.Marshal(n.Facets[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "securitySeverity") {
		t.Errorf("an absent score still travels: %s", body)
	}
}

// A finding reported after a page loaded changes a drawn node's kinds of finding, not the node, so
// the delta carries them beside the count: a live Explorer filtered by severity keeps the node an
// error just arrived on without a reload.
func TestADeltaCarriesTheKindsOfFindingADrawnNodeNowHolds(t *testing.T) {
	s := subgraphFixture(t)
	since, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}
	seedFacetFinding(t, s, "step:tool:0", "actions/code-injection/critical", "CodeQL", "error", 9.3)

	delta, err := s.GraphDelta(since, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	for _, m := range delta.Metrics {
		if m.Key != "octo-org/tool/.github/workflows/ci.yml" {
			if len(m.Facets) != 0 && m.Findings == 0 {
				t.Errorf("%s carries facets %v with no findings", m.Key, m.Facets)
			}
			continue
		}
		if m.Findings != 1 || len(m.Facets) != 1 || m.Facets[0].Band != "critical" ||
			m.Facets[0].Severity != "error" || m.Facets[0].Rule != "actions/code-injection/critical" {
			t.Errorf("the drawn workflow's metric = %+v, want the one critical error it now holds", m)
		}
		return
	}
	t.Fatal("the delta carried no metric for the drawn workflow")
}

// An account rolls up what its members carry, facets as well as the count, or a severity filter
// greys every account out while the chip says it kept the ones holding errors.
func TestAnAccountRollsUpTheFacetsOfWhatItHolds(t *testing.T) {
	s := subgraphFixture(t)
	addRepoNodes(t, s)
	seedFacetFinding(t, s, "step:tool:0", "actions/unpinned-tag", "CodeQL", "warning", 5.0)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	org := graphNodeNamed(t, g, OrgKey("octo-org"))
	if len(org.Facets) != 1 || org.Facets[0].Count != 1 || org.Facets[0].Severity != "warning" {
		t.Errorf("the account's facets = %+v, want the one warning beneath it", org.Facets)
	}
}

// Empty is a list, never null: the browser reads it as a set.
func TestANodeWithNoFindingsCarriesAnEmptyFacetList(t *testing.T) {
	s := subgraphFixture(t)

	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Facets == nil {
			t.Fatalf("%s carries a nil facet list", n.Key)
		}
	}
}
