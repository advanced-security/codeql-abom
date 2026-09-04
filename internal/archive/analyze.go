package archive

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// SuiteAnalyzer evaluates a query suite against an extracted database, writing SARIF.
type SuiteAnalyzer interface {
	AnalyzeDatabase(ctx context.Context, dbPath, suite, sarifPath string) error
}

// AnalyzeOptions configures a suite run over a directory of archives.
type AnalyzeOptions struct {
	Dir     string
	Suite   string
	WorkDir string
	// Concurrency bounds simultaneous evaluations. Suites are memory-hungry, so this wants to
	// be lower than for a single query.
	Concurrency int
	// Resolver attributes an archive to a repository when no sibling SARIF is present.
	Resolver      *ingest.RepoResolver
	KeepArtifacts bool
}

// Analyze evaluates a query suite against every archive and ingests the findings.
//
// This is what fills in the verdict side of the model. The graph records what depends on what;
// only the queries decide whether a given use is a problem, and each finding stays attributed
// to the rule that made that call.
func (r *Runner) Analyze(ctx context.Context, opts AnalyzeOptions) (work.Stats, error) {
	if opts.Suite == "" || opts.WorkDir == "" {
		return work.Stats{}, fmt.Errorf("archive: Suite and WorkDir are required")
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
			return r.prepareAnalysis(ctx, job, archives[job.Repository], opts)
		},
		Consume: func(_ context.Context, p work.Prepared) (int, error) {
			return r.consumeFindings(p, opts)
		},
		OnEvent: r.OnEvent,
	}
	return pool.Run(ctx, jobs)
}

func (r *Runner) prepareAnalysis(
	ctx context.Context, job work.Job, archivePath string, opts AnalyzeOptions,
) (work.Prepared, error) {
	dir := filepath.Join(opts.WorkDir, strings.ReplaceAll(job.Repository, "/", "_"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return work.Prepared{}, fmt.Errorf("creating work directory: %w", err)
	}

	dbDir, err := r.Extract(archivePath, filepath.Join(dir, "db"))
	if err != nil {
		return work.Prepared{}, err
	}

	sarifPath := filepath.Join(dir, "results.sarif")
	if err := r.Suite.AnalyzeDatabase(ctx, dbDir, opts.Suite, sarifPath); err != nil {
		return work.Prepared{}, err
	}

	// The database is the bulk of the disk cost and is finished with once the suite has run.
	if !opts.KeepArtifacts {
		os.RemoveAll(filepath.Join(dir, "db"))
	}
	return work.Prepared{Job: job, ResultPath: sarifPath}, nil
}

func (r *Runner) consumeFindings(p work.Prepared, opts AnalyzeOptions) (int, error) {
	runID, err := r.Store.RecordQueryRun(store.QueryRun{
		QueryID: opts.Suite, Repository: p.Job.Repository, DBCommit: p.Commit,
	})
	if err != nil {
		return 0, err
	}

	f, err := os.Open(p.ResultPath)
	if err != nil {
		return 0, fmt.Errorf("opening sarif: %w", err)
	}
	n, err := r.Ingester.FindingsFromSARIF(f, ingest.Options{
		Repository: p.Job.Repository, QueryRunID: runID,
	})
	f.Close()
	if err != nil {
		return 0, err
	}

	if !opts.KeepArtifacts {
		os.RemoveAll(filepath.Dir(p.ResultPath))
	}
	return n, nil
}
