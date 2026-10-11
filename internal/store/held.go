package store

import (
	"database/sql"
	"fmt"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// RecordHeldMemberResult moves a held run's member and keeps what its queries reported there, in
// one transaction so the counts and the list always describe the same pass. A held result is a
// Finding with no repository row and no evaluation named: nobody has converted it yet.
func (s *Store) RecordHeldMemberResult(
	runID int64, id DatabaseIdentity, r MemberResult, held []Finding,
) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	isHeld, err := runIsHeld(tx, runID)
	if err != nil {
		return err
	}
	// A run's results are findings, and a list held beside them would be a second account of them.
	if !isHeld {
		return fmt.Errorf("run %d stores what it finds as findings, so it holds nothing", runID)
	}
	if err := linksItsOwnKind(tx, runID, isHeld, r.QueryRunID, id); err != nil {
		return err
	}
	if err := updateMember(tx, runID, id, r); err != nil {
		return err
	}
	// Only a pass that counted replaces the list, as only one that counted replaces the counts.
	if r.Counted {
		if err := replaceHeld(tx, runID, id, held); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s: %w", id.Repository, err)
	}
	return nil
}

// linksItsOwnKind refuses a member linked to an evaluation of the other kind. A findings run linked
// to a held evaluation would list sightings nobody wrote; a held run linked to a full one would let
// the next run skip on an answer whose findings are held.
func linksItsOwnKind(tx *transaction, runID int64, runHeld bool, evaluation int64, id DatabaseIdentity) error {
	if evaluation == 0 {
		return nil
	}
	var evaluationHeld bool
	if err := tx.QueryRow(`SELECT held FROM abom_query_run WHERE row_id = ?`, evaluation).
		Scan(&evaluationHeld); err != nil {
		return fmt.Errorf("reading evaluation %d of %s: %w", evaluation, id.Repository, err)
	}
	switch {
	case runHeld && !evaluationHeld:
		return fmt.Errorf("run %d holds what it finds, and evaluation %d of %s stored findings",
			runID, evaluation, id.Repository)
	case !runHeld && evaluationHeld:
		return fmt.Errorf("run %d stores findings, and evaluation %d of %s held its results",
			runID, evaluation, id.Repository)
	}
	return nil
}

