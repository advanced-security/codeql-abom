package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// QueueAuthoringSession asks for a session on a request where sessions are limited in number: it
// waits, unstarted, until BeginQueuedSession, and keeps decision, the digest of the signed decision
// that asked for it.
func (s *Store) QueueAuthoringSession(request int64, startedBy, model, decision string) (int64, error) {
	if startedBy == "" || model == "" {
		return 0, errors.New("a session records who started it and the model it runs")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	var exists, under int
	if err := tx.QueryRow(`
		SELECT (SELECT COUNT(*) FROM abom_query_request WHERE row_id = ?),
		       (SELECT COUNT(*) FROM abom_authoring_session WHERE request_id = ? AND state IN (?, ?))`,
		request, request, SessionRunning, SessionQueued).Scan(&exists, &under); err != nil {
		return 0, fmt.Errorf("reading request %d: %w", request, err)
	}
	switch {
	case exists == 0:
		return 0, fmt.Errorf("asking for a session on request %d: %w", request, ErrNoQueryRequest)
	case under > 0:
		return 0, fmt.Errorf("request %d: %w", request, ErrSessionUnderWay)
	}
	res, err := tx.Exec(`
		INSERT INTO abom_authoring_session (request_id, state, started_by, model, started_at, queued_at, decision)
		VALUES (?, ?, ?, ?, '', ?, ?)`,
		request, SessionQueued, startedBy, model, time.Now().UTC().Format(time.RFC3339Nano), decision)
	if err != nil {
		return 0, fmt.Errorf("asking for a session on request %d: %w", request, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back a session: %w", err)
	}
	return id, tx.Commit()
}

// QueuedSession is a session waiting its turn: on a request, or drafting from a run about a query.
type QueuedSession struct {
	AuthoringSession
	RunID   int64
	QueryID string
}

// QueuedAuthoringSessions are the sessions waiting, the longest waiting first.
func (s *Store) QueuedAuthoringSessions() ([]QueuedSession, error) {
	rows, err := s.db.Query(`
		SELECT row_id, COALESCE(request_id, 0), COALESCE(run_id, 0), query_id, started_by, model, queued_at
		FROM abom_authoring_session WHERE state = ? ORDER BY queued_at, row_id`, SessionQueued)
	if err != nil {
		return nil, fmt.Errorf("reading the sessions waiting: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (QueuedSession, error) {
		q := QueuedSession{AuthoringSession: AuthoringSession{State: SessionQueued}}
		if err := rows.Scan(&q.RowID, &q.RequestID, &q.RunID, &q.QueryID, &q.StartedBy, &q.Model,
			&q.QueuedAt); err != nil {
			return QueuedSession{}, fmt.Errorf("scanning a session waiting: %w", err)
		}
		return q, nil
	})
}

// DraftPick is one result picked from a run for a draft, as the draft reads it: where the run found
// it and what it said there, or the repository where it found nothing it should have.
type DraftPick struct {
	Repository string `json:"repository"`
	Kind       string `json:"kind"`
	FilePath   string `json:"filePath,omitempty"`
	StartLine  int    `json:"startLine,omitempty"`
	Message    string `json:"message,omitempty"`
}

// QueueDraftSession asks for a session drafting about a query from a run where sessions are limited
// in number: it waits, unstarted, until BeginQueuedSession, with the picks it drafts from and decision,
// the digest of the signed decision that asked for it.
func (s *Store) QueueDraftSession(run int64, queryID, startedBy, model, decision string, picks []DraftPick) (int64, error) {
	if strings.TrimSpace(queryID) == "" || strings.TrimSpace(startedBy) == "" || strings.TrimSpace(model) == "" {
		return 0, errors.New("a drafting session records the query, who started it and the model it runs")
	}
	if len(picks) == 0 {
		return 0, errors.New("a draft is written from picked results; pick one")
	}
	body, err := json.Marshal(picks)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()
	var exists, under int
	if err := tx.QueryRow(`
		SELECT (SELECT COUNT(*) FROM abom_analysis_run WHERE row_id = ?),
		       (SELECT COUNT(*) FROM abom_authoring_session
		        WHERE run_id = ? AND request_id IS NULL AND state IN (?, ?))`,
		run, run, SessionRunning, SessionQueued).Scan(&exists, &under); err != nil {
		return 0, fmt.Errorf("reading run %d: %w", run, err)
	}
	switch {
	case exists == 0:
		return 0, fmt.Errorf("drafting from run %d: there is no such run", run)
	case under > 0:
		return 0, fmt.Errorf("drafting from run %d: %w", run, ErrSessionUnderWay)
	}
	res, err := tx.Exec(`
		INSERT INTO abom_authoring_session (run_id, query_id, state, started_by, model, started_at, queued_at,
		  decision, draft_picks)
		VALUES (?, ?, ?, ?, ?, '', ?, ?, ?)`,
		run, queryID, SessionQueued, startedBy, model, time.Now().UTC().Format(time.RFC3339Nano), decision,
		string(body))
	if err != nil {
		return 0, fmt.Errorf("asking for a draft from run %d: %w", run, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back a session: %w", err)
	}
	return id, tx.Commit()
}

// draftPicks are the picks a session asked for by QueueDraftSession drafts from; none for one handed
// its picks directly.
func (s *Store) draftPicks(session int64) ([]DraftPick, error) {
	var body string
	if err := s.db.QueryRow(`SELECT draft_picks FROM abom_authoring_session WHERE row_id = ?`, session).Scan(&body); err != nil {
		return nil, fmt.Errorf("reading what session %d drafts from: %w", session, err)
	}
	if body == "" {
		return nil, nil
	}
	var out []DraftPick
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("reading what session %d drafts from: %w", session, err)
	}
	return out, nil
}

// BeginQueuedSession starts a waiting session, as running from now.
func (s *Store) BeginQueuedSession(session int64) error {
	res, err := s.db.Exec(`UPDATE abom_authoring_session SET state = ?, started_at = ?
		WHERE row_id = ? AND state = ?`,
		SessionRunning, time.Now().UTC().Format(time.RFC3339), session, SessionQueued)
	if err != nil {
		return fmt.Errorf("beginning session %d: %w", session, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("beginning session %d: it is not waiting", session)
	}
	return nil
}

// SessionDecision is the digest of the signed decision that asked for a session, empty where none
// was kept.
func (s *Store) SessionDecision(session int64) (string, error) {
	var digest string
	err := s.db.QueryRow(`SELECT decision FROM abom_authoring_session WHERE row_id = ?`, session).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("session %d is not on record", session)
	}
	return digest, err
}

// SetSessionAttempt records the worker attempt running a session and the digest of its grant, as the
// attempt is launched.
func (s *Store) SetSessionAttempt(session int64, attempt, grant string) error {
	res, err := s.db.Exec(`UPDATE abom_authoring_session SET attempt = ?, grant_digest = ? WHERE row_id = ? AND state = ?`,
		attempt, grant, session, SessionRunning)
	if err != nil {
		return fmt.Errorf("recording session %d's attempt: %w", session, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return fmt.Errorf("recording session %d's attempt: %w", session, ErrSessionNotUnderWay)
	}
	return nil
}

// RemoteSession is a session a worker was running: where its reports are, and how far they were kept.
type RemoteSession struct {
	Session     int64
	Attempt     string
	Grant       string
	JournalSeq  int64
	JournalHead string
}

// RunningRemoteSessions are the sessions held as running that a worker was launched for.
func (s *Store) RunningRemoteSessions() ([]RemoteSession, error) {
	rows, err := s.db.Query(`SELECT row_id, attempt, grant_digest, journal_seq, journal_head
		FROM abom_authoring_session WHERE state = ? AND attempt <> '' ORDER BY row_id`, SessionRunning)
	if err != nil {
		return nil, fmt.Errorf("reading the sessions workers were running: %w", err)
	}
	return collectRows(rows, func(rows *sql.Rows) (RemoteSession, error) {
		var r RemoteSession
		if err := rows.Scan(&r.Session, &r.Attempt, &r.Grant, &r.JournalSeq, &r.JournalHead); err != nil {
			return RemoteSession{}, fmt.Errorf("scanning a session a worker was running: %w", err)
		}
		return r, nil
	})
}
