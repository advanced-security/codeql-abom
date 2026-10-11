package store

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/lockfile"
)

// lockCoverageDDL records what a repository's Actions lockfile said at one commit.
//
// Keyed by repository, commit and workflow path because coverage is per workflow: a lockfile can
// exist and say nothing about a given workflow, which is a different fact from the repository
// having no lockfile at all. The repository-level answer -- no lockfile, or one this consumer
// cannot read -- is the row whose workflow path is empty.
//
// Nothing here is a verdict. A recorded coverage state says what the lockfile claims, never that
// the selection was verified against GitHub or that the workflow is safe.
const lockCoverageDDL = `
CREATE TABLE IF NOT EXISTS abom_lock_coverage (
  repository    TEXT NOT NULL,
  commit_sha    TEXT NOT NULL,
  -- Empty for the repository-level state, so the key never contains NULL.
  workflow_path TEXT NOT NULL DEFAULT '',
  coverage      TEXT NOT NULL,
  -- The version as written, retained even when it is the reason the file is unreadable.
  schema_version TEXT NOT NULL DEFAULT '',
  -- sha256 over the original bytes, taken before parsing.
  lock_digest   TEXT NOT NULL DEFAULT '',
  reason        TEXT NOT NULL DEFAULT '',
  seen_at       TEXT NOT NULL,
  -- 1 once the reading also kept the lock's graph below. A covered lock read before that has
  -- coverage and no entries, which is not the same as a lock that has none.
  graph_recorded INTEGER NOT NULL DEFAULT 0,
  -- The parser release and interpretation that read it. Empty on a reading made before this was
  -- recorded, which no current reader matches.
  reader        TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (repository, commit_sha, workflow_path)
);
-- The measurement reads by state across the corpus, not by repository.
CREATE INDEX IF NOT EXISTS idx_lock_coverage_state
  ON abom_lock_coverage(coverage);

-- What each pin key of one repository's lock selects at one commit. Native graph evidence, kept
-- apart from source declarations: a pin here is not an edge and names no declaring line.
CREATE TABLE IF NOT EXISTS abom_lock_dependency (
  repository    TEXT NOT NULL COLLATE NOCASE,
  commit_sha    TEXT NOT NULL,
  -- OWNER/REPO@REF, owner and repository in lower case, the ref as written.
  pin_key       TEXT NOT NULL,
  owner         TEXT NOT NULL,
  repo          TEXT NOT NULL,
  -- What was asked for, and the ref the tool resolved the commit from; they differ whenever an
  -- entry is keyed by commit and narrowed to a tag.
  ref           TEXT NOT NULL,
  resolved_from TEXT NOT NULL DEFAULT '',
  -- Empty when the entry's commit is not algorithm-prefixed, which then selects nothing.
  digest_algorithm TEXT NOT NULL DEFAULT '',
  digest        TEXT NOT NULL DEFAULT '',
  -- GitHub's numeric ids as the lock records them: attributed evidence that detects a transfer,
  -- never an ownership declaration.
  owner_id      INTEGER,
  repo_id       INTEGER,
  PRIMARY KEY (repository, commit_sha, pin_key)
);
-- A nested selection starts from the composite being analyzed and asks which locks reached it.
CREATE INDEX IF NOT EXISTS idx_lock_dependency_target
  ON abom_lock_dependency(owner, repo);

-- The lock's DAG: an entry's direct nested dependencies. The only record of which commit a
-- composite action's own uses: run at for this consumer.
CREATE TABLE IF NOT EXISTS abom_lock_use (
  repository TEXT NOT NULL COLLATE NOCASE,
  commit_sha TEXT NOT NULL,
  parent_pin TEXT NOT NULL,
  child_pin  TEXT NOT NULL,
  PRIMARY KEY (repository, commit_sha, parent_pin, child_pin)
);

-- The pins a workflow entry lists: the ones the workflow and its own local composites name. Not
-- a closure: what a remote composite pulls in is reachable through abom_lock_use alone.
CREATE TABLE IF NOT EXISTS abom_lock_workflow_pin (
  repository    TEXT NOT NULL COLLATE NOCASE,
  commit_sha    TEXT NOT NULL,
  workflow_path TEXT NOT NULL,
  pin_key       TEXT NOT NULL,
  PRIMARY KEY (repository, commit_sha, workflow_path, pin_key)
);`

