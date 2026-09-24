package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// authoringSessionDDL keeps every agent session on a request, and every hook invocation in one.
const authoringSessionDDL = `
CREATE TABLE IF NOT EXISTS abom_authoring_session (
  row_id     INTEGER PRIMARY KEY,
  request_id INTEGER NOT NULL REFERENCES abom_query_request(row_id),
  state      TEXT NOT NULL,
  started_by TEXT NOT NULL,
  model      TEXT NOT NULL,
  started_at TEXT NOT NULL,
  ended_at   TEXT,
  detail     TEXT NOT NULL DEFAULT ''
);

-- Two agents writing one query's files would each read the other's work as its own.
CREATE UNIQUE INDEX IF NOT EXISTS idx_authoring_session_running
  ON abom_authoring_session(request_id) WHERE state = 'running';

-- Append-only: what an agent tried, as it was decided, including everything refused.
CREATE TABLE IF NOT EXISTS abom_authoring_event (
  row_id     INTEGER PRIMARY KEY,
  session_id INTEGER NOT NULL REFERENCES abom_authoring_session(row_id),
  at         TEXT NOT NULL,
  hook       TEXT NOT NULL,
  tool       TEXT NOT NULL,
  subject    TEXT NOT NULL DEFAULT '',
  decision   TEXT NOT NULL,
  step       TEXT NOT NULL DEFAULT '',
  reason     TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '',
  took_ms    INTEGER
);

CREATE INDEX IF NOT EXISTS idx_authoring_event_session ON abom_authoring_event(session_id, row_id);`

// Where a session stands.
const (
	SessionRunning = "running"
	// SessionFinished is an agent that stopped on its own.
	SessionFinished = "finished"
	// SessionStopped is a session a person or a limit stopped.
	SessionStopped = "stopped"
	// SessionFailed is a session that could not go on: the CLI, the model or ABOM failed under it.
	SessionFailed = "failed"
	// SessionInterrupted is a session the server went away under, which nothing ended.
	SessionInterrupted = "interrupted"
)

var sessionEnds = []string{SessionFinished, SessionStopped, SessionFailed}

var (
	// ErrSessionUnderWay is a second session on a request one is already working.
	ErrSessionUnderWay = errors.New("a session is already working this request")
	// ErrSessionNotUnderWay is ending a session that has already ended, or never began.
	ErrSessionNotUnderWay = errors.New("this session is not under way")
)

// AuthoringSession is one agent session on a request.
type AuthoringSession struct {
	RowID     int64  `json:"rowId"`
	RequestID int64  `json:"requestId"`
	State     string `json:"state"`
	StartedBy string `json:"startedBy"`
	Model     string `json:"model"`
	StartedAt string `json:"startedAt"`
	EndedAt   string `json:"endedAt"`
	Detail    string `json:"detail"`
	Events    int    `json:"events"`
	Refused   int    `json:"refused"`
}

// AuthoringEvent is one hook invocation in a session.
type AuthoringEvent struct {
	RowID     int64  `json:"rowId"`
	SessionID int64  `json:"sessionId"`
	At        string `json:"at"`
	Hook      string `json:"hook"`
	Tool      string `json:"tool"`
	Subject   string `json:"subject"`
	Decision  string `json:"decision"`
	Step      string `json:"step"`
	Reason    string `json:"reason"`
	Detail    string `json:"detail"`
	// TookMs is how long the call ran, nil where nothing measured it, which is not zero.
	TookMs *int64 `json:"tookMs"`
}

