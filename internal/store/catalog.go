package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// CatalogEntry records what discovery found for one repository.
type CatalogEntry struct {
	Repository  string
	Language    string
	CommitOID   string
	SizeBytes   int64
	CreatedAt   string
	UpdatedAt   string
	HasDatabase bool
}

// UpsertCatalogEntry records a discovery result, replacing any previous one.
func (s *Store) UpsertCatalogEntry(e CatalogEntry) error {
	_, err := s.db.Exec(`
		INSERT INTO abom_database_catalog
			(repository, language, commit_oid, size_bytes, created_at, updated_at,
			 has_database, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(repository) DO UPDATE SET
			language     = excluded.language,
			commit_oid   = excluded.commit_oid,
			size_bytes   = excluded.size_bytes,
			created_at   = excluded.created_at,
			updated_at   = excluded.updated_at,
			has_database = excluded.has_database,
			checked_at   = excluded.checked_at`,
		e.Repository, e.Language, nullable(e.CommitOID), e.SizeBytes,
		nullable(e.CreatedAt), nullable(e.UpdatedAt), e.HasDatabase, now())
	if err != nil {
		return fmt.Errorf("recording catalog entry for %s: %w", e.Repository, err)
	}
	return nil
}

// CatalogRepositories is what the ingestion pass consumes, so the expensive per-repository
// discovery is paid once rather than on every run.
func (s *Store) CatalogRepositories(language string) ([]string, error) {
	return queryAll(s, "listing catalog", func(r *sql.Rows) (string, error) {
		var repo string
		return repo, r.Scan(&repo)
	}, `
		SELECT repository FROM abom_database_catalog
		WHERE has_database = 1 AND language = ?
		ORDER BY repository`, language)
}

// CatalogCoverage reports how many repositories were probed and how many had a database.
func (s *Store) CatalogCoverage(language string) (withDB, total int, err error) {
	err = s.db.QueryRow(`
		SELECT COALESCE(SUM(has_database), 0), COUNT(*)
		FROM abom_database_catalog WHERE language = ?`, language).Scan(&withDB, &total)
	if err != nil {
		return 0, 0, fmt.Errorf("computing coverage: %w", err)
	}
	return withDB, total, nil
}

// CatalogCommit returns the commit recorded for a repository, if discovery found a database.
func (s *Store) CatalogCommit(repository, language string) (string, bool, error) {
	var commit sql.NullString
	err := s.db.QueryRow(`
		SELECT commit_oid FROM abom_database_catalog
		WHERE repository = ? AND language = ? AND has_database = 1`,
		repository, language).Scan(&commit)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading catalog commit: %w", err)
	}
	return commit.String, true, nil
}
