package store

import "fmt"

// PostureRow is one rule's finding count against the references it was measured over.
//
// The ratio is deliberately not called a score, a grade or a percentage of "correct" pinning.
// What counts as correctly pinned is the rule's judgement, so the rule id travels with every
// row and the reader can see which query produced the number.
type PostureRow struct {
	Scope string `json:"scope"`
	// ScopeKey is the repository's full name, or empty for the whole organisation.
	ScopeKey        string `json:"scopeKey"`
	RuleID          string `json:"ruleId"`
	Findings        int    `json:"findings"`
	ReferencesTotal int    `json:"referencesTotal"`
}

// PinningPosture reports finding counts per rule, for the organisation and per repository.
//
// Passing a rule id narrows to one question, such as actions/unpinned-tag for whether references
// are pinned to a commit.
func (s *Store) PinningPosture(search, ruleID string, limit int) ([]PostureRow, error) {
	where := `WHERE 1 = 1`
	args := []any{}
	if search != "" {
		where += ` AND scope_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}
	if ruleID != "" {
		where += ` AND rule_id = ?`
		args = append(args, ruleID)
	}

	query := `
		SELECT scope_kind, scope_key, rule_id, findings, references_total
		FROM abom_pinning
		` + where + `
		ORDER BY scope_kind, findings DESC, scope_key, rule_id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading pinning posture: %w", err)
	}
	defer rows.Close()

	out := []PostureRow{}
	for rows.Next() {
		var p PostureRow
		if err := rows.Scan(&p.Scope, &p.ScopeKey, &p.RuleID, &p.Findings,
			&p.ReferencesTotal); err != nil {
			return nil, fmt.Errorf("scanning posture row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PostureRules lists the rule ids posture has been measured for, so a reader can choose one.
func (s *Store) PostureRules() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT rule_id FROM abom_pinning ORDER BY rule_id`)
	if err != nil {
		return nil, fmt.Errorf("reading posture rules: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning rule id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
