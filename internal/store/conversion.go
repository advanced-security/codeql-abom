package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Conversion is what converting a held run's results did.
type Conversion struct {
	// Converted counts the results this conversion made findings of.
	Converted int `json:"converted"`
	// Already counts the results somebody had converted from this run before.
	Already int `json:"already"`
}

// heldRow is one held result with the member it belongs to.
type heldRow struct {
	id         int64
	repository string
	evaluation sql.NullInt64
	finding    Finding
}

// ConvertHeld makes findings of what a held run kept: every result in the run when repository is
// empty, every result in that member when ids is empty, and otherwise the results named. Each
// finding is written as adopting the query would have written it, under the held evaluation that
// reported it, and the conversion records who made it and from which run.
func (s *Store) ConvertHeld(runID int64, repository string, ids []int64, by string) (Conversion, error) {
	var out Conversion
	if err := validateConversionRequest(repository, ids, by); err != nil {
		return out, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return out, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	held, err := runIsHeld(tx, runID)
	if err != nil {
		return out, err
	}
	if !held {
		return out, fmt.Errorf("run %d stored what it found as findings, so it holds nothing to convert", runID)
	}
	rows, err := heldRows(tx, runID, repository, ids)
	if err != nil {
		return out, err
	}

	findings, err := conversionFindings(tx, runID, rows)
	if err != nil {
		return out, err
	}
	written, err := addFindings(tx, findings)
	if err != nil {
		return out, err
	}
	out, err = recordHeldConversions(tx, runID, written, by)
	if err != nil {
		return out, err
	}
	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("committing the conversion: %w", err)
	}
	// Attribution is what makes a finding visible to anything built on the graph.
	if out.Converted > 0 {
		if _, err := s.LinkFindingsToNodes(); err != nil {
			return out, fmt.Errorf("attributing what run %d converted: %w", runID, err)
		}
	}
	return out, nil
}

func validateConversionRequest(repository string, ids []int64, by string) error {
	if strings.TrimSpace(by) == "" {
		return errors.New("converting results to findings records who did it; sign in with gh")
	}
	if repository == "" && len(ids) > 0 {
		return errors.New("results are named within one repository; name it")
	}
	return nil
}

func conversionFindings(tx *transaction, runID int64, rows []heldRow) ([]Finding, error) {
	repositories := map[string]int64{}
	var findings []Finding
	for _, r := range rows {
		finding, err := heldFinding(tx, runID, repositories, r)
		if err != nil {
			return nil, err
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func heldFinding(tx *transaction, runID int64, repositories map[string]int64, r heldRow) (Finding, error) {
	if !r.evaluation.Valid {
		return Finding{}, fmt.Errorf("run %d held %s's results before it recorded the evaluation that "+
			"reported them, so they cannot be traced; run it again with findings off to convert them",
			runID, r.repository)
	}
	id, err := repositoryRowID(tx, repositories, r.repository)
	if err != nil {
		return Finding{}, err
	}
	f := r.finding
	f.RepositoryID, f.QueryRunID = id, r.evaluation.Int64
	return f, nil
}

func repositoryRowID(tx *transaction, repositories map[string]int64, repository string) (int64, error) {
	if id, ok := repositories[repository]; ok {
		return id, nil
	}
	var id int64
	if err := tx.QueryRow(`SELECT row_id FROM repository WHERE repository_full_name = ?`,
		repository).Scan(&id); err != nil {
		return 0, fmt.Errorf("%s is not a repository ABOM tracks, so it holds no findings: %w",
			repository, err)
	}
	repositories[repository] = id
	return id, nil
}

func recordHeldConversions(tx *transaction, runID int64, written []int64, by string) (Conversion, error) {
	var out Conversion
	now := time.Now().UTC().Format(time.RFC3339)
	for _, finding := range written {
		res, err := tx.Exec(`
			INSERT INTO abom_held_conversion (run_id, finding_row_id, converted_by, converted_at)
			VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`, runID, finding, by, now)
		if err != nil {
			return out, fmt.Errorf("recording the conversion of finding %d: %w", finding, err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			out.Converted++
		} else {
			out.Already++
		}
	}
	return out, nil
}

// heldRows reads the held results a conversion asked for, refusing a repository the run does not
// cover and a result it did not hold there.
func heldRows(tx *transaction, runID int64, repository string, ids []int64) ([]heldRow, error) {
	if repository != "" {
		var members int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM abom_analysis_member WHERE run_id = ? AND repository = ?`,
			runID, repository).Scan(&members); err != nil {
			return nil, fmt.Errorf("reading %s in run %d: %w", repository, runID, err)
		}
		if members == 0 {
			return nil, fmt.Errorf("run %d, %s: %w", runID, repository, ErrNoRunMember)
		}
	}
	rows, err := tx.Query(`
		SELECT h.row_id, h.repository, m.query_run_id, h.rule_id, h.file_path, h.start_line,
		       h.start_column, h.end_line, h.end_column, h.message, h.severity, h.fingerprint,
		       h.result_hash, h.provider, h.security_severity
		FROM abom_held_result h
		JOIN abom_analysis_member m
		  ON m.run_id = h.run_id AND m.repository = h.repository AND m.db_commit = h.db_commit
		 AND m.source_filter = h.source_filter AND m.analysis_mode = h.analysis_mode
		WHERE h.run_id = ? AND (? = '' OR h.repository = ?)
		ORDER BY h.row_id`, runID, repository, repository)
	if err != nil {
		return nil, fmt.Errorf("reading what run %d held: %w", runID, err)
	}
	defer rows.Close()

	wanted := map[int64]bool{}
	for _, id := range ids {
		wanted[id] = true
	}
	var out []heldRow
	for rows.Next() {
		var r heldRow
		f := &r.finding
		if err := rows.Scan(&r.id, &r.repository, &r.evaluation, &f.RuleID, &f.FilePath,
			&f.StartLine, &f.StartColumn, &f.EndLine, &f.EndColumn, &f.Message, &f.Severity,
			&f.Fingerprint, &f.ResultHash, &f.Provider, &f.SecuritySeverity); err != nil {
			return nil, fmt.Errorf("scanning what run %d held: %w", runID, err)
		}
		if len(ids) > 0 && !wanted[r.id] {
			continue
		}
		delete(wanted, r.id)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id := range wanted {
		return nil, fmt.Errorf("result %d is not one run %d held in %s", id, runID, repository)
	}
	return out, nil
}
