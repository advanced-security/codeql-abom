package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// queryRequestDDL keeps asks apart from query versions, so an ask nobody answered stays visible.
const queryRequestDDL = `
CREATE TABLE IF NOT EXISTS abom_query_request (
  row_id        INTEGER PRIMARY KEY,
  description   TEXT NOT NULL,
  compliant     TEXT NOT NULL,
  non_compliant TEXT NOT NULL,
  query_id      TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  requested_by  TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  target_source TEXT,
  source_run_id INTEGER REFERENCES abom_analysis_run(row_id),
  target_detail TEXT
);

-- What was picked from the run a request was asked from.
CREATE TABLE IF NOT EXISTS abom_query_request_evidence ` + evidenceTable

// evidenceTable is the evidence table's body, shared with the rebuild that gave it held results.
// A false alarm names a finding a run stored or a result a held run kept; zero stands for neither,
// since NULLs are distinct in a key and two held picks in one repository must both be kept.
const evidenceTable = `(
  request_id     INTEGER NOT NULL REFERENCES abom_query_request(row_id),
  run_id         INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  repository     TEXT NOT NULL,
  finding_row_id INTEGER NOT NULL DEFAULT 0,
  kind           TEXT NOT NULL,
  held_result_id INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (request_id, run_id, repository, finding_row_id, held_result_id)
);`

// What a request asks for. Empty is a request that has not said.
const (
	RequestMissed     = "missed"
	RequestFalseAlarm = "false-alarm"
	RequestBoth       = "both"
)

// Where a request stands. Declined rather than deleted, so an ask nobody took up stays listable.
const (
	RequestOpen       = "open"
	RequestInProgress = "in-progress"
	RequestDone       = "done"
	RequestDeclined   = "declined"
)

// Who chose a request's target.
const (
	TargetOperator = "operator"
	TargetAgent    = "agent"
)

var (
	requestKinds    = []string{"", RequestMissed, RequestFalseAlarm, RequestBoth}
	requestStatuses = []string{RequestOpen, RequestInProgress, RequestDone, RequestDeclined}
	targetSources   = []string{TargetOperator, TargetAgent}
)

// ErrNoQueryRequest is an edit of a request nobody made.
var ErrNoQueryRequest = errors.New("there is no such query request")

// QueryRequest is one ask for a query customization.
type QueryRequest struct {
	RowID        int64  `json:"rowId"`
	Description  string `json:"description"`
	Compliant    string `json:"compliant"`
	NonCompliant string `json:"nonCompliant"`
	// QueryID names the query the request is about, and is empty for one that does not exist yet.
	QueryID string `json:"queryId"`
	// TargetSource says who chose QueryID. Empty with a target is a request kept before that was
	// recorded, which is not the same as the operator having chosen it.
	TargetSource string `json:"targetSource"`
	// TargetDetail is why the agent chose QueryID; empty for any target a person chose.
	TargetDetail string `json:"targetDetail"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
	RequestedBy  string `json:"requestedBy"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	// SourceRunID is the run the request was asked from, zero when it was asked from nothing.
	SourceRunID int64 `json:"sourceRunId"`
	// Evidence is what was picked from that run. Kept as filed: an edit changes what is asked
	// for, never what it was asked from.
	Evidence []RequestEvidence `json:"evidence"`
}

// RequestEvidence is one result picked from a run: a finding that should not have fired, a result a
// held run kept that should not have, or a repository where the query ran and something should
// have.
type RequestEvidence struct {
	RunID        int64  `json:"runId"`
	Repository   string `json:"repository"`
	FindingRowID int64  `json:"findingRowId"`
	HeldResultID int64  `json:"heldResultId"`
	Kind         string `json:"kind"`
}

// QueryRequestKinds and QueryRequestStatuses are the vocabularies, for a control to offer.
func QueryRequestKinds() []string { return slices.Clone(requestKinds) }

func QueryRequestStatuses() []string { return slices.Clone(requestStatuses) }