// replaceHeld replaces rather than adds, or analyzing again would list each result twice.
func replaceHeld(tx *transaction, runID int64, id DatabaseIdentity, held []Finding) error {
	if _, err := tx.Exec(`
		DELETE FROM abom_held_result
		WHERE run_id = ? AND repository = ? AND db_commit = ?
		  AND source_filter = ? AND analysis_mode = ?`,
		runID, id.Repository, id.Commit, id.SourceFilter, id.Mode); err != nil {
		return fmt.Errorf("clearing what %s held: %w", id.Repository, err)
	}
	insert, err := tx.Prepare(`
		INSERT INTO abom_held_result
			(run_id, repository, db_commit, source_filter, analysis_mode, rule_id, file_path,
			 start_line, start_column, end_line, end_column, message, severity, fingerprint,
			 result_hash, provider, security_severity)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing what %s held: %w", id.Repository, err)
	}
	defer insert.Close()
	for _, f := range held {
		if _, err := insert.Exec(runID, id.Repository, id.Commit, id.SourceFilter, id.Mode,
			f.RuleID, f.FilePath, f.StartLine, f.StartColumn, f.EndLine, f.EndColumn, f.Message,
			f.Severity, f.Fingerprint, f.ResultHash, f.Provider, f.SecuritySeverity); err != nil {
			return fmt.Errorf("recording what %s held: %w", id.Repository, err)
		}
	}
	return nil
}

// memberHeld lists what one member of a held run reported, as a run's findings are.
func (s *Store) memberHeld(
	runID int64, repository, queryID string, limit int, out MemberFindings,
) (MemberFindings, error) {
	out.Held = true
	// A converted result is found through its finding's identity, which outlives the held row.
	rows, err := s.db.Query(`
		SELECT h.row_id, h.rule_id, h.file_path, h.start_line, h.start_column, h.message, h.severity,
		       COALESCE(c.finding_row_id, 0), COALESCE(c.converted_by, ''), COALESCE(c.converted_at, ''),
		       NULLIF(h.security_severity, 0)
		FROM abom_held_result h
		LEFT JOIN repository rp ON rp.repository_full_name = h.repository
		LEFT JOIN abom_finding_any_mode f
		  ON f.rule_id = h.rule_id AND f.repository_row_id = rp.row_id
		 AND COALESCE(f.file_path, '') = h.file_path AND f.start_line = h.start_line
		 AND f.start_column = h.start_column AND f.fingerprint = h.fingerprint
		LEFT JOIN abom_held_conversion c ON c.run_id = h.run_id AND c.finding_row_id = f.row_id
		WHERE h.run_id = ? AND h.repository = ? AND (? = '' OR h.rule_id = ?)
		ORDER BY h.file_path, h.start_line, h.start_column, h.rule_id, h.row_id
		LIMIT ?`, runID, repository, queryID, queryID, limit+1)
	if err != nil {
		return out, fmt.Errorf("listing what %s held in run %d: %w", repository, runID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var f RunFinding
		if err := rows.Scan(&f.HeldResultID, &f.RuleID, &f.FilePath, &f.StartLine, &f.StartColumn,
			&f.Message, &f.Severity, &f.FindingRowID, &f.ConvertedBy, &f.ConvertedAt,
			&f.SecuritySeverity); err != nil {
			return out, fmt.Errorf("scanning what %s held: %w", repository, err)
		}
		f.Band = viewspec.BandOf(f.SecuritySeverity)
		if len(out.Findings) == limit {
			out.Truncated = true
			break
		}
		out.Findings = append(out.Findings, f)
	}
	return out, rows.Err()
}

func runIsHeld(tx *transaction, runID int64) (bool, error) {
	var held bool
	err := tx.QueryRow(`SELECT held FROM abom_analysis_run WHERE row_id = ?`, runID).Scan(&held)
	if err == sql.ErrNoRows {
		return false, fmt.Errorf("no run %d", runID)
	}
	if err != nil {
		return false, fmt.Errorf("reading run %d: %w", runID, err)
	}
	return held, nil
}

// migrateHeldNames carries a store written while held runs were called predictions onto the names
// that say what they are. Before the schema, which would otherwise create those names empty.
func (s *Store) migrateHeldNames() error {
	has, err := s.hasColumn("abom_analysis_run", "prediction")
	if err != nil {
		return err
	}
	if has {
		if _, err := s.db.Exec(`ALTER TABLE abom_analysis_run RENAME COLUMN prediction TO held`); err != nil {
			return fmt.Errorf("renaming abom_analysis_run.prediction: %w", err)
		}
	}
	var old int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'abom_predicted_result'`).Scan(&old); err != nil {
		return fmt.Errorf("looking for abom_predicted_result: %w", err)
	}
	if old == 0 {
		return nil
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_predicted_member`); err != nil {
		return fmt.Errorf("dropping idx_predicted_member: %w", err)
	}
	if _, err := s.db.Exec(`ALTER TABLE abom_predicted_result RENAME TO abom_held_result`); err != nil {
		return fmt.Errorf("renaming abom_predicted_result: %w", err)
	}
	return nil
}

// migrateEvidenceKey rebuilds evidence so multiple held picks in one repository stay distinct.
func (s *Store) migrateEvidenceKey() error {
	has, err := s.hasColumn("abom_query_request_evidence", "held_result_id")
	if err != nil || has {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning the evidence rebuild: %w", err)
	}
	defer tx.Rollback()
	if err := rebuildTable(tx, "abom_query_request_evidence", evidenceTable,
		"request_id, run_id, repository, finding_row_id, kind"); err != nil {
		return err
	}
	return tx.Commit()
}
