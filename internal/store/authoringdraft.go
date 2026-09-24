package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// requestDraftDDL keeps what a drafting session proposed.
const requestDraftDDL = `
-- Append-only: every draft a session proposed; the newest is the one a person reads.
CREATE TABLE IF NOT EXISTS abom_request_draft (
  row_id        INTEGER PRIMARY KEY,
  session_id    INTEGER NOT NULL REFERENCES abom_authoring_session(row_id),
  drafted_at    TEXT NOT NULL,
  description   TEXT NOT NULL,
  compliant     TEXT NOT NULL,
  non_compliant TEXT NOT NULL,
  rationale     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_request_draft_session ON abom_request_draft(session_id, row_id);`

// draftSessionIndexDDL keeps one draft at a time from a run, so a reader is never shown two answers
// to one set of picks.
const draftSessionIndexDDL = `
CREATE UNIQUE INDEX IF NOT EXISTS idx_authoring_session_drafting
  ON abom_authoring_session(run_id) WHERE state = 'running' AND request_id IS NULL;`

// RequestDraft is a request an agent drafted from a run's results, which nobody has filed.
type RequestDraft struct {
	RowID        int64  `json:"rowId"`
	SessionID    int64  `json:"sessionId"`
	DraftedAt    string `json:"draftedAt"`
	Description  string `json:"description"`
	Compliant    string `json:"compliant"`
	NonCompliant string `json:"nonCompliant"`
	Rationale    string `json:"rationale"`
}

// DraftSession is a session drafting a request from a run's results, and the newest draft it
// proposed, nil until it proposes one.
type DraftSession struct {
	AuthoringSession
	RunID   int64         `json:"runId"`
	QueryID string        `json:"queryId"`
	Draft   *RequestDraft `json:"draft"`
}

// StartDraftSession records a session beginning to draft a request about a query from a run.
func (s *Store) StartDraftSession(run int64, queryID, startedBy, model string) (int64, error) {
	if strings.TrimSpace(queryID) == "" || strings.TrimSpace(startedBy) == "" || strings.TrimSpace(model) == "" {
		return 0, errors.New("a drafting session records the query, who started it and the model it runs")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var exists, running int
	if err := tx.QueryRow(`
		SELECT (SELECT COUNT(*) FROM abom_analysis_run WHERE row_id = ?),
		       (SELECT COUNT(*) FROM abom_authoring_session
		        WHERE run_id = ? AND request_id IS NULL AND state = ?)`,
		run, run, SessionRunning).Scan(&exists, &running); err != nil {
		return 0, fmt.Errorf("reading run %d: %w", run, err)
	}
	switch {
	case exists == 0:
		return 0, fmt.Errorf("drafting from run %d: there is no such run", run)
	case running > 0:
		return 0, fmt.Errorf("drafting from run %d: %w", run, ErrSessionUnderWay)
	}
	res, err := tx.Exec(`
		INSERT INTO abom_authoring_session (run_id, query_id, state, started_by, model, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		run, queryID, SessionRunning, startedBy, model, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("starting a draft from run %d: %w", run, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back a session: %w", err)
	}
	return id, tx.Commit()
}

// RecordRequestDraft keeps a draft a session proposed.
func (s *Store) RecordRequestDraft(d RequestDraft) (int64, error) {
	if strings.TrimSpace(d.Description) == "" || strings.TrimSpace(d.Compliant) == "" ||
		strings.TrimSpace(d.NonCompliant) == "" {
		return 0, errors.New("a draft says what to detect, with a COMPLIANT and a NON_COMPLIANT example, " +
			"as a request must")
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_request_draft (session_id, drafted_at, description, compliant, non_compliant, rationale)
		SELECT row_id, ?, ?, ?, ?, ? FROM abom_authoring_session WHERE row_id = ? AND request_id IS NULL`,
		time.Now().UTC().Format(time.RFC3339), d.Description, d.Compliant, d.NonCompliant, d.Rationale,
		d.SessionID)
	if err != nil {
		return 0, fmt.Errorf("keeping a draft from session %d: %w", d.SessionID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return 0, fmt.Errorf("keeping a draft: session %d is not drafting from a run", d.SessionID)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	return res.LastInsertId()
}

// CheckPicked refuses picks a run did not show, by the rules a request filed from them is held to.
func (s *Store) CheckPicked(run int64, queryID string, evidence []RequestEvidence) error {
	r := QueryRequest{SourceRunID: run, QueryID: queryID, Kind: RequestBoth, Evidence: evidence}
	if err := r.checkEvidence(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	return evidenceShown(tx, r)
}

// DraftSessions lists the sessions drafting from a run, newest first, each with its newest draft.
func (s *Store) DraftSessions(run int64) ([]DraftSession, error) {
	rows, err := s.db.Query(`
		SELECT s.row_id, s.run_id, s.query_id, s.state, s.started_by, s.model, s.started_at,
		       COALESCE(s.ended_at, ''), s.detail,
		       (SELECT COUNT(*) FROM abom_authoring_event e WHERE e.session_id = s.row_id),
		       (SELECT COUNT(*) FROM abom_authoring_event e WHERE e.session_id = s.row_id AND e.decision = 'refused'),
		       s.model_calls, s.input_tokens, s.output_tokens, s.nano_aiu,
		       d.row_id, d.drafted_at, d.description, d.compliant, d.non_compliant, d.rationale
		FROM abom_authoring_session s
		LEFT JOIN abom_request_draft d ON d.row_id =
		  (SELECT MAX(row_id) FROM abom_request_draft WHERE session_id = s.row_id)
		WHERE s.run_id = ? AND s.request_id IS NULL
		ORDER BY s.row_id DESC`, run)
	if err != nil {
		return nil, fmt.Errorf("reading the drafts from run %d: %w", run, err)
	}
	defer rows.Close()

	out := []DraftSession{}
	for rows.Next() {
		var d DraftSession
		var calls, input, output, nano, draftID sql.NullInt64
		var at, description, compliant, nonCompliant, rationale sql.NullString
		if err := rows.Scan(&d.RowID, &d.RunID, &d.QueryID, &d.State, &d.StartedBy, &d.Model, &d.StartedAt,
			&d.EndedAt, &d.Detail, &d.Events, &d.Refused, &calls, &input, &output, &nano,
			&draftID, &at, &description, &compliant, &nonCompliant, &rationale); err != nil {
			return nil, fmt.Errorf("scanning a drafting session: %w", err)
		}
		d.ModelCalls, d.InputTokens = reported(calls), reported(input)
		d.OutputTokens, d.NanoAIU = reported(output), reported(nano)
		if draftID.Valid {
			d.Draft = &RequestDraft{RowID: draftID.Int64, SessionID: d.RowID, DraftedAt: at.String,
				Description: description.String, Compliant: compliant.String,
				NonCompliant: nonCompliant.String, Rationale: rationale.String}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
