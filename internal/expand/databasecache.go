package expand

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// DatabaseCache retains the CodeQL databases a crawl builds, so a query can be run across a
// corpus that already exists instead of extracting one repository at a time.
//
// Identity is the repository, commit, checkout filter and analysis mode -- the tuple
// store.DatabaseIdentity names and abom_query_run hashes. The filter is part of it because a
// commit alone does not name a tree, and the mode because one rule id means different things in
// the two shapes.
//
// This owns the disk and the store owns the index. They are written in that order, so an
// interrupted round leaves a directory nothing refers to, which a sweep can reclaim -- rather
// than a row naming a directory that was never promoted, which is a hit that cannot be served.
//
// A nil *DatabaseCache is a disabled cache, and every method tolerates one.
type DatabaseCache struct {
	// Dir holds the cache, outside WorkDir so databases outlive the round that built them.
	Dir string
	// MaxBytes is the ceiling trimming enforces. Zero or less retains without bound.
	MaxBytes int64
}

// databaseDir is the database within an entry. The same layout staged and in place, so promoting
// a finished build is a rename rather than a copy.
func databaseDir(entry string) string { return filepath.Join(entry, "db") }

// Stage opens a directory for a build, and the path the database goes at.
//
// A build writes here and is promoted once it succeeds, so a partial extraction is never visible
// under an identity and an interrupted one cannot be mistaken for an analyzed repository. The
// database path is returned rather than created: codeql refuses to write into a directory that
// already exists.
func (c *DatabaseCache) Stage() (string, string, error) {
	if c == nil || c.Dir == "" {
		return "", "", fmt.Errorf("database cache: no directory configured")
	}
	root := filepath.Join(c.Dir, stagingDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", fmt.Errorf("database cache: preparing staging: %w", err)
	}
	entry, err := os.MkdirTemp(root, "build-")
	if err != nil {
		return "", "", fmt.Errorf("database cache: staging a build: %w", err)
	}
	return entry, databaseDir(entry), nil
}

// Reuse reports the database held at an identity, if one has been promoted there.
func (c *DatabaseCache) Reuse(id store.DatabaseIdentity) (string, bool) {
	if c == nil || c.Dir == "" {
		return "", false
	}
	entry, ok := c.entry(id)
	if !ok {
		return "", false
	}
	db := databaseDir(entry)
	if info, err := os.Stat(db); err != nil || !info.IsDir() {
		return "", false
	}
	return db, true
}

// Retain promotes a staged build to the identity it was built for, returning the database in
// place and what it occupies.
//
// The rename is within one filesystem and so is atomic: a reader finds a complete database or
// none. A build at an identity that already holds one replaces it, matching the store's upsert
// so the row and the directory cannot describe different databases. Serializing two builds of
// one identity is the checkout hold's job one level down, which is where they are already
// serialized by the commit they share.
func (c *DatabaseCache) Retain(
	staged string, id store.DatabaseIdentity,
) (string, int64, error) {
	if c == nil || c.Dir == "" {
		return "", 0, fmt.Errorf("database cache: no directory configured")
	}
	entry, ok := c.entry(id)
	if !ok {
		return "", 0, fmt.Errorf("database cache: %q at %q in %q mode cannot name an entry",
			id.Repository, id.Commit, id.Mode)
	}
	size, err := treeBytes(databaseDir(staged))
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return "", 0, fmt.Errorf("database cache: preparing %s: %w", filepath.Dir(entry), err)
	}
	if err := os.RemoveAll(entry); err != nil {
		return "", 0, fmt.Errorf("database cache: replacing %s: %w", entry, err)
	}
	if err := os.Rename(staged, entry); err != nil {
		return "", 0, fmt.Errorf("database cache: retaining %s at %s: %w",
			id.Repository, id.Commit, err)
	}
	return databaseDir(entry), size, nil
}

// Discard removes a database and the entry holding it, which is how eviction reclaims disk.
// The path comes back from the store, so it is checked against the cache root before anything is
// removed: a wrong row must not become a wrong deletion.
func (c *DatabaseCache) Discard(database string) error {
	if c == nil || c.Dir == "" {
		return nil
	}
	entry := filepath.Dir(database)
	if !c.contains(entry) {
		return fmt.Errorf("database cache: %s is not inside %s", database, c.Dir)
	}
	if err := os.RemoveAll(entry); err != nil {
		return fmt.Errorf("database cache: discarding %s: %w", entry, err)
	}
	return nil
}

// DiscardStaged removes a build that will never be promoted.
//
// Called on the failure path, where what to remove is known exactly. ReclaimStaging exists for
// the crash that never reaches here, not as the ordinary way a failed build is cleaned up:
// waiting out the abandonment window for every refused repository is hours of occupancy the
// ceiling cannot see.
func (c *DatabaseCache) DiscardStaged(staged string) {
	if c == nil || c.Dir == "" || staged == "" || !c.contains(staged) {
		return
	}
	os.RemoveAll(staged)
}

// ReclaimStaging removes staged builds old enough that nothing can still be writing them, and
// reports how many it took.
//
// A killed crawl leaves a staged database nothing will ever promote, and the ceiling is computed
// from the rows naming promoted databases, so that occupancy is invisible to it. The age bound is
// what keeps this from deleting a database out from under the extractor writing it.
func (c *DatabaseCache) ReclaimStaging() (int, error) {
	if c == nil || c.Dir == "" {
		return 0, nil
	}
	root := filepath.Join(c.Dir, stagingDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("database cache: reading %s: %w", root, err)
	}
	removed := 0
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) < stagingLifetime {
			continue
		}
		// One unremovable directory is not a reason to abandon the rest.
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}

// entry is where one identity's database lives, and whether it can be named at all.
func (c *DatabaseCache) entry(id store.DatabaseIdentity) (string, bool) {
	owner, repo, ok := splitRepository(id.Repository)
	if !ok || !store.ValidAnalysisMode(id.Mode) {
		return "", false
	}
	ownerSegment, ok := cacheSegment(owner)
	if !ok {
		return "", false
	}
	repoSegment, ok := cacheSegment(repo)
	if !ok {
		return "", false
	}
	// Commit, filter and shape in one segment, for the reason the checkout cache joins the first
	// two: a single slot per commit would have each build delete a database that answers a
	// different question. The filter is an id rather than the patterns — `filterID` hashes the
	// pattern set — so all three parts are path-safe and the joined name reads back.
	buildSegment, ok := cacheSegment(id.Commit + "-" + id.SourceFilter + "-" + id.Mode)
	if !ok {
		return "", false
	}
	return filepath.Join(c.Dir, ownerSegment, repoSegment, buildSegment), true
}

// contains reports whether a path is under the cache root.
func (c *DatabaseCache) contains(path string) bool {
	root, err := filepath.Abs(c.Dir)
	if err != nil {
		return false
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, "..")
}

// splitRepository separates owner/repo, refusing anything that is not exactly that.
//
// The name arrives from somebody else's workflow file. Refused rather than rewritten, because a
// rewritten name is a different repository.
func splitRepository(full string) (string, string, bool) {
	owner, repo, ok := strings.Cut(full, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return "", "", false
	}
	return owner, repo, true
}
