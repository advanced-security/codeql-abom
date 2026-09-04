package ingest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// ingestAs loads the fixture as though it came from the named repository, so that a single
// fixture can stand in for many repositories sharing similar workflows.
func ingestAs(t *testing.T, s *store.Store, repository string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	defer f.Close()

	w := store.NewWriter(s, 0)
	if _, err := New(s, w).EdgesFromBQRS(f, Options{Repository: repository}); err != nil {
		t.Fatalf("EdgesFromBQRS(%s): %v", repository, err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	w.ResetLocal()
}

// blastRadius counts the distinct repositories that transitively depend on a node.
const blastRadiusSQL = `
WITH RECURSIVE dependents(node_row_id) AS (
    SELECT row_id FROM abom_node WHERE node_key = ?
  UNION
    SELECT e.src_node_row_id
    FROM abom_edge e JOIN dependents d ON e.dst_node_row_id = d.node_row_id
)
SELECT COUNT(DISTINCT n.repository_row_id)
FROM dependents d JOIN abom_node n ON d.node_row_id = n.row_id
WHERE n.repository_row_id IS NOT NULL`

func blastRadius(t *testing.T, s *store.Store, nodeKey string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(blastRadiusSQL, nodeKey).Scan(&n); err != nil {
		t.Fatalf("blast radius for %q: %v", nodeKey, err)
	}
	return n
}

// "What is the blast radius of this action?" — the headline question.
func TestBlastRadiusCountsDependentRepositories(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")
	ingestAs(t, s, "octo-org/service")
	ingestAs(t, s, "octo-org/tool")

	if got := blastRadius(t, s, "actions/checkout"); got != 3 {
		t.Errorf("actions/checkout blast radius = %d, want 3", got)
	}
}

// A local action in one repository must never merge with a same-named action in another, or
// blast radius is silently overstated across the whole organization.
func TestLocalActionsAreScopedPerRepository(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")
	ingestAs(t, s, "octo-org/service")

	var nodes int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_node WHERE node_kind IN ('local_action','composite_action')`).
		Scan(&nodes); err != nil {
		t.Fatalf("counting local action nodes: %v", err)
	}
	if nodes != 2 {
		t.Errorf("got %d local action nodes across 2 repositories, want 2", nodes)
	}
}

// A step using ./.github/actions/greet must reach the actions that composite action itself
// uses, or transitive supply-chain risk is invisible.
func TestBlastRadiusTraversesCompositeActions(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")
	ingestAs(t, s, "octo-org/service")

	if got := blastRadius(t, s, "actions/cache"); got != 2 {
		t.Errorf("actions/cache blast radius = %d, want 2 (reached via the composite action)", got)
	}
}

// A local reusable workflow reference must resolve to the workflow node it names.
func TestLocalReusableWorkflowResolvesToWorkflowNode(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")

	var kind string
	if err := s.DB().QueryRow(`
		SELECT n.node_kind FROM abom_edge e JOIN abom_node n ON e.dst_node_row_id = n.row_id
		WHERE e.ref_raw = './.github/workflows/reusable.yml'`).Scan(&kind); err != nil {
		t.Fatalf("querying reusable workflow target: %v", err)
	}
	if kind != "workflow" {
		t.Errorf("local reusable workflow resolved to %q, want workflow", kind)
	}
}

// Ingestion records references verbatim and judges none of them. Whether any of these is
// "unpinned" is decided by a CodeQL query and stored as a finding; posture is asserted in the
// graph package against those findings.
func TestReferencesArePreservedVerbatim(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")

	rows, err := s.DB().Query(`SELECT ref_raw FROM abom_edge ORDER BY ref_raw`)
	if err != nil {
		t.Fatalf("querying references: %v", err)
	}
	defer rows.Close()

	got := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		got[raw] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading references: %v", err)
	}

	// Every reference shape in the fixture must survive, including the ones upstream queries
	// ignore, because a later abom/ query is what will evaluate them.
	for _, want := range []string{
		"actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
		"actions/setup-node@v4",
		"actions/stale@main",
		"actions/labeler",
		"./.github/actions/greet",
		"docker://alpine:3.20",
		"github/codeql-action/init@v3",
		"octo-org/shared/.github/workflows/build.yml@v1",
		"./.github/workflows/reusable.yml",
	} {
		if !got[want] {
			t.Errorf("reference %q was not preserved (all: %v)", want, got)
		}
	}
}

// "Which repositories would a version bump affect?"
func TestBumpImpactListsAffectedRepositories(t *testing.T) {
	s := newTestStore(t)
	ingestAs(t, s, "octo-org/app")
	ingestAs(t, s, "octo-org/service")

	rows, err := s.DB().Query(`
		SELECT DISTINCT r.repository_full_name
		FROM abom_edge e
		JOIN abom_node n ON e.dst_node_row_id = n.row_id
		JOIN repository r ON e.repository_row_id = r.row_id
		WHERE n.node_key = 'actions/setup-node' AND e.ref_value = 'v4'
		ORDER BY r.repository_full_name`)
	if err != nil {
		t.Fatalf("querying bump impact: %v", err)
	}
	defer rows.Close()

	var affected []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		affected = append(affected, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading bump impact: %v", err)
	}
	if len(affected) != 2 || affected[0] != "octo-org/app" || affected[1] != "octo-org/service" {
		t.Errorf("affected = %v, want both repositories", affected)
	}
}

// Cycles are possible through mutually-referencing reusable workflows; traversal must finish.
func TestBlastRadiusTerminatesOnCycles(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)

	repoID, err := s.UpsertRepository("octo-org/cyclic", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	for _, key := range []string{"wf:a", "wf:b"} {
		if err := w.AddNode(store.Node{Key: key, Kind: "workflow", RepositoryID: repoID}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	edges := []store.Edge{
		{Key: "e:a->b", SrcKey: "wf:a", DstKey: "wf:b", Kind: "job-uses-reusable-workflow"},
		{Key: "e:b->a", SrcKey: "wf:b", DstKey: "wf:a", Kind: "job-uses-reusable-workflow"},
	}
	for _, e := range edges {
		if err := w.AddEdge(e); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if got := blastRadius(t, s, "wf:a"); got != 1 {
		t.Errorf("cyclic blast radius = %d, want 1", got)
	}
}
