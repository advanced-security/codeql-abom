// Package discover finds which repositories in an organisation have a CodeQL database.
//
// GitHub's code scanning endpoints are per-repository, so establishing the catalog is itself a
// large parallel workload: one enumeration pass over the organisation, then one lookup for
// every repository it returned. Running it as its own pass means the cost is paid once and
// ingestion can then work from the catalog.
package discover

import (
	"context"
	"errors"
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Source enumerates repositories and looks up their databases.
type Source interface {
	ListRepositories(ctx context.Context, org string) ([]string, error)
	FindDatabase(ctx context.Context, repository, language string) (catalog.Database, error)
}

// Discoverer populates the database catalog.
type Discoverer struct {
	Source      Source
	Store       *store.Store
	Language    string
	Concurrency int
	OnEvent     func(work.Event)
}

// Run enumerates the organisation and records a catalog entry for every repository.
func (d *Discoverer) Run(ctx context.Context, org string) (work.Stats, error) {
	repos, err := d.Source.ListRepositories(ctx, org)
	if err != nil {
		return work.Stats{}, err
	}
	return d.RunRepositories(ctx, repos)
}

// RunRepositories probes an explicit repository list.
func (d *Discoverer) RunRepositories(ctx context.Context, repos []string) (work.Stats, error) {
	if d.Language == "" {
		return work.Stats{}, errors.New("discover: Language is required")
	}

	jobs := make([]work.Job, 0, len(repos))
	for _, r := range repos {
		jobs = append(jobs, work.Job{Repository: r})
	}

	pool := &work.Pool{
		Concurrency: d.Concurrency,
		Prepare: func(ctx context.Context, job work.Job) (work.Prepared, error) {
			db, err := d.Source.FindDatabase(ctx, job.Repository, d.Language)
			if err != nil && !errors.Is(err, catalog.ErrNoDatabase) {
				return work.Prepared{}, err
			}
			// A missing database is recorded rather than skipped: the absence is the
			// coverage measurement, and it stops the next pass re-probing this repository.
			return work.Prepared{Job: job, Commit: db.CommitOID}, nil
		},
		Consume: func(_ context.Context, p work.Prepared) (int, error) {
			entry := store.CatalogEntry{
				Repository:  p.Job.Repository,
				Language:    d.Language,
				CommitOID:   p.Commit,
				HasDatabase: p.Commit != "",
			}
			if err := d.Store.UpsertCatalogEntry(entry); err != nil {
				return 0, err
			}
			if entry.HasDatabase {
				return 1, nil
			}
			return 0, nil
		},
		OnEvent: d.OnEvent,
	}

	stats, err := pool.Run(ctx, jobs)
	if err != nil {
		return stats, fmt.Errorf("discovering databases: %w", err)
	}
	return stats, nil
}
