// Package archive runs ABOM's own queries over CodeQL databases already on disk.
//
// A MRVA download leaves one database archive per repository. Evaluating them locally is how
// the dependency graph is built, because the graph query is a plain table query that the
// hosted MRVA backend will not run.
package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/sarifin"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// QueryRunner evaluates a query against an extracted database.
type QueryRunner interface {
	RunQuery(ctx context.Context, dbPath, queryPath, outputPath string) error
}

// Extractor unpacks a database archive and returns the database root.
type Extractor func(zipPath, destDir string) (string, error)

// Decoder converts BQRS into the JSON the ingester consumes.
type Decoder func(ctx context.Context, bqrsPath, jsonPath string) error

// Options configures a run over a directory of archives.
type Options struct {
	Dir         string
	QueryPath   string
	QueryID     string
	WorkDir     string
	Concurrency int
	// Resolver attributes an archive to a repository when no sibling SARIF is present.
	Resolver *ingest.RepoResolver
	// KeepArtifacts retains extracted databases for debugging.
	KeepArtifacts bool
}

// Runner evaluates a query across every archive in a directory.
type Runner struct {
	CodeQL QueryRunner
	// Suite evaluates a whole query suite, producing the findings side of the model.
	Suite    SuiteAnalyzer
	Extract  Extractor
	Decode   Decoder
	Store    *store.Store
	Ingester *ingest.Ingester
	Writer   *store.Writer
	OnEvent  func(work.Event)
}

// archiveSuffix is how gh-mrva names a downloaded database.
const archiveSuffix = "_db.zip"

// Run evaluates the query against each archive and ingests the results.
func (r *Runner) Run(ctx context.Context, opts Options) (work.Stats, error) {
	if opts.QueryPath == "" || opts.WorkDir == "" {
		return work.Stats{}, fmt.Errorf("archive: QueryPath and WorkDir are required")
	}

	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		return work.Stats{}, fmt.Errorf("reading %s: %w", opts.Dir, err)
	}

	var jobs []work.Job
	archives := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, archiveSuffix) {
			continue
		}
		repo, ok := repositoryFor(opts.Dir, name, opts.Resolver)
		if !ok {
			continue
		}
		jobs = append(jobs, work.Job{Repository: repo})
		archives[repo] = filepath.Join(opts.Dir, name)
	}

	pool := &work.Pool{
		Concurrency: opts.Concurrency,
		Prepare: func(ctx context.Context, job work.Job) (work.Prepared, error) {
			return r.prepare(ctx, job, archives[job.Repository], opts)
		},
		Consume: func(_ context.Context, p work.Prepared) (int, error) {
			return r.consume(p, opts)
		},
		OnEvent: r.OnEvent,
	}
	return pool.Run(ctx, jobs)
}

func (r *Runner) prepare(
	ctx context.Context, job work.Job, archivePath string, opts Options,
) (work.Prepared, error) {
	dir := filepath.Join(opts.WorkDir, strings.ReplaceAll(job.Repository, "/", "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return work.Prepared{}, fmt.Errorf("creating work directory: %w", err)
	}

	dbDir, err := r.Extract(archivePath, filepath.Join(dir, "db"))
	if err != nil {
		return work.Prepared{}, err
	}

	bqrsPath := filepath.Join(dir, "results.bqrs")
	if err := r.CodeQL.RunQuery(ctx, dbDir, opts.QueryPath, bqrsPath); err != nil {
		return work.Prepared{}, err
	}

	jsonPath := filepath.Join(dir, "results.json")
	if err := r.Decode(ctx, bqrsPath, jsonPath); err != nil {
		return work.Prepared{}, err
	}

	// The extracted database is the bulk of the disk cost and is finished with once the
	// query has run, so it goes before the serialized phase rather than after.
	if !opts.KeepArtifacts {
		os.RemoveAll(filepath.Join(dir, "db"))
	}
	return work.Prepared{Job: job, ResultPath: jsonPath}, nil
}

func (r *Runner) consume(p work.Prepared, opts Options) (int, error) {
	runID, err := r.Store.RecordQueryRun(store.QueryRun{
		QueryID: opts.QueryID, Repository: p.Job.Repository, DBCommit: p.Commit,
	})
	if err != nil {
		return 0, err
	}

	f, err := os.Open(p.ResultPath)
	if err != nil {
		return 0, fmt.Errorf("opening results: %w", err)
	}
	defer f.Close()

	rows, err := r.Ingester.EdgesFromBQRS(f, ingest.Options{
		Repository: p.Job.Repository, QueryRunID: runID,
	})
	if err != nil {
		return 0, err
	}
	if err := r.Writer.Flush(); err != nil {
		return 0, err
	}
	r.Writer.ResetLocal()

	if !opts.KeepArtifacts {
		os.RemoveAll(filepath.Dir(p.ResultPath))
	}
	return rows, nil
}

// repositoryFor attributes an archive, preferring the sibling SARIF's recorded provenance over
// the flattened filename.
func repositoryFor(dir, archiveName string, resolver *ingest.RepoResolver) (string, bool) {
	base := strings.TrimSuffix(archiveName, archiveSuffix)

	sarifPath := filepath.Join(dir, base+".sarif")
	if f, err := os.Open(sarifPath); err == nil {
		prov, provErr := sarifin.ReadProvenance(f)
		f.Close()
		if provErr == nil && prov.Repository != "" {
			return prov.Repository, true
		}
	}
	if resolver == nil {
		return "", false
	}
	return resolver.Resolve(base)
}
