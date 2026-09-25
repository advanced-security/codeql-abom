// Package discover finds which repositories in an organization have a CodeQL database.
//
// GitHub's code scanning endpoints are per-repository, so establishing the catalog is itself a
// large parallel workload. Batched workflow evidence narrows Actions probes without treating an
// unprobed repository as one without a database. Ingestion then works from the measured catalog.
package discover

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Source enumerates repositories and looks up their databases.
type Source interface {
	ListAll(ctx context.Context, account string) (catalog.Listing, error)
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

// Run lists the account and records catalog entries only for repositories actually probed.
func (d *Discoverer) Run(ctx context.Context, org string) (work.Stats, error) {
	found, err := d.Source.ListAll(ctx, org)
	if err != nil {
		return work.Stats{}, err
	}
	repos := make([]string, 0, len(found.Repositories))
	for _, repository := range found.Repositories {
		if strings.EqualFold(d.Language, "actions") && repository.HasWorkflows != nil && !*repository.HasWorkflows {
			continue
		}
		repos = append(repos, repository.FullName)
	}
	stats, err := d.RunRepositories(ctx, repos)
	stats.Skipped += len(found.Repositories) - len(repos)
	return stats, err
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