// fileQueryRequest keeps a new request and what was picked for it, in one transaction.
func (s *Store) fileQueryRequest(r QueryRequest, now string) (int64, error) {
	if err := r.checkEvidence(); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if err := evidenceShown(tx, r); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`
INSERT INTO abom_query_request
  (description, compliant, non_compliant, query_id, kind, status, requested_by, created_at, updated_at,
   target_source, source_run_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Description, r.Compliant, r.NonCompliant, r.QueryID, r.Kind, r.Status,
		r.RequestedBy, now, now, nullable(r.TargetSource), nullableID(r.SourceRunID))
	if err != nil {
		return 0, fmt.Errorf("keeping a query request: %w", err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back a query request: %w", err)
	}
	for _, e := range r.Evidence {
		if _, err := tx.Exec(`
INSERT OR IGNORE INTO abom_query_request_evidence
  (request_id, run_id, repository, finding_row_id, held_result_id, kind)
VALUES (?, ?, ?, ?, ?, ?)`, id, e.RunID, e.Repository, e.FindingRowID, e.HeldResultID, e.Kind); err != nil {
			return 0, fmt.Errorf("keeping what request %d was asked from: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing a query request: %w", err)
	}
	return id, nil
}

// checkEvidence refuses picked results whose shape cannot be what a run showed.
func (r QueryRequest) checkEvidence() error {
	switch {
	case len(r.Evidence) > 0 && r.SourceRunID == 0:
		return errors.New("evidence is picked from a run; name the run it came from")
	case r.SourceRunID != 0 && r.QueryID == "":
		return errors.New("a request asked from a run is about a query that ran in it")
	}
	for _, e := range r.Evidence {
		switch {
		case e.RunID != r.SourceRunID:
			return fmt.Errorf("evidence from run %d on a request asked from run %d",
				e.RunID, r.SourceRunID)
		case e.Kind != RequestFalseAlarm && e.Kind != RequestMissed:
			return fmt.Errorf("evidence is a %s or a %s, not %q", RequestFalseAlarm, RequestMissed,
				e.Kind)
		case e.Kind == RequestFalseAlarm && (e.FindingRowID == 0) == (e.HeldResultID == 0):
			return errors.New("a false alarm names the one result that should not have fired")
		case e.Kind == RequestMissed && (e.FindingRowID != 0 || e.HeldResultID != 0):
			return errors.New("a missed detection names the repository, not a result")
		case r.Kind != RequestBoth && r.Kind != e.Kind:
			return fmt.Errorf("a request for %q cannot rest on a %s", r.Kind, e.Kind)
		}
	}
	return nil
}

// evidenceShown refuses picked results the run did not show: a run that never ran the query, a
// repository it never evaluated, or a finding that member's evaluation did not report.
func evidenceShown(tx *sql.Tx, r QueryRequest) error {
	if r.SourceRunID == 0 {
		return nil
	}
	var ran int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM abom_analysis_query WHERE run_id = ? AND query_id = ?`,
		r.SourceRunID, r.QueryID).Scan(&ran); err != nil {
		return fmt.Errorf("reading run %d: %w", r.SourceRunID, err)
	}
	if ran == 0 {
		return fmt.Errorf("run %d did not run %s, so a request about it was not asked from there",
			r.SourceRunID, r.QueryID)
	}
	held, err := runIsHeld(tx, r.SourceRunID)
	if err != nil {
		return err
	}
	for _, e := range r.Evidence {
		if err := pickShown(tx, r.QueryID, held, e); err != nil {
			return err
		}
	}
	return nil
}

// pickShown refuses one pick the run did not show. A held run shows what it kept, and a member it
// counted is one it evaluated even when a store from before held evaluations links none.
func pickShown(tx *sql.Tx, queryID string, held bool, e RequestEvidence) error {
	switch {
	case held && e.FindingRowID != 0:
		return fmt.Errorf("run %d held what it found, so it stored no finding %d to cite",
			e.RunID, e.FindingRowID)
	case !held && e.HeldResultID != 0:
		return fmt.Errorf("run %d stored what it found as findings, so it held no result %d to cite",
			e.RunID, e.HeldResultID)
	}
	var evaluated int
	if err := tx.QueryRow(`
		SELECT CASE WHEN ? THEN COUNT(result_count) ELSE COUNT(query_run_id) END
		FROM abom_analysis_member WHERE run_id = ? AND repository = ?`,
		held, e.RunID, e.Repository).Scan(&evaluated); err != nil {
		return fmt.Errorf("reading %s in run %d: %w", e.Repository, e.RunID, err)
	}
	if evaluated == 0 {
		return fmt.Errorf("run %d evaluated nothing in %s, so it shows nothing there",
			e.RunID, e.Repository)
	}
	if e.Kind != RequestFalseAlarm {
		return nil
	}
	if held {
		var kept int
		if err := tx.QueryRow(`
			SELECT COUNT(*) FROM abom_held_result
			WHERE row_id = ? AND run_id = ? AND repository = ? AND rule_id = ?`,
			e.HeldResultID, e.RunID, e.Repository, queryID).Scan(&kept); err != nil {
			return fmt.Errorf("reading held result %d: %w", e.HeldResultID, err)
		}
		if kept == 0 {
			return fmt.Errorf("result %d is not one %s reported in %s in run %d",
				e.HeldResultID, queryID, e.Repository, e.RunID)
		}
		return nil
	}
	var reported int
	if err := tx.QueryRow(`
		SELECT COUNT(*) FROM abom_analysis_member m
		JOIN abom_finding_sighting sg ON sg.query_run_id = m.query_run_id
		JOIN abom_finding_any_mode f ON f.row_id = sg.finding_row_id
		WHERE m.run_id = ? AND m.repository = ? AND f.row_id = ? AND f.rule_id = ?`,
		e.RunID, e.Repository, e.FindingRowID, queryID).Scan(&reported); err != nil {
		return fmt.Errorf("reading finding %d: %w", e.FindingRowID, err)
	}
	if reported == 0 {
		return fmt.Errorf("finding %d is not one %s reported in %s in run %d",
			e.FindingRowID, queryID, e.Repository, e.RunID)
	}
	return nil
}

