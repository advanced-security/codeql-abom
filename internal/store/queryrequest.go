package store

import (
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
  updated_at    TEXT NOT NULL
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

var (
	requestKinds    = []string{"", RequestMissed, RequestFalseAlarm, RequestBoth}
	requestStatuses = []string{RequestOpen, RequestInProgress, RequestDone, RequestDeclined}
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
	QueryID     string `json:"queryId"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	RequestedBy string `json:"requestedBy"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// QueryRequestKinds and QueryRequestStatuses are the vocabularies, for a control to offer.
func QueryRequestKinds() []string { return slices.Clone(requestKinds) }

func QueryRequestStatuses() []string { return slices.Clone(requestStatuses) }

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
		res, err := s.db.Exec(`
INSERT INTO abom_query_request
  (description, compliant, non_compliant, query_id, kind, status, requested_by, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.Description, r.Compliant, r.NonCompliant, r.QueryID, r.Kind, r.Status,
			r.RequestedBy, now, now)
		if err != nil {
			return 0, fmt.Errorf("keeping a query request: %w", err)
		}
		return res.LastInsertId()
	}

	res, err := s.db.Exec(`
UPDATE abom_query_request SET description = ?, compliant = ?, non_compliant = ?, query_id = ?,
  kind = ?, status = ?, updated_at = ?
WHERE row_id = ?`,
		r.Description, r.Compliant, r.NonCompliant, r.QueryID, r.Kind, r.Status, now, r.RowID)
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
	return nil
}

// QueryRequests lists every request, newest first.
func (s *Store) QueryRequests() ([]QueryRequest, error) {
	out := []QueryRequest{}
	rows, err := s.db.Query(`
		SELECT row_id, description, compliant, non_compliant, query_id, kind, status,
		       requested_by, created_at, updated_at
		FROM abom_query_request ORDER BY row_id DESC`)
	if err != nil {
		return nil, fmt.Errorf("reading query requests: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r QueryRequest
		if err := rows.Scan(&r.RowID, &r.Description, &r.Compliant, &r.NonCompliant, &r.QueryID,
			&r.Kind, &r.Status, &r.RequestedBy, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning a query request: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
