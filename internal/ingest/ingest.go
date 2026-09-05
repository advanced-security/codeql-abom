// Package ingest turns CodeQL query results into ABOM graph rows.
//
// It is the seam between extraction (BQRS/SARIF) and persistence, and it is where the raw
// `uses:` strings emitted by AbomEdges.ql become classified nodes and edges.
package ingest

import (
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/bqrs"
	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Edge kinds emitted by AbomEdges.ql.
const (
	edgeStepUsesAction      = "step-uses-action"
	edgeCompositeUsesAction = "composite-step-uses-action"
	edgeJobUsesReusableWF   = "job-uses-reusable-workflow"
)

// Node kinds for source-side vertices.
const (
	nodeRepo              = "repo"
	nodeWorkflow          = "workflow"
	nodeCompositeAction   = "composite_action"
	nodeJob               = "job"
	nodeStep              = "step"
	provenanceUnspecified = "bqrs"
)

// Options describes the repository a batch of results belongs to.
type Options struct {
	Repository    string
	RepositoryURL string
	// Provenance records how the rows were obtained, distinguishing CodeQL-derived edges from
	// any future YAML-parsed fallback.
	Provenance string
	// QueryRunID ties every emitted row back to the query and pack version that produced it.
	QueryRunID int64
	// CLIVersion is the CodeQL the ledger recorded for this run. When set, SARIF that names a
	// different one is refused rather than attributed to the wrong analyzer.
	CLIVersion string
}

// Ingester writes graph rows for one repository at a time.
type Ingester struct {
	store  *store.Store
	writer *store.Writer
}

// New returns an Ingester bound to a store and its batched writer.
func New(s *store.Store, w *store.Writer) *Ingester {
	return &Ingester{store: s, writer: w}
}

// EdgesFromBQRS streams AbomEdges.ql results into the graph, returning the number of edges.
//
// Rows are consumed as they decode so that a repository emitting a very large number of edges
// never has to be held in memory.
func (i *Ingester) EdgesFromBQRS(r io.Reader, opts Options) (int, error) {
	if opts.Repository == "" {
		return 0, fmt.Errorf("ingest: Options.Repository is required")
	}
	provenance := opts.Provenance
	if provenance == "" {
		provenance = provenanceUnspecified
	}

	repoID, err := i.store.UpsertRepository(opts.Repository, opts.RepositoryURL)
	if err != nil {
		return 0, err
	}

	repoKey := normalize.RepoKey(opts.Repository)
	if err := i.writer.AddNode(store.Node{
		Key:          repoKey,
		Kind:         nodeRepo,
		DisplayName:  opts.Repository,
		RepositoryID: repoID,
	}); err != nil {
		return 0, err
	}

	// Containers already emitted for this repository, so a workflow with many jobs does not
	// re-queue its parent node once per row.
	seen := make(map[string]bool)

	return bqrs.StreamSelect(r, func(row bqrs.Row) error {
		return i.edge(row, opts, repoID, repoKey, provenance, seen)
	})
}

func (i *Ingester) edge(
	row bqrs.Row, opts Options, repoID int64, repoKey, provenance string, seen map[string]bool,
) error {
	edgeKind, err := row.String("edgeKind")
	if err != nil {
		return err
	}
	srcPath, err := row.String("srcPath")
	if err != nil {
		return err
	}
	srcJobID, err := row.String("srcJobId")
	if err != nil {
		return err
	}
	stepIndex, err := row.Int("srcStepIndex")
	if err != nil {
		return err
	}
	usesRaw, err := row.String("usesRaw")
	if err != nil {
		return err
	}
	pinComment, err := row.String("pinComment")
	if err != nil {
		return err
	}
	startLine, err := row.Int("startLine")
	if err != nil {
		return err
	}
	startColumn, err := row.Int("startColumn")
	if err != nil {
		return err
	}
	endLine, err := row.Int("endLine")
	if err != nil {
		return err
	}
	endColumn, err := row.Int("endColumn")
	if err != nil {
		return err
	}

	srcKey, err := i.sourceChain(edgeKind, opts.Repository, srcPath, srcJobID, stepIndex,
		repoID, repoKey, seen)
	if err != nil {
		return err
	}

	ref := normalize.ParseUses(usesRaw, pinComment)
	dstKey := ref.NodeKeyIn(opts.Repository)

	// A local reference and the file that defines it must resolve to one node, so local
	// targets adopt the container's kind and repository instead of a distinct identity.
	dstKind := string(ref.NodeKind)
	var dstRepoID int64
	switch ref.NodeKind {
	case normalize.NodeLocalAction:
		dstKind, dstRepoID = nodeCompositeAction, repoID
	case normalize.NodeLocalWorkflow:
		dstKind, dstRepoID = nodeWorkflow, repoID
	}

	if err := i.writer.AddNode(store.Node{
		Key:          dstKey,
		Kind:         dstKind,
		Owner:        ref.Owner,
		Repo:         ref.Repo,
		Path:         ref.Subpath,
		DisplayName:  usesRaw,
		IsExternal:   ref.IsExternal,
		RepositoryID: dstRepoID,
	}); err != nil {
		return err
	}

	return i.writer.AddEdge(store.Edge{
		Key:          normalize.EdgeKey(opts.Repository, srcPath, startLine, startColumn),
		SrcKey:       srcKey,
		DstKey:       dstKey,
		Kind:         edgeKind,
		RefRaw:       usesRaw,
		RefValue:     ref.RefValue,
		PinComment:   ref.PinComment,
		FilePath:     srcPath,
		StartLine:    startLine,
		StartColumn:  startColumn,
		EndLine:      endLine,
		EndColumn:    endColumn,
		RepositoryID: repoID,
		QueryRunID:   opts.QueryRunID,
		Provenance:   provenance,
	})
}

// sourceChain emits the container nodes an edge hangs from and returns the key of the node the
// edge originates at.
//
// Containment is expressed through node columns rather than extra edges: at organization scale
// that avoids millions of rows that carry no information the columns do not already hold.
func (i *Ingester) sourceChain(
	edgeKind, repository, srcPath, srcJobID string, stepIndex int,
	repoID int64, repoKey string, seen map[string]bool,
) (string, error) {
	addOnce := func(n store.Node) error {
		if seen[n.Key] {
			return nil
		}
		seen[n.Key] = true
		return i.writer.AddNode(n)
	}

	switch edgeKind {
	case edgeCompositeUsesAction:
		containerKey := normalize.CompositeActionKey(repository, srcPath)
		if err := addOnce(store.Node{
			Key: containerKey, Kind: nodeCompositeAction, Repo: repository, Path: srcPath,
			DisplayName: srcPath, RepositoryID: repoID,
		}); err != nil {
			return "", err
		}
		stepKey := normalize.StepKey(repository, srcPath, "", stepIndex)
		if err := addOnce(store.Node{
			Key: stepKey, Kind: nodeStep, Repo: repository, Path: srcPath,
			StepIndex: stepIndex, RepositoryID: repoID, ContainerKey: containerKey,
		}); err != nil {
			return "", err
		}
		return stepKey, nil

	case edgeJobUsesReusableWF:
		workflowKey := normalize.WorkflowKey(repository, srcPath)
		if err := addOnce(store.Node{
			Key: workflowKey, Kind: nodeWorkflow, Repo: repository, Path: srcPath,
			DisplayName: srcPath, RepositoryID: repoID,
		}); err != nil {
			return "", err
		}
		jobKey := normalize.JobKey(repository, srcPath, srcJobID)
		if err := addOnce(store.Node{
			Key: jobKey, Kind: nodeJob, Repo: repository, Path: srcPath, JobID: srcJobID,
			DisplayName: srcJobID, StepIndex: -1, RepositoryID: repoID,
			ContainerKey: workflowKey,
		}); err != nil {
			return "", err
		}
		return jobKey, nil

	case edgeStepUsesAction:
		workflowKey := normalize.WorkflowKey(repository, srcPath)
		if err := addOnce(store.Node{
			Key: workflowKey, Kind: nodeWorkflow, Repo: repository, Path: srcPath,
			DisplayName: srcPath, RepositoryID: repoID,
		}); err != nil {
			return "", err
		}
		jobKey := normalize.JobKey(repository, srcPath, srcJobID)
		if err := addOnce(store.Node{
			Key: jobKey, Kind: nodeJob, Repo: repository, Path: srcPath, JobID: srcJobID,
			DisplayName: srcJobID, StepIndex: -1, RepositoryID: repoID,
			ContainerKey: workflowKey,
		}); err != nil {
			return "", err
		}
		stepKey := normalize.StepKey(repository, srcPath, srcJobID, stepIndex)
		if err := addOnce(store.Node{
			Key: stepKey, Kind: nodeStep, Repo: repository, Path: srcPath, JobID: srcJobID,
			StepIndex: stepIndex, RepositoryID: repoID, ContainerKey: workflowKey,
		}); err != nil {
			return "", err
		}
		return stepKey, nil

	default:
		return "", fmt.Errorf("ingest: unknown edge kind %q", edgeKind)
	}
}