// SaveQueryRequest inserts or edits by RowID, keeping who asked and when, and returns the id.
func (s *Store) SaveQueryRequest(r QueryRequest) (int64, error) {
	if r.Status == "" {
		r.Status = RequestOpen
	}
	if err := r.check(); err != nil {
		return 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)

	if r.RowID == 0 {
		if strings.TrimSpace(r.RequestedBy) == "" {
			return 0, errors.New("a request records who asked for it")
		}
		return s.fileQueryRequest(r, now)
	}

	// An edit is the operator's act, so reasoning the agent gave no longer describes the target.
	res, err := s.db.Exec(`
UPDATE abom_query_request SET description = ?, compliant = ?, non_compliant = ?, query_id = ?,
  kind = ?, status = ?, updated_at = ?, target_source = ?, target_detail = NULL
WHERE row_id = ?`,
		r.Description, r.Compliant, r.NonCompliant, r.QueryID, r.Kind, r.Status, now,
		nullable(r.TargetSource), r.RowID)
	if err != nil {
		return 0, fmt.Errorf("editing query request %d: %w", r.RowID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return 0, fmt.Errorf("editing query request %d: %w", r.RowID, ErrNoQueryRequest)
	}
	return r.RowID, nil
}

func (r QueryRequest) check() error {
	for name, value := range map[string]string{
		"a description of what to detect": r.Description,
		"a compliant example":             r.Compliant,
		"a non-compliant example":         r.NonCompliant,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("a request needs %s", name)
		}
	}
	if !slices.Contains(requestKinds, r.Kind) {
		return fmt.Errorf("a request's kind is %s, not %q", strings.Join(requestKinds[1:], ", "), r.Kind)
	}
	if !slices.Contains(requestStatuses, r.Status) {
		return fmt.Errorf("a request's status is %s, not %q", strings.Join(requestStatuses, ", "), r.Status)
	}
	switch {
	case r.QueryID != "" && r.TargetSource == "":
		return fmt.Errorf("a request about %s records who chose it", r.QueryID)
	case r.QueryID == "" && r.TargetSource != "":
		return errors.New("a request with no target has nobody who chose one")
	case r.TargetSource != "" && !slices.Contains(targetSources, r.TargetSource):
		return fmt.Errorf("a target is chosen by %s, not %q", strings.Join(targetSources, " or "),
			r.TargetSource)
	}
	return nil
}

// QueryRequests lists every request, newest first, with what each was asked from.
func (s *Store) QueryRequests() ([]QueryRequest, error) {
	evidence, err := s.requestEvidence()
	if err != nil {
		return nil, err
	}
	out := []QueryRequest{}
	rows, err := s.db.Query(`
		SELECT row_id, description, compliant, non_compliant, query_id, kind, status,
		       requested_by, created_at, updated_at, COALESCE(target_source, ''),
		       COALESCE(source_run_id, 0), COALESCE(target_detail, '')
		FROM abom_query_request ORDER BY row_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("reading query requests: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r QueryRequest
		if err := rows.Scan(&r.RowID, &r.Description, &r.Compliant, &r.NonCompliant, &r.QueryID,
			&r.Kind, &r.Status, &r.RequestedBy, &r.CreatedAt, &r.UpdatedAt,
			&r.TargetSource, &r.SourceRunID, &r.TargetDetail); err != nil {
			return nil, fmt.Errorf("scanning a query request: %w", err)
		}
		r.Evidence = append([]RequestEvidence{}, evidence[r.RowID]...)
		out = append(out, r)
	}
	return out, rows.Err()
}

// requestEvidence reads every request's picked results, keyed by the request.
func (s *Store) requestEvidence() (map[int64][]RequestEvidence, error) {
	out := map[int64][]RequestEvidence{}
	rows, err := s.db.Query(`
		SELECT request_id, run_id, repository, finding_row_id, held_result_id, kind
		FROM abom_query_request_evidence
		ORDER BY request_id, repository, finding_row_id, held_result_id`)
	if err != nil {
		return nil, fmt.Errorf("reading what requests were asked from: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var e RequestEvidence
		if err := rows.Scan(&id, &e.RunID, &e.Repository, &e.FindingRowID, &e.HeldResultID,
			&e.Kind); err != nil {
			return nil, fmt.Errorf("scanning a request's evidence: %w", err)
		}
		out[id] = append(out[id], e)
	}
	return out, rows.Err()
}
