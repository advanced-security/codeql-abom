package store

import (
	"database/sql"
	"fmt"
)

// PredictedResult is one result a prediction's query would report in a member. It is not a
// finding: nobody has adopted the query that reported it.
type PredictedResult struct {
	RuleID      string
	FilePath    string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
	Message     string
	Severity    string
	Fingerprint string
	ResultHash  string
}

// RecordPredictedMemberResult moves a prediction's member and holds what it would report there, in
// one transaction so the counts and the list always describe the same pass.
func (s *Store) RecordPredictedMemberResult(
	runID int64, id DatabaseIdentity, r MemberResult, predicted []PredictedResult,
) error {
	if r.QueryRunID != 0 {
		return fmt.Errorf("run %d is a prediction, which stores no evaluation of %s", runID, id.Repository)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	isPrediction, err := runIsPrediction(tx, runID)
	if err != nil {
		return err
	}
	// A run's results are findings, and a list held beside them would be a second account of them.
	if !isPrediction {
		return fmt.Errorf("run %d is not a prediction, so what it found is stored as findings", runID)
	}
	if err := updateMember(tx, runID, id, r); err != nil {
		return err
	}
	// Only a pass that counted replaces the list, as only one that counted replaces the counts.
	if r.Counted {
		if err := replacePredicted(tx, runID, id, predicted); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s: %w", id.Repository, err)
	}
	return nil
}

// replacePredicted replaces rather than adds, or predicting again would list each result twice.
func replacePredicted(tx *sql.Tx, runID int64, id DatabaseIdentity, predicted []PredictedResult) error {
	if _, err := tx.Exec(`
		DELETE FROM abom_predicted_result
		WHERE run_id = ? AND repository = ? AND db_commit = ?
		  AND source_filter = ? AND analysis_mode = ?`,
		runID, id.Repository, id.Commit, id.SourceFilter, id.Mode); err != nil {
		return fmt.Errorf("clearing what %s would report: %w", id.Repository, err)
	}
	insert, err := tx.Prepare(`
		INSERT INTO abom_predicted_result
			(run_id, repository, db_commit, source_filter, analysis_mode, rule_id, file_path,
			 start_line, start_column, end_line, end_column, message, severity, fingerprint,
			 result_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing what %s would report: %w", id.Repository, err)
	}
	defer insert.Close()
	for _, p := range predicted {
		if _, err := insert.Exec(runID, id.Repository, id.Commit, id.SourceFilter, id.Mode,
			p.RuleID, p.FilePath, p.StartLine, p.StartColumn, p.EndLine, p.EndColumn, p.Message,
			p.Severity, p.Fingerprint, p.ResultHash); err != nil {
			return fmt.Errorf("recording what %s would report: %w", id.Repository, err)
		}
	}
	return nil
}

// memberPredictions lists what one member of a prediction would report, as a run's findings are.
func (s *Store) memberPredictions(
	runID int64, repository, queryID string, limit int, out MemberFindings,
) (MemberFindings, error) {
	out.Predicted = true
	rows, err := s.db.Query(`
		SELECT rule_id, file_path, start_line, start_column, message, severity
		FROM abom_predicted_result
		WHERE run_id = ? AND repository = ? AND (? = '' OR rule_id = ?)
		ORDER BY file_path, start_line, start_column, rule_id, row_id
		LIMIT ?`, runID, repository, queryID, queryID, limit+1)
	if err != nil {
		return out, fmt.Errorf("listing what %s would report in run %d: %w", repository, runID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var f RunFinding
		if err := rows.Scan(&f.RuleID, &f.FilePath, &f.StartLine, &f.StartColumn, &f.Message,
			&f.Severity); err != nil {
			return out, fmt.Errorf("scanning what %s would report: %w", repository, err)
		}
		if len(out.Findings) == limit {
			out.Truncated = true
			break
		}
		out.Findings = append(out.Findings, f)
	}
	return out, rows.Err()
}
