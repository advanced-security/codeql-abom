// Package pipeline orchestrates ABOM's end-to-end processing of an organisation.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/bqrs"
	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// DatabaseSource supplies CodeQL databases. It is satisfied by catalog.Client and by any
// mrva-system or mrvahepc adapter added later.
type DatabaseSource interface {
	FindDatabase(ctx context.Context, repository, language string) (catalog.Database, error)
	DownloadDatabase(ctx context.Context, repository, language, destPath string) error
}

// QueryRunner evaluates a query against a database directory.
type QueryRunner interface {
	RunQuery(ctx context.Context, dbPath, queryPath, outputPath string) error
}

// Decoder converts a BQRS file into the JSON the ingester consumes.
type Decoder func(ctx context.Context, bqrsPath, jsonPath string) error

// Config parameterises a run.
type Config struct {
	Language    string
	QueryPath   string
	QueryID     string
	WorkDir     string
	Concurrency int

	// ExtractorVersion invalidates ledger entries when result interpretation changes, so a
	// fixed classification bug re-runs work that is otherwise considered complete.
	ExtractorVersion string

	// KeepArtifacts retains downloaded databases and decoded results for debugging. Left off,
	// each repository's artifacts are removed as soon as they are ingested, which is what
	// keeps disk usage bounded across an organisation.
	KeepArtifacts bool
}

// Pipeline processes repositories into the ABOM graph.
type Pipeline struct {
	Source   DatabaseSource
	Runner   QueryRunner
	Decode   Decoder
	Store    *store.Store
	Ingester *ingest.Ingester
	Writer   *store.Writer
	Config   Config
	OnEvent  func(work.Event)
}

// DefaultDecoder shells out to the CodeQL CLI.
func DefaultDecoder(codeqlPath string) Decoder {
	return func(ctx context.Context, bqrsPath, jsonPath string) error {
		out, err := os.Create(jsonPath)
		if err != nil {
			return fmt.Errorf("creating %s: %w", jsonPath, err)
		}
		defer out.Close()
		return bqrs.DecodeToJSON(ctx, codeqlPath, bqrsPath, out)
	}
}

// Run processes every repository, returning aggregate statistics.
func (p *Pipeline) Run(ctx context.Context, repositories []string) (work.Stats, error) {
	if p.Config.WorkDir == "" {
		return work.Stats{}, errors.New("pipeline: Config.WorkDir is required")
	}
	if p.Config.QueryPath == "" {
		return work.Stats{}, errors.New("pipeline: Config.QueryPath is required")
	}

	jobs := make([]work.Job, 0, len(repositories))
	for _, repo := range repositories {
		jobs = append(jobs, work.Job{Repository: repo})
	}

	pool := &work.Pool{
		Concurrency: p.Config.Concurrency,
		Prepare:     p.prepare,
		Consume:     p.consume,
		OnEvent:     p.OnEvent,
	}
	return pool.Run(ctx, jobs)
}

// prepare performs the parallelizable work: metadata lookup, ledger check, download, query
// evaluation and decoding.
func (p *Pipeline) prepare(ctx context.Context, job work.Job) (work.Prepared, error) {
	db, err := p.Source.FindDatabase(ctx, job.Repository, p.Config.Language)
	if err != nil {
		if errors.Is(err, catalog.ErrNoDatabase) {
			return work.Prepared{}, fmt.Errorf("%s: %w", job.Repository, work.ErrSkip)
		}
		return work.Prepared{}, err
	}

	// Consulting the ledger on the commit alone avoids downloading a database whose results
	// are already recorded, which is the difference between thousands of downloads and none.
	should, err := p.Store.ShouldProcess(p.unit(job.Repository, db.CommitOID))
	if err != nil {
		return work.Prepared{}, err
	}
	if !should {
		return work.Prepared{}, fmt.Errorf("%s already ingested: %w", job.Repository, work.ErrSkip)
	}

	dir := filepath.Join(p.Config.WorkDir, sanitize(job.Repository))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return work.Prepared{}, fmt.Errorf("creating work directory: %w", err)
	}

	zipPath := filepath.Join(dir, "database.zip")
	if err := p.Source.DownloadDatabase(ctx, job.Repository, p.Config.Language, zipPath); err != nil {
		return work.Prepared{}, err
	}

	dbDir, err := runner.Extract(zipPath, filepath.Join(dir, "db"))
	if err != nil {
		return work.Prepared{}, err
	}

	bqrsPath := filepath.Join(dir, "results.bqrs")
	if err := p.Runner.RunQuery(ctx, dbDir, p.Config.QueryPath, bqrsPath); err != nil {
		return work.Prepared{}, err
	}

	jsonPath := filepath.Join(dir, "results.json")
	if err := p.Decode(ctx, bqrsPath, jsonPath); err != nil {
		return work.Prepared{}, err
	}

	return work.Prepared{Job: job, ResultPath: jsonPath, Commit: db.CommitOID}, nil
}

// consume persists one repository's results. It runs serially because the store admits a
// single writer.
func (p *Pipeline) consume(ctx context.Context, prepared work.Prepared) (int, error) {
	unit := p.unit(prepared.Job.Repository, prepared.Commit)
	if err := p.Store.BeginUnit(unit); err != nil {
		return 0, err
	}

	rows, err := p.ingestFile(prepared)
	if err != nil {
		// A failed unit stays in the ledger as failed so the next run retries it.
		if ledgerErr := p.Store.FailUnit(unit, err); ledgerErr != nil {
			return 0, fmt.Errorf("%w (and recording the failure failed: %v)", err, ledgerErr)
		}
		return 0, err
	}

	if err := p.Store.CompleteUnit(unit, rows); err != nil {
		return 0, err
	}
	if !p.Config.KeepArtifacts {
		os.RemoveAll(filepath.Dir(prepared.ResultPath))
	}
	return rows, nil
}

func (p *Pipeline) ingestFile(prepared work.Prepared) (int, error) {
	f, err := os.Open(prepared.ResultPath)
	if err != nil {
		return 0, fmt.Errorf("opening results: %w", err)
	}
	defer f.Close()

	rows, err := p.Ingester.EdgesFromBQRS(f, ingest.Options{Repository: prepared.Job.Repository})
	if err != nil {
		return 0, err
	}
	if err := p.Writer.Flush(); err != nil {
		return 0, err
	}
	// Repository-local node ids are useless to the next repository and would otherwise grow
	// the cache without bound.
	p.Writer.ResetLocal()
	return rows, nil
}

func (p *Pipeline) unit(repository, commit string) store.Unit {
	return store.Unit{
		Repository:       repository,
		DBCommit:         commit,
		QueryID:          p.Config.QueryID,
		ExtractorVersion: p.Config.ExtractorVersion,
	}
}

// sanitize turns owner/name into a single safe path segment.
func sanitize(repository string) string {
	return strings.NewReplacer("/", "_", "..", "_", string(os.PathSeparator), "_").
		Replace(repository)
}
