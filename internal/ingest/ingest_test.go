package ingest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func fixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func ingestFixture(t *testing.T, s *store.Store) int {
	t.Helper()
	w := store.NewWriter(s, 0)
	ing := New(s, w)
	n, err := ing.EdgesFromBQRS(fixture(t), Options{
		Repository:    "octo-org/app",
		RepositoryURL: "https://github.com/octo-org/app",
		Provenance:    "bqrs",
	})
	if err != nil {
		t.Fatalf("EdgesFromBQRS: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return n
}

func TestIngestEdgeCount(t *testing.T) {
	s := newTestStore(t)
	if got := ingestFixture(t, s); got != 13 {
		t.Errorf("ingested %d edges, want 13", got)
	}

	var edges int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edges); err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	if edges != 13 {
		t.Errorf("stored %d edges, want 13", edges)
	}
}

func TestIngestClassifiesReferences(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	tests := []struct {
		usesRaw  string
		refValue string
		nodeKind string
		nodeKey  string
	}{
		{"actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
			"11bd71901bbe5b1630ceea73d27597364c9af683", "action", "actions/checkout"},
		{"actions/setup-node@v4", "v4", "action", "actions/setup-node"},
		{"actions/upload-artifact@v4.6.2", "v4.6.2", "action", "actions/upload-artifact"},
		{"actions/stale@main", "main", "action", "actions/stale"},
		{"actions/labeler", "", "action", "actions/labeler"},
		{"./.github/actions/greet", "", "composite_action",
			"octo-org/app/.github/actions/greet"},
		{"docker://alpine:3.20", "3.20", "docker_image", "docker://alpine:3.20"},
		{"github/codeql-action/init@v3", "v3", "action", "github/codeql-action/init"},
		{"octo-org/shared/.github/workflows/build.yml@v1", "v1", "reusable_workflow",
			"octo-org/shared/.github/workflows/build.yml"},
		{"./.github/workflows/reusable.yml", "", "workflow",
			"octo-org/app/.github/workflows/reusable.yml"},
	}

	for _, tt := range tests {
		t.Run(tt.usesRaw, func(t *testing.T) {
			var refValue sql.NullString
			var nodeKind, nodeKey string
			err := s.DB().QueryRow(`
				SELECT e.ref_value, n.node_kind, n.node_key
				FROM abom_edge e JOIN abom_node n ON e.dst_node_row_id = n.row_id
				WHERE e.ref_raw = ?`, tt.usesRaw).Scan(&refValue, &nodeKind, &nodeKey)
			if err != nil {
				t.Fatalf("querying edge: %v", err)
			}
			if refValue.String != tt.refValue {
				t.Errorf("ref_value = %q, want %q", refValue.String, tt.refValue)
			}
			if nodeKind != tt.nodeKind {
				t.Errorf("node_kind = %q, want %q", nodeKind, tt.nodeKind)
			}
			if nodeKey != tt.nodeKey {
				t.Errorf("node_key = %q, want %q", nodeKey, tt.nodeKey)
			}
		})
	}
}

// The pin comment is only recoverable from QL, so losing it here would be unrecoverable.
// It is stored verbatim and never interpreted.
func TestIngestPreservesPinComment(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var comment string
	if err := s.DB().QueryRow(`
		SELECT pin_comment FROM abom_edge
		WHERE ref_raw = 'actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683'`).
		Scan(&comment); err != nil {
		t.Fatalf("querying edge: %v", err)
	}
	if comment != "v4.2.2" {
		t.Errorf("pin_comment = %q, want v4.2.2", comment)
	}
}

func TestIngestBuildsSourceHierarchy(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	wantKinds := map[string]int{
		"repo":             1,
		"workflow":         2, // main.yml and reusable.yml
		"composite_action": 1, // .github/actions/greet/action.yml
	}
	for kind, want := range wantKinds {
		var got int
		if err := s.DB().QueryRow(
			`SELECT COUNT(*) FROM abom_node WHERE node_kind = ?`, kind).Scan(&got); err != nil {
			t.Fatalf("counting %s nodes: %v", kind, err)
		}
		if got != want {
			t.Errorf("%s nodes = %d, want %d", kind, got, want)
		}
	}
}

// A job-level reusable workflow call has no enclosing step, so the edge must originate at the
// job node rather than inventing a step.
func TestIngestReusableWorkflowEdgeStartsAtJob(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var srcKind string
	if err := s.DB().QueryRow(`
		SELECT n.node_kind FROM abom_edge e JOIN abom_node n ON e.src_node_row_id = n.row_id
		WHERE e.ref_raw = 'octo-org/shared/.github/workflows/build.yml@v1'`).
		Scan(&srcKind); err != nil {
		t.Fatalf("querying edge source: %v", err)
	}
	if srcKind != "job" {
		t.Errorf("source node kind = %q, want job", srcKind)
	}
}

func TestIngestMarksExternalTargets(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var external bool
	if err := s.DB().QueryRow(
		`SELECT is_external FROM abom_node WHERE node_key = 'actions/checkout'`).
		Scan(&external); err != nil {
		t.Fatalf("querying node: %v", err)
	}
	if !external {
		t.Error("actions/checkout must be marked external")
	}

	if err := s.DB().QueryRow(
		`SELECT is_external FROM abom_node WHERE node_key = 'octo-org/app/.github/actions/greet'`).
		Scan(&external); err != nil {
		t.Fatalf("querying node: %v", err)
	}
	if external {
		t.Error("a local action must not be marked external")
	}
}

func TestIngestAttributesEdgesToRepository(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var orphaned int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_edge WHERE repository_row_id IS NULL`).Scan(&orphaned); err != nil {
		t.Fatalf("counting orphaned edges: %v", err)
	}
	if orphaned != 0 {
		t.Errorf("%d edges have no repository attribution", orphaned)
	}
}

// Re-ingesting an unchanged repository must not grow the graph.
func TestIngestIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var nodesBefore, edgesBefore int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node`).Scan(&nodesBefore)
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edgesBefore)

	ingestFixture(t, s)

	var nodesAfter, edgesAfter int
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_node`).Scan(&nodesAfter)
	s.DB().QueryRow(`SELECT COUNT(*) FROM abom_edge`).Scan(&edgesAfter)

	if nodesAfter != nodesBefore || edgesAfter != edgesBefore {
		t.Errorf("re-ingest changed the graph: nodes %d->%d, edges %d->%d",
			nodesBefore, nodesAfter, edgesBefore, edgesAfter)
	}
}

// The composite action is both an edge target and an edge source; that shared identity is what
// makes transitive blast radius work.
func TestIngestLinksCompositeActionAsBothTargetAndSource(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var asTarget int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_edge e JOIN abom_node n ON e.dst_node_row_id = n.row_id
		WHERE n.node_key = 'octo-org/app/.github/actions/greet'`).Scan(&asTarget); err != nil {
		t.Fatalf("counting inbound edges: %v", err)
	}
	if asTarget != 1 {
		t.Errorf("composite action has %d inbound edges, want 1", asTarget)
	}

	var fromComposite int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_edge
		WHERE edge_kind = 'composite-step-uses-action'`).Scan(&fromComposite); err != nil {
		t.Fatalf("counting composite edges: %v", err)
	}
	if fromComposite != 2 {
		t.Errorf("composite action has %d outbound edges, want 2", fromComposite)
	}
}

func TestIngestRecordsProvenance(t *testing.T) {
	s := newTestStore(t)
	ingestFixture(t, s)

	var wrong int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_edge WHERE provenance IS NOT 'bqrs'`).Scan(&wrong); err != nil {
		t.Fatalf("counting provenance: %v", err)
	}
	if wrong != 0 {
		t.Errorf("%d edges have the wrong provenance", wrong)
	}
}
