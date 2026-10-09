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
	// The one edge that is ordering rather than use: both ends are jobs in the same file.
	edgeJobNeedsJob = store.EdgeKindJobNeedsJob
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
	// Attribute says which repository holds each file a database reports on, for a database that
	// holds more than one. Nil attributes everything to Repository, which is what one
	// repository's own checkout is. With it set, every repository must already be on record:
	// attribution reads repositories and never creates or rewrites one.
	Attribute func(path string) (Located, error)
}

// Located is where a file a database reported on really lives.
type Located struct {
	Repository string
	Path       string
	// Excluded is a file ABOM wrote into the database itself, which is never a finding location.
	Excluded bool
}

// locate attributes one path, or names it as Repository's when nothing attributes.
func (o Options) locate(path string) (Located, error) {
	if o.Attribute == nil {
		return Located{Repository: o.Repository, Path: path}, nil
	}
	return o.Attribute(path)
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
	// A closure's edges are the edges each repository's own analysis already recorded, key for
	// key, so storing them would restate evidence rather than add any.
	if opts.Attribute != nil {
		return 0, fmt.Errorf("ingest: edges are stored only from a database holding one repository")
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
		return i.edge(row, opts, repoID, provenance, seen)
	})
}

func (i *Ingester) edge(
	row bqrs.Row, opts Options, repoID int64, provenance string, seen map[string]bool,
) error {
	fields, err := readEdgeFields(row)
	if err != nil {
		return err
	}

	srcKey, err := i.sourceChain(fields.edgeKind, opts.Repository, fields.srcPath, fields.srcJobID, fields.stepIndex,
		repoID, seen)
	if err != nil {
		return err
	}

	if fields.edgeKind == edgeJobNeedsJob {
		return i.needsEdge(needsTarget{
			repository: opts.Repository, srcPath: fields.srcPath, srcKey: srcKey, neededJobID: fields.usesRaw,
			repoID: repoID, queryRunID: opts.QueryRunID, provenance: provenance, seen: seen,
			startLine: fields.startLine, startColumn: fields.startColumn,
			endLine: fields.endLine, endColumn: fields.endColumn,
		})
	}

	ref := normalize.ParseUses(fields.usesRaw, fields.pinComment)
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
		DisplayName:  fields.usesRaw,
		IsExternal:   ref.IsExternal,
		RepositoryID: dstRepoID,
	}); err != nil {
		return err
	}

	return i.writer.AddEdge(store.Edge{
		Key:          normalize.EdgeKey(opts.Repository, fields.srcPath, fields.startLine, fields.startColumn),
		SrcKey:       srcKey,
		DstKey:       dstKey,
		Kind:         fields.edgeKind,
		RefRaw:       fields.usesRaw,
		RefValue:     ref.RefValue,
		PinComment:   ref.PinComment,
		FilePath:     fields.srcPath,
		StartLine:    fields.startLine,
		StartColumn:  fields.startColumn,
		EndLine:      fields.endLine,
		EndColumn:    fields.endColumn,
		RepositoryID: repoID,
		QueryRunID:   opts.QueryRunID,
		Provenance:   provenance,
	})
}

type edgeFields struct {
	edgeKind, srcPath, srcJobID string
	usesRaw, pinComment         string
	stepIndex                   int
	startLine, startColumn      int
	endLine, endColumn          int
}

func readEdgeFields(row bqrs.Row) (edgeFields, error) {
	var fields edgeFields
	steps := []func() error{
		func() error { return readString(row, "edgeKind", &fields.edgeKind) },
		func() error { return readString(row, "srcPath", &fields.srcPath) },
		func() error { return readString(row, "srcJobId", &fields.srcJobID) },
		func() error { return readInt(row, "srcStepIndex", &fields.stepIndex) },
		func() error { return readString(row, "usesRaw", &fields.usesRaw) },
		func() error { return readString(row, "pinComment", &fields.pinComment) },
		func() error { return readInt(row, "startLine", &fields.startLine) },
		func() error { return readInt(row, "startColumn", &fields.startColumn) },
		func() error { return readInt(row, "endLine", &fields.endLine) },
		func() error { return readInt(row, "endColumn", &fields.endColumn) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return edgeFields{}, err
		}
	}
	return fields, nil
}

func readString(row bqrs.Row, name string, dst *string) error {
	value, err := row.String(name)
	if err != nil {
		return err
	}
	*dst = value
	return nil
}

func readInt(row bqrs.Row, name string, dst *int) error {
	value, err := row.Int(name)
	if err != nil {
		return err
	}
	*dst = value
	return nil
}

// sourceChain emits the container nodes an edge hangs from and returns the key of the node the
// edge originates at.
//
// Containment is expressed through node columns rather than extra edges: at organization scale
// that avoids millions of rows that carry no information the columns do not already hold.
func (i *Ingester) sourceChain(
	edgeKind, repository, srcPath, srcJobID string, stepIndex int,
	repoID int64, seen map[string]bool,
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

	case edgeJobUsesReusableWF, edgeJobNeedsJob, edgeStepUsesAction:
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
		if edgeKind != edgeStepUsesAction {
			return jobKey, nil
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