// LockCoverage is one recorded coverage row.
type LockCoverage struct {
	WorkflowPath string
	Coverage     string
	Schema       string
	Digest       string
	Reason       string
}

// LockCoverageRows builds the rows for one repository from a survey of its tree.
//
// The file's schema, digest and reason ride on every row, so a reader that selects one workflow
// still knows which lockfile answered for it.
func LockCoverageRows(coverage, schema, digest, reason string, workflows []lockfile.WorkflowCoverage) []LockCoverage {
	rows := make([]LockCoverage, 0, len(workflows))
	for _, w := range workflows {
		row := LockCoverage{
			WorkflowPath: w.Path,
			Coverage:     string(w.Coverage),
			Schema:       schema,
			Digest:       digest,
		}
		// Only the repository-level row carries the reason; a workflow did not fail to parse.
		if w.Path == "" {
			row.Coverage, row.Reason = coverage, reason
		}
		rows = append(rows, row)
	}
	return rows
}

// LockDependency is one recorded entry of a lock's dependency map.
type LockDependency struct {
	Pin          string
	Owner        string
	Repo         string
	Ref          string
	ResolvedFrom string
	Algorithm    string
	Digest       string
	OwnerID      int64
	RepoID       int64
}

// LockUse is one edge of a lock's DAG: an entry and one of its direct nested dependencies.
type LockUse struct {
	Parent string
	Child  string
}

// LockWorkflowPin is one pin a workflow entry lists.
type LockWorkflowPin struct {
	WorkflowPath string
	Pin          string
}

// LockGraph is what a lock declares beyond coverage: every entry, its DAG, and which pins each
// workflow names.
type LockGraph struct {
	Dependencies []LockDependency
	Uses         []LockUse
	WorkflowPins []LockWorkflowPin
}

// LockGraphOf builds the graph to record from a lock that was read. Empty for a lock nothing
// could read, which selects nothing.
func LockGraphOf(lock lockfile.Lock) LockGraph {
	var g LockGraph
	for _, d := range lock.Dependencies() {
		g.Dependencies = append(g.Dependencies, LockDependency{
			Pin: d.Pin, Owner: d.Owner, Repo: d.Repo, Ref: d.Ref, ResolvedFrom: d.ResolvedFrom,
			Algorithm: d.Algorithm, Digest: d.Digest, OwnerID: d.OwnerID, RepoID: d.RepoID,
		})
		for _, child := range d.Uses {
			g.Uses = append(g.Uses, LockUse{Parent: d.Pin, Child: child})
		}
	}
	for _, w := range lock.WorkflowPins() {
		g.WorkflowPins = append(g.WorkflowPins, LockWorkflowPin{WorkflowPath: w.Path, Pin: w.Pin})
	}
	return g
}

