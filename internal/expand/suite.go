package expand

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Resolver expands a suite into queries and reads what each declares about itself.
//
// An interface so the expansion loop can be tested without a CodeQL CLI, and so a caller that
// still names a single query needs no resolver at all.
type Resolver interface {
	ResolveQueries(ctx context.Context, suite string) ([]string, error)
	ResolveMetadata(ctx context.Context, queryPath string) (runner.Metadata, error)
}

// plannedQuery is one query to run against a repository's database, with the shape its results
// must be read as.
type plannedQuery struct {
	path  string
	id    string
	shape ingest.Shape
}

// plan works out what to run for a round, once, rather than per repository.
//
// Resolving a suite shells out to the CodeQL CLI, and doing that for every repository in a crawl
// of thousands would cost more than the queries. The suite cannot change mid-round.
func (e *Expander) plan(ctx context.Context, opts Options) ([]plannedQuery, error) {
	if opts.Suite == "" {
		// A single query path stays supported: it is what `expand` and `build` pass, and a
		// suite is not worth resolving to run one thing.
		return []plannedQuery{{
			path: opts.QueryPath, id: opts.QueryID, shape: ingest.ShapeEdges,
		}}, nil
	}
	if e.Resolver == nil {
		return nil, fmt.Errorf("expand: a suite needs a Resolver to expand it")
	}

	paths, err := e.Resolver.ResolveQueries(ctx, opts.Suite)
	if err != nil {
		return nil, err
	}
	planned := make([]plannedQuery, 0, len(paths))
	for _, path := range paths {
		md, err := e.Resolver.ResolveMetadata(ctx, path)
		if err != nil {
			return nil, err
		}
		shape, err := ingest.ShapeOf(md)
		if err != nil {
			// Naming the suite matters: the query is in the run because a suite selected it,
			// and the suite is where someone will go to take it back out.
			return nil, fmt.Errorf("suite %s: %w", opts.Suite, err)
		}
		if shape == ingest.ShapeFindings {
			// Findings come from `database analyze` as SARIF, which is a different lane. A
			// suite may legitimately mix them; this one just does not run them.
			continue
		}
		planned = append(planned, plannedQuery{path: path, id: md.ID, shape: shape})
	}
	return planned, nil
}

// runQueries evaluates every planned query against one database, returning decoded results keyed
// by query id. The first edges query is returned separately because settling a dependency
// depends on it, while everything else only enriches.
func (e *Expander) runQueries(
	ctx context.Context, dbDir, dir string, planned []plannedQuery,
) (edges string, extra map[string]string, err error) {
	for _, q := range planned {
		bqrsPath := filepath.Join(dir, safeName(q.id)+".bqrs")
		if err := e.CodeQL.RunQuery(ctx, dbDir, q.path, bqrsPath); err != nil {
			return "", nil, err
		}
		jsonPath := filepath.Join(dir, safeName(q.id)+".json")
		if err := e.Decode(ctx, bqrsPath, jsonPath); err != nil {
			return "", nil, err
		}
		// The BQRS is decoded and finished with; failing to clear it costs disk, not results.
		_ = os.Remove(bqrsPath)

		if q.shape == ingest.ShapeEdges && edges == "" {
			edges = jsonPath
			continue
		}
		if extra == nil {
			extra = make(map[string]string, len(planned))
		}
		extra[q.id] = jsonPath
	}
	return edges, extra, nil
}

// ingestExtra records one non-edge query's results against the repository just analyzed.
//
// Attribution is per query, not per round: two queries against one database are two statements
// by two authors, and a shared run id would make it impossible to say which said what.
func (e *Expander) ingestExtra(queryID, path, repository, commit string) error {
	runID, err := e.Store.RecordQueryRun(store.QueryRun{
		QueryID: queryID, Repository: repository, DBCommit: commit,
	})
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening results: %w", err)
	}
	defer f.Close()

	_, err = e.Ingester.ObservationsFromBQRS(f, ingest.Options{
		Repository: repository, QueryRunID: runID,
	})
	return err
}

// safeName turns a query id into something usable as a filename. Query ids carry slashes, and
// two queries in one suite must not write to the same path.
func safeName(id string) string {
	if id == "" {
		return "results"
	}
	out := []rune(id)
	for i, r := range out {
		switch r {
		case '/', '\\', ':', ' ':
			out[i] = '-'
		}
	}
	return string(out)
}
