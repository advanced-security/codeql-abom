package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// edge mirrors one AbomEdges.ql result row.
type edge struct {
	kind      string
	path      string
	jobID     string
	stepIndex int
	uses      string
	line      int
}

// bqrsJSON renders edges in the exact shape `codeql bqrs decode --format=json` emits.
func bqrsJSON(edges []edge) string {
	cols := []string{
		"edgeKind", "srcKind", "srcPath", "srcJobId", "srcStepIndex",
		"srcStepId", "usesRaw", "pinComment", "startLine", "startColumn",
		"endLine", "endColumn",
	}
	var b strings.Builder
	b.WriteString(`{"#select":{"columns":[`)
	for i, c := range cols {
		if i > 0 {
			b.WriteByte(',')
		}
		kind := "String"
		if strings.HasPrefix(c, "src") && c == "srcStepIndex" || strings.HasSuffix(c, "Line") ||
			strings.HasSuffix(c, "Column") {
			kind = "Integer"
		}
		fmt.Fprintf(&b, `{"name":%q,"kind":%q}`, c, kind)
	}
	b.WriteString(`],"tuples":[`)
	for i, e := range edges {
		if i > 0 {
			b.WriteByte(',')
		}
		srcKind := "job"
		if e.kind == edgeCompositeUsesAction {
			srcKind = "composite_action"
		} else if e.kind == edgeJobUsesReusableWF {
			srcKind = "workflow"
		}
		uses, _ := json.Marshal(e.uses)
		fmt.Fprintf(&b, `[%q,%q,%q,%q,%d,"",%s,"",%d,1,%d,9]`,
			e.kind, srcKind, e.path, e.jobID, e.stepIndex, uses, e.line, e.line)
	}
	b.WriteString(`]}}`)
	return b.String()
}

func ingestEdges(t *testing.T, ing *Ingester, w *store.Writer, repo string, edges []edge) {
	t.Helper()
	if _, err := ing.EdgesFromBQRS(strings.NewReader(bqrsJSON(edges)), Options{Repository: repo}); err != nil {
		t.Fatalf("ingesting %s: %v", repo, err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("flushing %s: %v", repo, err)
	}
	w.ResetLocal()
}

// reachableFrom returns every node transitively reachable from the given repository, walking
// dependency edges through whatever the targets themselves depend on.
func reachableFrom(t *testing.T, s *store.Store, repo string) map[string]bool {
	t.Helper()
	rows, err := s.DB().Query(`
		WITH RECURSIVE seed(id) AS (
			SELECT e.dst_node_row_id
			FROM abom_edge e
			JOIN repository r ON r.row_id = e.repository_row_id
			WHERE r.repository_full_name = ?
			UNION
			SELECT e.dst_node_row_id
			FROM abom_edge e
			JOIN seed ON seed.id = e.src_node_row_id
			UNION
			-- A referenced workflow or action is also the container its own edges hang from.
			SELECT e.dst_node_row_id
			FROM abom_edge e
			JOIN abom_node src ON src.row_id = e.src_node_row_id
			JOIN seed ON seed.id = src.container_node_row_id
		)
		SELECT n.node_key FROM seed JOIN abom_node n ON n.row_id = seed.id`, repo)
	if err != nil {
		t.Fatalf("traversing graph: %v", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		out[key] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading reachable nodes: %v", err)
	}
	return out
}

// A repository calling another repository's reusable workflow must inherit that workflow's own
// dependencies. Without key unification the two appear as separate nodes and the transitive
// supply chain is invisible.
func TestCrossRepositoryReusableWorkflowConnects(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestEdges(t, ing, w, "octo-org/consumer", []edge{
		{kind: edgeJobUsesReusableWF, path: ".github/workflows/ci.yml", jobID: "call",
			stepIndex: -1, uses: "octo-org/shared/.github/workflows/build.yml@v1", line: 5},
	})
	ingestEdges(t, ing, w, "octo-org/shared", []edge{
		{kind: edgeStepUsesAction, path: ".github/workflows/build.yml", jobID: "build",
			stepIndex: 0, uses: "actions/checkout@v4", line: 9},
	})

	reachable := reachableFrom(t, s, "octo-org/consumer")
	if !reachable["octo-org/shared/.github/workflows/build.yml"] {
		t.Error("consumer does not reach the shared reusable workflow")
	}
	if !reachable["actions/checkout"] {
		t.Error("consumer does not transitively reach actions/checkout through the shared workflow")
	}
}

// The same must hold for a composite action published by another repository.
func TestCrossRepositoryCompositeActionConnects(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestEdges(t, ing, w, "octo-org/consumer", []edge{
		{kind: edgeStepUsesAction, path: ".github/workflows/ci.yml", jobID: "build",
			stepIndex: 0, uses: "octo-org/actions-lib/greet@v1", line: 7},
	})
	ingestEdges(t, ing, w, "octo-org/actions-lib", []edge{
		{kind: edgeCompositeUsesAction, path: "greet/action.yml", stepIndex: 0,
			uses: "actions/setup-node@v4", line: 4},
	})

	reachable := reachableFrom(t, s, "octo-org/consumer")
	if !reachable["octo-org/actions-lib/greet"] {
		t.Error("consumer does not reach the published composite action")
	}
	if !reachable["actions/setup-node"] {
		t.Error("consumer does not transitively reach actions/setup-node through the composite action")
	}
}

// Ingestion order must not matter: the consumer may be processed before the repository that
// defines what it calls, which is the normal case across a large organization.
func TestUnificationIsOrderIndependent(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestEdges(t, ing, w, "octo-org/shared", []edge{
		{kind: edgeStepUsesAction, path: ".github/workflows/build.yml", jobID: "build",
			stepIndex: 0, uses: "actions/checkout@v4", line: 9},
	})
	ingestEdges(t, ing, w, "octo-org/consumer", []edge{
		{kind: edgeJobUsesReusableWF, path: ".github/workflows/ci.yml", jobID: "call",
			stepIndex: -1, uses: "octo-org/shared/.github/workflows/build.yml@v1", line: 5},
	})

	if !reachableFrom(t, s, "octo-org/consumer")["actions/checkout"] {
		t.Error("transitive reach depends on ingestion order")
	}

	// The shared workflow must be one node, not one per naming form.
	var nodes int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_node WHERE node_key = ?`,
		"octo-org/shared/.github/workflows/build.yml").Scan(&nodes); err != nil {
		t.Fatalf("counting nodes: %v", err)
	}
	if nodes != 1 {
		t.Errorf("shared workflow has %d nodes, want 1", nodes)
	}
}