// StartAuthoringSession records a session beginning on a request.
func (s *Store) StartAuthoringSession(request int64, startedBy, model string) (int64, error) {
	if strings.TrimSpace(startedBy) == "" || strings.TrimSpace(model) == "" {
		return 0, errors.New("a session records who started it and the model it runs")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var running, exists int
	if err := tx.QueryRow(`
		SELECT (SELECT COUNT(*) FROM abom_query_request WHERE row_id = ?),
		       (SELECT COUNT(*) FROM abom_authoring_session WHERE request_id = ? AND state = ?)`,
		request, request, SessionRunning).Scan(&exists, &running); err != nil {
		return 0, fmt.Errorf("reading request %d: %w", request, err)
	}
	switch {
	case exists == 0:
		return 0, fmt.Errorf("starting a session on request %d: %w", request, ErrNoQueryRequest)
	case running > 0:
		return 0, fmt.Errorf("request %d: %w", request, ErrSessionUnderWay)
	}
	res, err := tx.Exec(`
		INSERT INTO abom_authoring_session (request_id, state, started_by, model, started_at)
		VALUES (?, ?, ?, ?, ?)`,
		request, SessionRunning, startedBy, model, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("starting a session on request %d: %w", request, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back a session: %w", err)
	}
	return id, tx.Commit()
}

// EndAuthoringSession records how a session under way ended.
func (s *Store) EndAuthoringSession(session int64, state, detail string) error {
	if !slices.Contains(sessionEnds, state) {
		return fmt.Errorf("a session ends %s, not %q", strings.Join(sessionEnds, ", "), state)
	}
	res, err := s.db.Exec(`
		UPDATE abom_authoring_session SET state = ?, detail = ?, ended_at = ?
		WHERE row_id = ? AND state = ?`,
		state, detail, time.Now().UTC().Format(time.RFC3339), session, SessionRunning)
	if err != nil {
		return fmt.Errorf("ending session %d: %w", session, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("ending session %d: %w", session, ErrSessionNotUnderWay)
	}
	return nil
}

// InterruptAuthoringSessions ends sessions still marked running; valid only before serving starts.
func (s *Store) InterruptAuthoringSessions() (int, error) {
	res, err := s.db.Exec(`
		UPDATE abom_authoring_session SET state = ?, ended_at = ?,
		       detail = 'the server running this session stopped before the session ended'
		WHERE state = ?`,
		SessionInterrupted, time.Now().UTC().Format(time.RFC3339), SessionRunning)
	if err != nil {
		return 0, fmt.Errorf("interrupting abandoned sessions: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AuthoringSessions lists a request's sessions, newest first, with how much each did and how often
// it was refused.
func (s *Store) AuthoringSessions(request int64) ([]AuthoringSession, error) {
	rows, err := s.db.Query(`
		SELECT s.row_id, s.request_id, s.state, s.started_by, s.model, s.started_at,
		       COALESCE(s.ended_at, ''), s.detail, COUNT(e.row_id),
		       COALESCE(SUM(e.decision = 'refused'), 0)
		FROM abom_authoring_session s
		LEFT JOIN abom_authoring_event e ON e.session_id = s.row_id
		WHERE s.request_id = ?
		GROUP BY s.row_id
		ORDER BY s.row_id DESC`, request)
	if err != nil {
		return nil, fmt.Errorf("reading the sessions on request %d: %w", request, err)
	}
	defer rows.Close()

	out := []AuthoringSession{}
	for rows.Next() {
		var a AuthoringSession
		if err := rows.Scan(&a.RowID, &a.RequestID, &a.State, &a.StartedBy, &a.Model, &a.StartedAt,
			&a.EndedAt, &a.Detail, &a.Events, &a.Refused); err != nil {
			return nil, fmt.Errorf("scanning a session: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RecordAuthoringEvent appends one hook invocation to its session's timeline.
func (s *Store) RecordAuthoringEvent(e AuthoringEvent) (int64, error) {
	if e.SessionID == 0 || e.At == "" || e.Hook == "" || e.Tool == "" || e.Decision == "" {
		return 0, errors.New("an event names its session, when, the hook, the tool and the decision")
	}
	var took any
	if e.TookMs != nil {
		took = *e.TookMs
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_authoring_event
		  (session_id, at, hook, tool, subject, decision, step, reason, detail, took_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.SessionID, e.At, e.Hook, e.Tool, e.Subject, e.Decision, e.Step, e.Reason, e.Detail, took)
	if err != nil {
		return 0, fmt.Errorf("recording an event in session %d: %w", e.SessionID, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	return res.LastInsertId()
}

// AuthoringEvents reads a session's timeline in order, from after the event a reader already has.
func (s *Store) AuthoringEvents(session, after int64, limit int) ([]AuthoringEvent, error) {
	if limit <= 0 {
		limit = authoringEventsLimit
	}
	rows, err := s.db.Query(`
		SELECT row_id, session_id, at, hook, tool, subject, decision, step, reason, detail, took_ms
		FROM abom_authoring_event
		WHERE session_id = ? AND row_id > ?
		ORDER BY row_id LIMIT ?`, session, after, limit)
	if err != nil {
		return nil, fmt.Errorf("reading session %d: %w", session, err)
	}
	defer rows.Close()

	out := []AuthoringEvent{}
	for rows.Next() {
		var e AuthoringEvent
		var took sql.NullInt64
		if err := rows.Scan(&e.RowID, &e.SessionID, &e.At, &e.Hook, &e.Tool, &e.Subject,
			&e.Decision, &e.Step, &e.Reason, &e.Detail, &took); err != nil {
			return nil, fmt.Errorf("scanning an event: %w", err)
		}
		if took.Valid {
			e.TookMs = &took.Int64
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// authoringEventsLimit bounds one read of a timeline; a reader pages on from the last row it got.
const authoringEventsLimit = 500

// DecideTarget records the agent's choice of the query a request is about, with why. Only where
// nobody chose one: the operator's choice is theirs, and a decision is made once.
func (s *Store) DecideTarget(request int64, queryID, rationale string) error {
	if strings.TrimSpace(queryID) == "" {
		return errors.New("a decided target names a query")
	}
	if strings.TrimSpace(rationale) == "" {
		return errors.New("a decided target says why it was chosen")
	}
	res, err := s.db.Exec(`
		UPDATE abom_query_request SET query_id = ?, target_source = ?, target_detail = ?, updated_at = ?
		WHERE row_id = ? AND query_id = ''`,
		queryID, TargetAgent, rationale, time.Now().UTC().Format(time.RFC3339), request)
	if err != nil {
		return fmt.Errorf("deciding the target of request %d: %w", request, err)
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		return err
	}
	var named string
	switch err := s.db.QueryRow(`SELECT query_id FROM abom_query_request WHERE row_id = ?`,
		request).Scan(&named); {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("deciding the target of request %d: %w", request, ErrNoQueryRequest)
	case err != nil:
		return fmt.Errorf("reading request %d: %w", request, err)
	}
	return fmt.Errorf("request %d is already about %s", request, named)
}
