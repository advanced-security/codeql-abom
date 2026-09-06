package ingest

import (
	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// needsTarget carries what a `needs:` row resolves to. It is a struct rather than a long
// parameter list because every field is a string and the order would be easy to transpose.
type needsTarget struct {
	repository  string
	srcPath     string
	srcKey      string
	neededJobID string
	repoID      int64
	queryRunID  int64
	provenance  string
	seen        map[string]bool
	startLine   int
	startColumn int
	endLine     int
	endColumn   int
}

// needsEdge records that one job must run after another.
//
// The target is a bare job id scoped to the file it appears in, never a `uses:` reference, so it
// is keyed directly rather than parsed: running it through ParseUses would read "build" as an
// owner and invent a repository that does not exist.
func (i *Ingester) needsEdge(t needsTarget) error {
	dstKey := normalize.JobKey(t.repository, t.srcPath, t.neededJobID)

	// The needed job may be named before anything else in the crawl mentions it — a job with no
	// `uses:` steps produces no other row at all — so the node is created here or the edge dangles.
	if !t.seen[dstKey] {
		t.seen[dstKey] = true
		if err := i.writer.AddNode(store.Node{
			Key:          dstKey,
			Kind:         nodeJob,
			Repo:         t.repository,
			Path:         t.srcPath,
			JobID:        t.neededJobID,
			DisplayName:  t.neededJobID,
			StepIndex:    -1,
			RepositoryID: t.repoID,
			ContainerKey: normalize.WorkflowKey(t.repository, t.srcPath),
		}); err != nil {
			return err
		}
	}

	return i.writer.AddEdge(store.Edge{
		Key:    normalize.EdgeKey(t.repository, t.srcPath, t.startLine, t.startColumn),
		SrcKey: t.srcKey,
		DstKey: dstKey,
		Kind:   edgeJobNeedsJob,
		// No RefRaw or RefValue: there is no reference here to pin or to drift, which is why
		// the pinning views must never count these rows.
		FilePath:     t.srcPath,
		StartLine:    t.startLine,
		StartColumn:  t.startColumn,
		EndLine:      t.endLine,
		EndColumn:    t.endColumn,
		RepositoryID: t.repoID,
		QueryRunID:   t.queryRunID,
		Provenance:   t.provenance,
	})
}