// RecordLockReading notes what the lockfile said for one repository at one commit: its coverage
// and the graph it declares.
//
// Replaces that commit's rows rather than adding to them: a commit's lockfile is one file with
// one answer, so a second reading of the same commit is a correction, not a second opinion. One
// transaction, because coverage on record is what stops the commit being read again, and coverage
// without its graph would stop it for good.
func (s *Store) RecordLockReading(
	repository, commit, reader string, rows []LockCoverage, graph LockGraph,
) error {
	if repository == "" || commit == "" {
		return fmt.Errorf("store: RecordLockReading needs a repository and a commit")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording the lock for %s: %w", repository, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, table := range []string{
		"abom_lock_coverage", "abom_lock_dependency", "abom_lock_use", "abom_lock_workflow_pin",
	} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE repository = ? AND commit_sha = ?`,
			repository, commit); err != nil {
			return fmt.Errorf("clearing %s for %s at %s: %w", table, repository, commit, err)
		}
	}

	seen := now()
	for _, row := range rows {
		if _, err := tx.Exec(`
			INSERT INTO abom_lock_coverage
			    (repository, commit_sha, workflow_path, coverage, schema_version, lock_digest,
			     reason, seen_at, graph_recorded, reader)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
			repository, commit, row.WorkflowPath, row.Coverage,
			row.Schema, row.Digest, row.Reason, seen, reader); err != nil {
			return fmt.Errorf("recording lock coverage for %s at %s: %w", repository, commit, err)
		}
	}
	for _, d := range graph.Dependencies {
		if _, err := tx.Exec(`
			INSERT INTO abom_lock_dependency
			    (repository, commit_sha, pin_key, owner, repo, ref, resolved_from,
			     digest_algorithm, digest, owner_id, repo_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			repository, commit, d.Pin, d.Owner, d.Repo, d.Ref, d.ResolvedFrom,
			d.Algorithm, d.Digest, nullableID(d.OwnerID), nullableID(d.RepoID)); err != nil {
			return fmt.Errorf("recording lock entry %s for %s at %s: %w", d.Pin, repository, commit, err)
		}
	}
	for _, u := range graph.Uses {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO abom_lock_use (repository, commit_sha, parent_pin, child_pin)
			VALUES (?, ?, ?, ?)`, repository, commit, u.Parent, u.Child); err != nil {
			return fmt.Errorf("recording lock use %s for %s at %s: %w", u.Parent, repository, commit, err)
		}
	}
	for _, w := range graph.WorkflowPins {
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO abom_lock_workflow_pin (repository, commit_sha, workflow_path, pin_key)
			VALUES (?, ?, ?, ?)`, repository, commit, w.WorkflowPath, w.Pin); err != nil {
			return fmt.Errorf("recording lock pins of %s for %s at %s: %w",
				w.WorkflowPath, repository, commit, err)
		}
	}
	return tx.Commit()
}

// LockGraphAt reports the graph recorded for one repository's lock at one commit, ordered the
// way RecordLockReading receives it from LockGraphOf.
func (s *Store) LockGraphAt(repository, commit string) (LockGraph, error) {
	var g LockGraph
	deps, err := s.db.Query(`
		SELECT pin_key, owner, repo, ref, resolved_from, digest_algorithm, digest,
		       COALESCE(owner_id, 0), COALESCE(repo_id, 0)
		FROM abom_lock_dependency WHERE repository = ? AND commit_sha = ?
		ORDER BY pin_key`, repository, commit)
	if err != nil {
		return g, fmt.Errorf("reading lock entries of %s at %s: %w", repository, commit, err)
	}
	for deps.Next() {
		var d LockDependency
		if err := deps.Scan(&d.Pin, &d.Owner, &d.Repo, &d.Ref, &d.ResolvedFrom, &d.Algorithm,
			&d.Digest, &d.OwnerID, &d.RepoID); err != nil {
			deps.Close()
			return g, fmt.Errorf("reading lock entries of %s at %s: %w", repository, commit, err)
		}
		g.Dependencies = append(g.Dependencies, d)
	}
	deps.Close()
	if err := deps.Err(); err != nil {
		return g, fmt.Errorf("reading lock entries of %s at %s: %w", repository, commit, err)
	}

	uses, err := s.db.Query(`
		SELECT parent_pin, child_pin FROM abom_lock_use WHERE repository = ? AND commit_sha = ?
		ORDER BY parent_pin, child_pin`, repository, commit)
	if err != nil {
		return g, fmt.Errorf("reading lock uses of %s at %s: %w", repository, commit, err)
	}
	for uses.Next() {
		var u LockUse
		if err := uses.Scan(&u.Parent, &u.Child); err != nil {
			uses.Close()
			return g, fmt.Errorf("reading lock uses of %s at %s: %w", repository, commit, err)
		}
		g.Uses = append(g.Uses, u)
	}
	uses.Close()
	if err := uses.Err(); err != nil {
		return g, fmt.Errorf("reading lock uses of %s at %s: %w", repository, commit, err)
	}

	pins, err := s.db.Query(`
		SELECT workflow_path, pin_key FROM abom_lock_workflow_pin
		WHERE repository = ? AND commit_sha = ? ORDER BY workflow_path, rowid`, repository, commit)
	if err != nil {
		return g, fmt.Errorf("reading lock workflow pins of %s at %s: %w", repository, commit, err)
	}
	defer pins.Close()
	for pins.Next() {
		var w LockWorkflowPin
		if err := pins.Scan(&w.WorkflowPath, &w.Pin); err != nil {
			return g, fmt.Errorf("reading lock workflow pins of %s at %s: %w", repository, commit, err)
		}
		g.WorkflowPins = append(g.WorkflowPins, w)
	}
	return g, pins.Err()
}

// LockCoverageCounts reports how many rows hold each coverage state.
//
// A state with no rows is absent from the result rather than present as zero, because the caller
// cannot otherwise tell a state nothing reached from one this build does not produce.
func (s *Store) LockCoverageCounts() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT coverage, COUNT(*) FROM abom_lock_coverage GROUP BY coverage`)
	if err != nil {
		return nil, fmt.Errorf("counting lock coverage: %w", err)
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var state string
		var n int
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("reading lock coverage counts: %w", err)
		}
		counts[state] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading lock coverage counts: %w", err)
	}
	return counts, nil
}

