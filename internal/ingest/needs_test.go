package ingest

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// edgeRow reads one edge back with the node keys and kinds it actually resolved to, because the
// interesting failure is an endpoint pointing at the wrong node rather than a missing row.
type edgeRow struct {
	Kind     string
	SrcKey   string
	DstKey   string
	DstKind  string
	External int
}

// `needs:` is the only ordering GitHub Actions guarantees between jobs, and both of its
// endpoints are jobs in the same file — not a `uses:` reference to something external. Parsing
// the target as a uses string would invent an owner and repo from a bare job id.
func TestANeededJobResolvesToTheJobInTheSameFile(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestEdges(t, ing, w, "octo-org/consumer", []edge{
		{kind: edgeJobNeedsJob, path: ".github/workflows/ci.yml", jobID: "deploy",
			stepIndex: -1, uses: "build", line: 12},
	})

	rows, err := s.DB().Query(`
		SELECT e.edge_kind, src.node_key, dst.node_key, dst.node_kind, dst.is_external
		FROM abom_edge e
		JOIN abom_node src ON src.row_id = e.src_node_row_id
		JOIN abom_node dst ON dst.row_id = e.dst_node_row_id
		WHERE e.edge_kind = ?`, edgeJobNeedsJob)
	if err != nil {
		t.Fatalf("reading needs edges: %v", err)
	}
	defer rows.Close()

	var got []edgeRow
	for rows.Next() {
		var r edgeRow
		if err := rows.Scan(&r.Kind, &r.SrcKey, &r.DstKey, &r.DstKind, &r.External); err != nil {
			t.Fatalf("scanning: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading needs edges: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly one needs edge, got %d", len(got))
	}

	wantSrc := normalize.JobKey("octo-org/consumer", ".github/workflows/ci.yml", "deploy")
	wantDst := normalize.JobKey("octo-org/consumer", ".github/workflows/ci.yml", "build")
	if got[0].SrcKey != wantSrc {
		t.Errorf("src is %q, want the declaring job %q", got[0].SrcKey, wantSrc)
	}
	if got[0].DstKey != wantDst {
		t.Errorf("dst is %q, want the needed job %q", got[0].DstKey, wantDst)
	}
	if got[0].DstKind != nodeJob {
		t.Errorf("dst kind is %q, want %q", got[0].DstKind, nodeJob)
	}
	if got[0].External != 0 {
		t.Error("a job in the analyzed repository was recorded as external")
	}
}

// A needed job that no other edge mentions still has to exist as a node, or the edge dangles.
func TestANeededJobIsCreatedEvenWhenNothingElseNamesIt(t *testing.T) {
	s := newTestStore(t)
	w := store.NewWriter(s, 0)
	ing := New(s, w)

	ingestEdges(t, ing, w, "octo-org/consumer", []edge{
		{kind: edgeJobNeedsJob, path: ".github/workflows/ci.yml", jobID: "deploy",
			stepIndex: -1, uses: "build", line: 12},
	})

	key := normalize.JobKey("octo-org/consumer", ".github/workflows/ci.yml", "build")
	var container string
	err := s.DB().QueryRow(`
		SELECT COALESCE(c.node_key, '')
		FROM abom_node n
		LEFT JOIN abom_node c ON c.row_id = n.container_node_row_id
		WHERE n.node_key = ?`, key).Scan(&container)
	if err != nil {
		t.Fatalf("the needed job was never created as a node: %v", err)
	}
	want := normalize.WorkflowKey("octo-org/consumer", ".github/workflows/ci.yml")
	if container != want {
		t.Errorf("needed job hangs from %q, want its workflow %q", container, want)
	}
}
