package store

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// authoringReviewDDL keeps what an operator made of a query's report, with the report they read.
const authoringReviewDDL = `
-- Append-only: an approval is what made a report the expected result, and a rejection is what the
-- next session is told.
CREATE TABLE IF NOT EXISTS abom_authoring_review (
  row_id        INTEGER PRIMARY KEY,
  request_id    INTEGER NOT NULL REFERENCES abom_query_request(row_id),
  query_id      TEXT NOT NULL,
  query_version TEXT NOT NULL,
  test_digest   TEXT NOT NULL DEFAULT '',
  actual        TEXT NOT NULL,
  verdict       TEXT NOT NULL,
  reason        TEXT NOT NULL DEFAULT '',
  reviewed_by   TEXT NOT NULL,
  reviewed_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_authoring_review_request ON abom_authoring_review(request_id, row_id);`

// What an operator made of a report.
const (
	ReviewApproved = "approved"
	ReviewRejected = "rejected"
)

// AuthoringReview is an operator's decision about what a query's tests reported.
type AuthoringReview struct {
	RowID        int64  `json:"rowId"`
	RequestID    int64  `json:"requestId"`
	QueryID      string `json:"queryId"`
	QueryVersion string `json:"queryVersion"`
	// TestDigest identifies the fixtures the report came from.
	TestDigest string `json:"testDigest"`
	// Actual is the report as the operator read it.
	Actual     string `json:"actual"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason"`
	ReviewedBy string `json:"reviewedBy"`
	ReviewedAt string `json:"reviewedAt"`
}

// RecordAuthoringReview keeps an operator's decision about a report.
func (s *Store) RecordAuthoringReview(r AuthoringReview) (int64, error) {
	switch {
	case r.Verdict != ReviewApproved && r.Verdict != ReviewRejected:
		return 0, fmt.Errorf("a review is %s or %s, not %q", ReviewApproved, ReviewRejected, r.Verdict)
	case r.Verdict == ReviewRejected && strings.TrimSpace(r.Reason) == "":
		return 0, errors.New("a rejection says why, because the next session is told")
	case strings.TrimSpace(r.ReviewedBy) == "":
		return 0, errors.New("a review records who made it")
	case r.QueryID == "" || r.QueryVersion == "":
		return 0, errors.New("a review names the query version whose report it decides")
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_query_request WHERE row_id = ?`,
		r.RequestID).Scan(&exists); err != nil {
		return 0, fmt.Errorf("reading request %d: %w", r.RequestID, err)
	}
	if exists == 0 {
		return 0, fmt.Errorf("reviewing request %d: %w", r.RequestID, ErrNoQueryRequest)
	}
	res, err := s.db.Exec(`
		INSERT INTO abom_authoring_review
		  (request_id, query_id, query_version, test_digest, actual, verdict, reason, reviewed_by, reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RequestID, r.QueryID, r.QueryVersion, r.TestDigest, r.Actual, r.Verdict, r.Reason, r.ReviewedBy,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, fmt.Errorf("recording a review of request %d: %w", r.RequestID, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	return res.LastInsertId()
}

// AuthoringReviews lists the reviews of a request's reports, newest first.
func (s *Store) AuthoringReviews(request int64) ([]AuthoringReview, error) {
	rows, err := s.db.Query(`
		SELECT row_id, request_id, query_id, query_version, test_digest, actual, verdict, reason,
		       reviewed_by, reviewed_at
		FROM abom_authoring_review WHERE request_id = ?
		ORDER BY row_id DESC`, request)
	if err != nil {
		return nil, fmt.Errorf("reading the reviews of request %d: %w", request, err)
	}
	defer rows.Close()

	out := []AuthoringReview{}
	for rows.Next() {
		var r AuthoringReview
		if err := rows.Scan(&r.RowID, &r.RequestID, &r.QueryID, &r.QueryVersion, &r.TestDigest, &r.Actual,
			&r.Verdict, &r.Reason, &r.ReviewedBy, &r.ReviewedAt); err != nil {
			return nil, fmt.Errorf("scanning a review: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
