package store

import (
	"errors"
	"fmt"
)

// ErrNoRunMember is a repository the run does not cover.
var ErrNoRunMember = errors.New("the run covers no such repository")

// RunFinding is one result a member of a run reported.
type RunFinding struct {
	FindingRowID int64  `json:"findingRowId"`
	RuleID       string `json:"ruleId"`
	FilePath     string `json:"filePath"`
	StartLine    int    `json:"startLine"`
	StartColumn  int    `json:"startColumn"`
	Message      string `json:"message"`
	Severity     string `json:"severity"`
}

// MemberFindings is what one member of a run reported.
//
// Evaluated keeps a member nothing ran against apart from one that ran and found nothing: both
// list no findings, and only the second is a zero.
type MemberFindings struct {
	Repository string `json:"repository"`
	Evaluated  bool   `json:"evaluated"`
	// Unlinked is a member counted before members kept their evaluation: it has results and
	// nothing to list them from until the run goes again.
	Unlinked  bool         `json:"unlinked"`
	Findings  []RunFinding `json:"findings"`
	Truncated bool         `json:"truncated"`
}

// RunFindings lists what one member's evaluation reported, narrowed to one query when one is
// named, at most limit of them.
//
// Read through the sightings of the member's own ledger row, because a finding row spans every
// run that ever reported it and its own query_run_id names only the first.
func (s *Store) RunFindings(runID int64, repository, queryID string, limit int) (MemberFindings, error) {
	out := MemberFindings{Repository: repository, Findings: []RunFinding{}}
	var members, evaluated, unlinked int
	if err := s.db.QueryRow(`
		SELECT COUNT(*), COUNT(query_run_id),
		       COUNT(CASE WHEN query_run_id IS NULL AND result_count IS NOT NULL THEN 1 END)
		FROM abom_analysis_member
		WHERE run_id = ? AND repository = ?`, runID, repository).Scan(&members, &evaluated, &unlinked); err != nil {
		return out, fmt.Errorf("reading %s in run %d: %w", repository, runID, err)
	}
	if members == 0 {
		return out, fmt.Errorf("run %d, %s: %w", runID, repository, ErrNoRunMember)
	}
	out.Evaluated, out.Unlinked = evaluated > 0, evaluated == 0 && unlinked > 0

	// Any mode, because the member's own ledger row already fixes which shape reported these.
	rows, err := s.db.Query(`
		SELECT f.row_id, f.rule_id, COALESCE(f.file_path, ''), COALESCE(f.start_line, 0),
		       COALESCE(f.start_column, 0), COALESCE(f.message, ''), COALESCE(f.severity, '')
		FROM abom_analysis_member m
		JOIN abom_finding_sighting sg ON sg.query_run_id = m.query_run_id
		JOIN abom_finding_any_mode f ON f.row_id = sg.finding_row_id
		WHERE m.run_id = ? AND m.repository = ? AND (? = '' OR f.rule_id = ?)
		ORDER BY f.file_path, f.start_line, f.start_column, f.rule_id, f.row_id
		LIMIT ?`, runID, repository, queryID, queryID, limit+1)
	if err != nil {
		return out, fmt.Errorf("listing what %s reported in run %d: %w", repository, runID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var f RunFinding
		if err := rows.Scan(&f.FindingRowID, &f.RuleID, &f.FilePath, &f.StartLine, &f.StartColumn,
			&f.Message, &f.Severity); err != nil {
			return out, fmt.Errorf("scanning a finding of %s: %w", repository, err)
		}
		if len(out.Findings) == limit {
			out.Truncated = true
			break
		}
		out.Findings = append(out.Findings, f)
	}
	return out, rows.Err()
}
