package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// authoringWriter is what an authoring write runs on: the database, or the transaction an ingest
// holds, so what a worker's session kept is replayed through the statements a local one writes with.
type authoringWriter interface {
	Exec(q string, a ...any) (sql.Result, error)
	QueryRow(q string, a ...any) *row
}

// What a worker's session keeps, by kind, in the order it kept them.
const (
	EntryEvent  = "event"
	EntryTree   = "tree"
	EntrySpend  = "spend"
	EntryModel  = "model"
	EntryTarget = "target"
	EntryTest   = "test"
	EntryDraft  = "draft"
	// EntryEnd is how the session ended as the worker saw it, which is handed back rather than kept.
	EntryEnd = "end"
)

// TargetDecision is the agent's choice of the query its request is about.
type TargetDecision struct {
	QueryID   string `json:"queryId"`
	Rationale string `json:"rationale"`
}

// SessionEnd is how a session ended, as the worker that ran it saw it.
type SessionEnd struct {
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// AuthoringEntry is one thing a session kept where the store was not, with the field its kind names.
type AuthoringEntry struct {
	Kind   string          `json:"kind"`
	Event  *AuthoringEvent `json:"event,omitempty"`
	Tree   *AuthoringAST   `json:"tree,omitempty"`
	Spend  *AuthoringSpend `json:"spend,omitempty"`
	Model  string          `json:"model,omitempty"`
	Target *TargetDecision `json:"target,omitempty"`
	Test   *QueryTest      `json:"test,omitempty"`
	Draft  *RequestDraft   `json:"draft,omitempty"`
	End    *SessionEnd     `json:"end,omitempty"`
}

// AuthoringReport is a run of a journal's entries: its place in the journal, the digest it is known
// by, and the digest of the report before it, which it follows.
type AuthoringReport struct {
	Seq     int64            `json:"seq"`
	Digest  string           `json:"digest"`
	Prev    string           `json:"prev"`
	Entries []AuthoringEntry `json:"entries"`
}

// AuthoringSnapshot is what a session reads from the store, as it stood when the session started.
type AuthoringSnapshot struct {
	Session AuditSession `json:"session"`
	// Sessions and Drafts are the session's own row, as its request's or its run's sessions list it.
	Sessions []AuthoringSession      `json:"sessions"`
	Drafts   []DraftSession          `json:"drafts"`
	Request  *QueryRequest           `json:"request,omitempty"`
	Run      *AnalysisRun            `json:"run,omitempty"`
	Catalog  []CatalogQuery          `json:"catalog"`
	Tests    map[string][]QueryTest  `json:"tests"`
	Trees    map[string]AuthoringAST `json:"trees"`
	// Picks are what a draft that waited its turn drafts from.
	Picks []DraftPick `json:"picks,omitempty"`
}

// AuthoringSnapshot reads what a session reads, for a worker to read in its place: its request or
// run, the catalog, and every query's tests and newest tree, since the session decides which query it
// is about as it works.
func (s *Store) AuthoringSnapshot(session int64) (AuthoringSnapshot, error) {
	meta, err := s.AuditSession(session)
	if err != nil {
		return AuthoringSnapshot{}, err
	}
	snap := AuthoringSnapshot{Session: meta, Sessions: []AuthoringSession{}, Drafts: []DraftSession{},
		Tests: map[string][]QueryTest{}}
	if meta.RequestID > 0 {
		requests, err := s.QueryRequests()
		if err != nil {
			return AuthoringSnapshot{}, err
		}
		for i := range requests {
			if requests[i].RowID == meta.RequestID {
				snap.Request = &requests[i]
			}
		}
		sessions, err := s.AuthoringSessions(meta.RequestID)
		if err != nil {
			return AuthoringSnapshot{}, err
		}
		for _, a := range sessions {
			if a.RowID == session {
				snap.Sessions = append(snap.Sessions, a)
			}
		}
	}
	if meta.RunID > 0 {
		run, err := s.AnalysisRun(meta.RunID)
		if err != nil {
			return AuthoringSnapshot{}, err
		}
		snap.Run = &run
		if snap.Picks, err = s.draftPicks(session); err != nil {
			return AuthoringSnapshot{}, err
		}
		drafts, err := s.DraftSessions(meta.RunID)
		if err != nil {
			return AuthoringSnapshot{}, err
		}
		for _, d := range drafts {
			if d.RowID == session {
				snap.Drafts = append(snap.Drafts, d)
			}
		}
	}
	if snap.Catalog, err = s.QueryCatalog(); err != nil {
		return AuthoringSnapshot{}, err
	}
	if snap.Catalog == nil {
		snap.Catalog = []CatalogQuery{}
	}
	tests, err := s.allQueryTests()
	if err != nil {
		return AuthoringSnapshot{}, err
	}
	for _, t := range tests {
		snap.Tests[t.QueryID] = append(snap.Tests[t.QueryID], t)
	}
	if snap.Trees, err = s.NewestASTs(); err != nil {
		return AuthoringSnapshot{}, err
	}
	return snap, nil
}

// IngestAuthoringReport replays a worker's report into the session that made it, in one transaction:
// whole or not at all, once, and only after the report before it. Every entry is kept against this
// session, and its request or run, whatever an entry names. The end the report carries is handed back.
func (s *Store) IngestAuthoringReport(session int64, r AuthoringReport) (*SessionEnd, error) {
	var end *SessionEnd
	for _, e := range r.Entries {
		if e.Kind == EntryEnd && e.End != nil {
			end = e.End
		}
	}
	if r.Seq < 1 || r.Digest == "" {
		return nil, errors.New("a report names its place in the journal and the digest it is known by")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var state, head string
	var request, run sql.NullInt64
	var seq int64
	err = tx.QueryRow(`SELECT state, request_id, run_id, journal_seq, journal_head
		FROM abom_authoring_session WHERE row_id = ?`, session).Scan(&state, &request, &run, &seq, &head)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("session %d is not on record", session)
	case err != nil:
		return nil, fmt.Errorf("reading session %d: %w", session, err)
	case r.Seq == seq && r.Digest != head:
		return nil, fmt.Errorf("report %d of session %d is not the one already kept", r.Seq, session)
	case r.Seq <= seq:
		// Kept already: a coordinator reads again what it may have read before it stopped.
		return end, nil
	case state != SessionRunning:
		return nil, fmt.Errorf("session %d is %s, so nothing more is kept for it", session, state)
	case r.Seq != seq+1:
		return nil, fmt.Errorf("report %d of session %d does not follow report %d, the last kept", r.Seq, session, seq)
	case r.Prev != head:
		return nil, fmt.Errorf("report %d of session %d does not follow the report kept before it", r.Seq, session)
	}
	in := ingest{tx: tx, session: session, request: request.Int64, run: run.Int64}
	if request.Valid {
		if err := tx.QueryRow(`SELECT query_id FROM abom_query_request WHERE row_id = ?`,
			request.Int64).Scan(&in.target); err != nil {
			return nil, fmt.Errorf("reading request %d: %w", request.Int64, err)
		}
	}
	for i, e := range r.Entries {
		if err := in.apply(e); err != nil {
			return nil, fmt.Errorf("entry %d of report %d of session %d: %w", i+1, r.Seq, session, err)
		}
	}
	if _, err := tx.Exec(`UPDATE abom_authoring_session SET journal_seq = ?, journal_head = ? WHERE row_id = ?`,
		r.Seq, r.Digest, session); err != nil {
		return nil, fmt.Errorf("advancing session %d's journal: %w", session, err)
	}
	return end, tx.Commit()
}

// ingest applies entries against one session, its request's current target, and its run.
type ingest struct {
	tx      *transaction
	session int64
	request int64
	run     int64
	target  string
}

func (in *ingest) apply(e AuthoringEntry) error {
	switch {
	case in.run != 0 && (e.Kind == EntryTarget || e.Kind == EntryTree || e.Kind == EntryTest):
		return fmt.Errorf("a %s, which a session drafting from a run does not make", e.Kind)
	case e.Kind == EntryEvent && e.Event != nil:
		event := *e.Event
		// The policy an event ran under is the one its session recorded last, found at replay.
		event.SessionID, event.PolicyID = in.session, 0
		_, err := recordAuthoringEvent(in.tx, event)
		return err
	case e.Kind == EntryTree && e.Tree != nil:
		tree := *e.Tree
		if tree.QueryID != "" && tree.QueryID != in.target {
			return fmt.Errorf("a tree printed for %s, which the session is not about", tree.QueryID)
		}
		tree.SessionID, tree.PrintedBy = in.session, ""
		_, err := recordAuthoringAST(in.tx, tree)
		return err
	case e.Kind == EntrySpend && e.Spend != nil:
		return addAuthoringSpend(in.tx, in.session, *e.Spend)
	case e.Kind == EntryModel && e.Model != "":
		return setAuthoringSessionModel(in.tx, in.session, e.Model)
	case e.Kind == EntryTarget && e.Target != nil:
		if err := decideTarget(in.tx, in.request, e.Target.QueryID, e.Target.Rationale); err != nil {
			return err
		}
		in.target = e.Target.QueryID
		return nil
	case e.Kind == EntryTest && e.Test != nil:
		if in.target == "" || e.Test.QueryID != in.target {
			return fmt.Errorf("a test of %q, which the session is not about", e.Test.QueryID)
		}
		return recordQueryTest(in.tx, *e.Test)
	case e.Kind == EntryDraft && e.Draft != nil:
		draft := *e.Draft
		draft.SessionID = in.session
		_, err := recordRequestDraft(in.tx, draft)
		return err
	case e.Kind == EntryEnd:
		return nil
	}
	return fmt.Errorf("an entry of kind %q carries nothing it can keep", e.Kind)
}