// HasLockCoverage reports whether this repository's lockfile was read at this commit by reader.
//
// Asked before a reference is settled without a checkout: a commit analyzed before this table
// existed has no rows, and settling it again would leave the lockfile unread indefinitely. Every
// reading writes at least the repository-level row, including "no lockfile here", so absence of
// rows means the question was never asked rather than answered with nothing.
func (s *Store) HasLockCoverage(repository, commit, reader string) (bool, error) {
	if repository == "" || commit == "" {
		return false, nil
	}
	var found bool
	err := s.db.QueryRow(`
		SELECT EXISTS(
		  SELECT 1 FROM abom_lock_coverage
		  WHERE repository = ? AND commit_sha = ? AND workflow_path = ''
		    AND `+readByCurrentReader+`)`,
		repository, commit, reader).Scan(&found)
	if err != nil {
		return false, fmt.Errorf("reading lock coverage for %s at %s: %w", repository, commit, err)
	}
	return found, nil
}

// readByCurrentReader holds for a repository-level reading this reader need not repeat. No
// lockfile is an answer no reader can change; anything else another reader recorded — or a
// reading older than the column, which names none — may read differently now, and is repeated.
const readByCurrentReader = `(coverage = 'not_onboarded' OR reader = ?)`

// RequeueLocksReadByAnotherReader puts back the dependencies whose commit's lock another reader
// read, so each is read again.
//
// Only the checkout is repeated: every evaluation of an unchanged commit is already on record,
// so a requeued row is settled from the ledger once its lock has been read. Nothing is requeued
// for a commit with no lockfile, and nothing that has never been analyzed, which reads its lock
// on the way anyway.
func (s *Store) RequeueLocksReadByAnotherReader(reader string) (int, error) {
	if reader == "" {
		return 0, fmt.Errorf("store: RequeueLocksReadByAnotherReader needs the current reader")
	}
	res, err := s.db.Exec(requeueDone+`
		AND COALESCE(resolved_sha, '') <> ''
		AND EXISTS (
		  SELECT 1 FROM abom_lock_coverage c
		  WHERE c.repository = abom_frontier.owner || '/' || abom_frontier.repo
		    AND c.commit_sha = abom_frontier.resolved_sha AND c.workflow_path = ''
		    AND NOT `+readByCurrentReader+`)`, reader)
	if err != nil {
		return 0, fmt.Errorf("requeueing locks another reader read: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
