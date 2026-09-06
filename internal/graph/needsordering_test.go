package graph

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// needsFixture is a workflow with two jobs and nothing but an ordering edge between them.
func needsFixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	w := store.NewWriter(s, 0)
	repoID, err := s.UpsertRepository("octo-org/app", store.OriginSeed)
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	for _, n := range []store.Node{
		{Key: "wf", Kind: "workflow", RepositoryID: repoID},
		{Key: "job-build", Kind: "job", RepositoryID: repoID, ContainerKey: "wf"},
		{Key: "job-deploy", Kind: "job", RepositoryID: repoID, ContainerKey: "wf"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.Key, err)
		}
	}
	if err := w.AddEdge(store.Edge{
		Key: "needs1", SrcKey: "job-deploy", DstKey: "job-build", Kind: "job-needs-job",
		FilePath: ".github/workflows/ci.yml", StartLine: 3, RepositoryID: repoID,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return s
}

// `needs:` says one job runs after another. It is ordering, not use, so it must not confer a
// blast radius: reach means "this many repositories would be affected if this were compromised",
// and a job in your own workflow is not a component you depend on. Every other edge kind in the
// graph is a `uses:`, so a traversal that reads them all silently absorbs this one.
func TestJobOrderingIsNotADependency(t *testing.T) {
	s := needsFixture(t)

	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	var rows int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*)
		FROM abom_blast_radius b JOIN abom_node n ON n.row_id = b.node_row_id
		WHERE n.node_key = 'job-build'`).Scan(&rows); err != nil {
		t.Fatalf("reading blast radius: %v", err)
	}
	if rows != 0 {
		t.Errorf("a job named by needs: was given a blast radius (%d rows); "+
			"ordering is not use, and reach means dependents", rows)
	}
}

// The same edge must not enter the runtime/build-time classification, which answers "does
// anything under review execute this dependency" — a question a job is not an answer to.
func TestJobOrderingIsNotClassifiedAsASupplyChainCall(t *testing.T) {
	s := needsFixture(t)

	chain, err := (&Builder{Store: s}).ClassifyDependencies()
	if err != nil {
		t.Fatalf("ClassifyDependencies: %v", err)
	}
	if _, known := chain.Of("job-build"); known {
		t.Error("a job named by needs: was classified as a supply-chain dependency")
	}
}
