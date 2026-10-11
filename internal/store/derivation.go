package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// DerivationWriter atomically persists a derivation; rollback is required unless commit succeeds.
type DerivationWriter interface {
	ClearReachMark() error
	MarkReach(version string, inputs map[int64]ReachInput, whole bool) error
	ClearReach() (int, error)
	DeleteReach(node int64) (int, error)
	PrepareReach() error
	WriteReach(node int64, reach ReachWrite) (int, error)
	ClearExposure() (int, error)
	DeleteExposure(node int64) (int, error)
	PrepareExposure() error
	WriteExposure(node int64, ghsa string, routes []ExposureRoute) (int, error)
	DeriveAdvisoryRoutes() (int, error)
	Commit() error
	Rollback() error
}

type ReachCounts struct{ Hops, Steps, Jobs, Workflows int }

// ReachWrite contains measured counts, not the graph traversal that produced them.
type ReachWrite struct {
	Repositories        map[int64]ReachCounts
	Versions            map[string]map[int64]int
	MaxDepth, RefsInUse int
}

type ExposureRoute struct {
	RepositoryID int64
	Hops         int
	Path         []string
}

type derivationWriter struct {
	tx                                       *transaction
	rollup, repositories, versions, exposure *statement
}

func (s *Store) BeginDerivation() (DerivationWriter, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	return &derivationWriter{tx: tx}, nil
}

func (w *derivationWriter) ClearReachMark() error { return ClearReachMark(w.tx) }
func (w *derivationWriter) MarkReach(version string, inputs map[int64]ReachInput, whole bool) error {
	return MarkReach(w.tx, version, inputs, whole)
}
func (w *derivationWriter) DeriveAdvisoryRoutes() (int, error) { return DeriveAdvisoryRoutes(w.tx) }
func (w *derivationWriter) Commit() error                      { w.close(); return w.tx.Commit() }
func (w *derivationWriter) Rollback() error                    { w.close(); return w.tx.Rollback() }

func (w *derivationWriter) close() {
	for _, stmt := range []*statement{w.rollup, w.repositories, w.versions, w.exposure} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
}

var reachTables = []string{"abom_blast_radius", "abom_node_reach", "abom_node_version_reach"}

func affectedRows(res sql.Result) int {
	n, err := res.RowsAffected()
	if err != nil {
		return 0
	}
	return int(n)
}

func (w *derivationWriter) ClearReach() (int, error) {
	n := 0
	for _, table := range reachTables {
		res, err := w.tx.Exec(`DELETE FROM ` + table)
		if err != nil {
			return n, fmt.Errorf("clearing %s: %w", table, err)
		}
		n += affectedRows(res)
	}
	return n, nil
}

func (w *derivationWriter) DeleteReach(node int64) (int, error) {
	n := 0
	for _, table := range reachTables {
		res, err := w.tx.Exec(`DELETE FROM `+table+` WHERE node_row_id = ?`, node)
		if err != nil {
			return n, fmt.Errorf("retracting %s for node %d: %w", table, node, err)
		}
		n += affectedRows(res)
	}
	return n, nil
}

func (w *derivationWriter) PrepareReach() error {
	if w.rollup != nil {
		return nil
	}
	var err error
	if w.rollup, err = w.tx.Prepare(`
		INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_steps, dependent_jobs,
			dependent_workflows, max_depth, refs_in_use)
		VALUES (?, ?, ?, ?, ?, ?, ?)`); err != nil {
		return fmt.Errorf("preparing the blast radius insert: %w", err)
	}
	if w.repositories, err = w.tx.Prepare(
		`INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops, steps, jobs, workflows)
		 VALUES (?, ?, ?, ?, ?, ?)`); err != nil {
		return fmt.Errorf("preparing the reach insert: %w", err)
	}
	if w.versions, err = w.tx.Prepare(`INSERT INTO abom_node_version_reach
		(node_row_id, commit_sha, repository_row_id, hops) VALUES (?, ?, ?, ?)`); err != nil {
		return fmt.Errorf("preparing the reach-by-version insert: %w", err)
	}
	return nil
}

func (w *derivationWriter) WriteReach(target int64, r ReachWrite) (int, error) {
	if err := w.PrepareReach(); err != nil {
		return 0, err
	}
	var steps, jobs, workflows int
	rows := 0
	for repo, d := range r.Repositories {
		steps, jobs, workflows = steps+d.Steps, jobs+d.Jobs, workflows+d.Workflows
		if _, err := w.repositories.Exec(target, repo, d.Hops, d.Steps, d.Jobs, d.Workflows); err != nil {
			return rows, fmt.Errorf("writing reach for node %d: %w", target, err)
		}
		rows++
	}
	if _, err := w.rollup.Exec(target, len(r.Repositories), steps, jobs, workflows,
		r.MaxDepth, r.RefsInUse); err != nil {
		return rows, fmt.Errorf("writing blast radius for node %d: %w", target, err)
	}
	rows++
	for commit, repos := range r.Versions {
		for repo, hops := range repos {
			if _, err := w.versions.Exec(target, commit, repo, hops); err != nil {
				return rows, fmt.Errorf("writing reach for node %d at %q: %w", target, commit, err)
			}
			rows++
		}
	}
	return rows, nil
}

func (w *derivationWriter) ClearExposure() (int, error) {
	res, err := w.tx.Exec(`DELETE FROM abom_exposure`)
	if err != nil {
		return 0, fmt.Errorf("clearing exposure: %w", err)
	}
	return affectedRows(res), nil
}

func (w *derivationWriter) DeleteExposure(node int64) (int, error) {
	res, err := w.tx.Exec(`DELETE FROM abom_exposure WHERE node_row_id = ?`, node)
	if err != nil {
		return 0, fmt.Errorf("retracting exposure through node %d: %w", node, err)
	}
	return affectedRows(res), nil
}

func (w *derivationWriter) PrepareExposure() error {
	if w.exposure != nil {
		return nil
	}
	var err error
	w.exposure, err = w.tx.Prepare(`
		INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repository_row_id, node_row_id, ghsa_id) DO UPDATE SET
			hops = excluded.hops, path = excluded.path`)
	if err != nil {
		return fmt.Errorf("preparing exposure insert: %w", err)
	}
	return nil
}

func (w *derivationWriter) WriteExposure(node int64, ghsa string, routes []ExposureRoute) (int, error) {
	if err := w.PrepareExposure(); err != nil {
		return 0, err
	}
	rows := 0
	for _, e := range routes {
		path, err := json.Marshal(e.Path)
		if err != nil {
			return rows, fmt.Errorf("encoding path: %w", err)
		}
		if _, err := w.exposure.Exec(e.RepositoryID, node, ghsa, e.Hops, string(path)); err != nil {
			return rows, fmt.Errorf("recording exposure of %d to %s: %w", e.RepositoryID, ghsa, err)
		}
		rows++
	}
	return rows, nil
}

var _ DerivationWriter = (*derivationWriter)(nil)
