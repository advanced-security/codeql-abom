package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// QueryRun identifies one evaluation of one query against one repository's database.
//
// Every stored fact points at a run, so any row can be traced to the query and pack version
// that produced it. That traceability is what makes amending a query and re-running an
// auditable operation rather than an opaque data change.
type QueryRun struct {
	QueryID      string
	QueryVersion string
	PackVersion  string
	Repository   string
	DBCommit     string
}

func (q QueryRun) key() string {
	h := sha256.New()
	for _, part := range []string{
		q.QueryID, q.QueryVersion, q.PackVersion, q.Repository, q.DBCommit,
	} {
		fmt.Fprintf(h, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// RecordQueryRun returns the row id for a run, inserting it when absent.
func (s *Store) RecordQueryRun(q QueryRun) (int64, error) {
	if q.QueryID == "" || q.Repository == "" {
		return 0, fmt.Errorf("store: QueryRun needs QueryID and Repository")
	}
	key := q.key()

	if _, err := s.db.Exec(`
		INSERT INTO abom_query_run
			(run_key, query_id, query_version, pack_version, repository, db_commit, ran_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_key) DO UPDATE SET ran_at = excluded.ran_at`,
		key, q.QueryID, nullable(q.QueryVersion), nullable(q.PackVersion),
		q.Repository, nullable(q.DBCommit), now()); err != nil {
		return 0, fmt.Errorf("recording query run for %s: %w", q.QueryID, err)
	}

	var id int64
	if err := s.db.QueryRow(
		`SELECT row_id FROM abom_query_run WHERE run_key = ?`, key).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving query run for %s: %w", q.QueryID, err)
	}
	return id, nil
}

// Finding is one result from any query, upstream or ABOM's own.
type Finding struct {
	QueryRunID       int64
	RuleID           string
	RepositoryID     int64
	FilePath         string
	StartLine        int
	StartColumn      int
	EndLine          int
	EndColumn        int
	Message          string
	Severity         string
	SecuritySeverity float64
	Fingerprint      string
}

// AddFindings writes findings in one transaction, ignoring exact duplicates.
func (s *Store) AddFindings(findings []Finding) (int, error) {
	if len(findings) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO abom_finding
			(query_run_id, rule_id, repository_row_id, file_path, start_line, start_column,
			 end_line, end_column, message, severity, security_severity, fingerprint)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("preparing finding insert: %w", err)
	}
	defer stmt.Close()

	written := 0
	for _, f := range findings {
		if _, err := stmt.Exec(nullableID(f.QueryRunID), f.RuleID, f.RepositoryID,
			nullable(f.FilePath), f.StartLine, f.StartColumn, f.EndLine, f.EndColumn,
			nullable(f.Message), nullable(f.Severity), f.SecuritySeverity,
			f.Fingerprint); err != nil {
			return written, fmt.Errorf("inserting finding for %s: %w", f.RuleID, err)
		}
		written++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// LinkFindingsToNodes attributes findings to graph nodes by source location.
//
// This join is what lets graph traversal answer security questions: a finding recorded against
// a file position becomes a property of the dependency edge at that position.
func (s *Store) LinkFindingsToNodes() (int, error) {
	// A finding on a uses: line belongs to that step.
	res, err := s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, e.src_node_row_id
		FROM abom_finding f
		JOIN abom_edge e
		  ON e.repository_row_id = f.repository_row_id
		 AND e.file_path = f.file_path
		 AND e.start_line = f.start_line
		WHERE true
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to nodes: %w", err)
	}
	n, _ := res.RowsAffected()

	// Most rules report on a workflow or a job rather than on a uses: line, so anything left
	// over attaches to the file it was reported in. Without this the majority of a suite's
	// output would be invisible to every view built on the graph.
	res, err = s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id
		FROM abom_finding f
		JOIN abom_node n
		  ON n.repository_row_id = f.repository_row_id
		 AND n.node_kind IN ('workflow', 'composite_action')
		 AND n.path = f.file_path
		WHERE NOT EXISTS (
		        SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to workflows: %w", err)
	}
	m, _ := res.RowsAffected()

	// A reusable workflow or a composite action is represented by the container holding its
	// jobs, so nothing at its path is a workflow node and the join above finds nothing. Walk out
	// of the jobs and steps recorded there to whatever holds them. Nesting is at most
	// step -> job -> container, so two levels reach it.
	//
	// Findings in a file the graph never modeled are left alone: a graph built from
	// dependencies has no node for a file that names none, and inventing one would report
	// something that was never extracted.
	res, err = s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, (
		        SELECT CASE
		                 WHEN n.node_kind NOT IN ('job','step') THEN n.row_id
		                 WHEN p.node_kind NOT IN ('job','step') THEN p.row_id
		                 WHEN gp.row_id IS NOT NULL             THEN gp.row_id
		                 ELSE COALESCE(p.row_id, n.row_id)
		               END
		        FROM abom_node n
		        LEFT JOIN abom_node p  ON p.row_id  = n.container_node_row_id
		        LEFT JOIN abom_node gp ON gp.row_id = p.container_node_row_id
		        WHERE n.repository_row_id = f.repository_row_id AND n.path = f.file_path
		        ORDER BY n.row_id LIMIT 1)
		FROM abom_finding f
		WHERE NOT EXISTS (
		        SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)
		  AND EXISTS (
		        SELECT 1 FROM abom_node n
		        WHERE n.repository_row_id = f.repository_row_id AND n.path = f.file_path)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to the container holding their file: %w", err)
	}
	c, _ := res.RowsAffected()
	return int(n + m + c), nil
}
