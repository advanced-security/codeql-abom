package store

import (
	"database/sql"
	"encoding/json"
)

type FindingAsset struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
}

type FindingDetails struct {
	Finding              FindingReview       `json:"finding"`
	Evaluations          []FindingEvaluation `json:"evaluations"`
	EvaluationsTruncated bool                `json:"evaluationsTruncated"`
	SelectedEvaluationID int64               `json:"selectedEvaluationId"`
	Documentation        json.RawMessage     `json:"documentation"`
	Result               json.RawMessage     `json:"result"`
	Assets               []FindingAsset      `json:"assets"`
	References           []string            `json:"references"`
}

func (s *Store) FindingReviewDetails(q FindingReviewQuery, evaluationID int64) (FindingDetails, error) {
	details := FindingDetails{Evaluations: []FindingEvaluation{}, Assets: []FindingAsset{}, References: []string{}}
	if q.FindingID <= 0 {
		return details, sql.ErrNoRows
	}
	q.Limit, q.Offset = 1, 0
	page, err := s.InventoryFindingsPage(q)
	if err != nil {
		return details, err
	}
	if len(page.Items) == 0 {
		return details, sql.ErrNoRows
	}
	details.Finding = page.Items[0]
	mode := q.Mode
	if mode == "" {
		mode = AnalysisModeRegular
	}
	trace, err := s.latestFindingTrace(q.FindingID, mode, 101)
	if err != nil {
		return details, err
	}
	details.Evaluations = trace.Evaluations
	if len(details.Evaluations) > 100 {
		details.EvaluationsTruncated = true
		details.Evaluations = details.Evaluations[:100]
	}
	if evaluationID == 0 && len(details.Evaluations) > 0 {
		evaluationID = details.Evaluations[0].QueryRunID
	}
	details.SelectedEvaluationID = evaluationID
	if evaluationID != 0 {
		var documentation, evidence sql.NullString
		err := s.db.QueryRow(`SELECT rule.documentation_json, sight.evidence_json
			FROM abom_finding_sighting sight
			JOIN abom_query_run evaluated ON evaluated.row_id = sight.query_run_id
			JOIN abom_finding_any_mode finding ON finding.row_id = sight.finding_row_id
			LEFT JOIN abom_query_run_rule rule ON rule.query_run_id = evaluated.row_id AND rule.rule_id = finding.rule_id
			WHERE sight.finding_row_id = ? AND sight.query_run_id = ? AND evaluated.analysis_mode = ?`,
			q.FindingID, evaluationID, mode).Scan(&documentation, &evidence)
		if err != nil {
			return details, err
		}
		if documentation.Valid {
			details.Documentation = json.RawMessage(documentation.String)
		}
		if evidence.Valid {
			details.Result = json.RawMessage(evidence.String)
		}
		details.References, err = s.QueryRunRefs(evaluationID)
		if err != nil {
			return details, err
		}
	}
	details.Assets, err = queryAll(s, "reading finding attribution", func(rows *sql.Rows) (FindingAsset, error) {
		var asset FindingAsset
		return asset, rows.Scan(&asset.Key, &asset.Kind)
	}, `SELECT node.node_key, node.node_kind FROM abom_finding_node link
		JOIN abom_node_live node ON node.row_id = link.node_row_id
		WHERE link.finding_row_id = ? ORDER BY node.node_key`, q.FindingID)
	return details, err
}
