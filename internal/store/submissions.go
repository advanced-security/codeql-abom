package store

import (
	"database/sql"
	"errors"
)

// Submission is one snapshot ABOM wrote to GitHub.
type Submission struct {
	RowID       int64  `json:"rowId"`
	Repository  string `json:"repository"`
	SHA         string `json:"sha"`
	Correlator  string `json:"correlator"`
	Detector    string `json:"detector"`
	Manifest    string `json:"manifest"`
	PackageURLs int    `json:"packageUrls"`
	// PackageDigest identifies what was sent, which is what a renewal check compares against.
	PackageDigest string `json:"packageDigest,omitempty"`
	Released      bool   `json:"released"`
	Result        string `json:"result"`
	Message       string `json:"message,omitempty"`
	SubmittedAt   string `json:"submittedAt"`
	// Submitted distinguishes a real row from the zero value returned for a repository nobody has
	// submitted to. Never submitted and submitted nothing are different states.
	Submitted bool `json:"-"`
}

// RecordSubmission appends a submission. Superseding one is a second row, never an update: the
// earlier row is the only evidence of what was published before it.
func (s *Store) RecordSubmission(r Submission) error {
	_, err := s.DB().Exec(`
		INSERT INTO abom_submission (repository, sha, correlator, detector, manifest,
		                             package_urls, package_digest, released, result, message,
		                             submitted_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		r.Repository, r.SHA, r.Correlator, r.Detector, r.Manifest,
		r.PackageURLs, r.PackageDigest, r.Released, r.Result, r.Message, r.SubmittedAt)
	return err
}

// LastSubmission returns the newest submission that actually reached GitHub, or a zero value with
// Submitted false when there is none.
//
// Refusals are excluded deliberately. Nothing was written, so comparing a closure against one
// would measure the repository against a write that never happened.
func (s *Store) LastSubmission(repository string) (Submission, error) {
	var r Submission
	err := s.DB().QueryRow(`
		SELECT row_id, repository, COALESCE(sha,''), correlator, detector, COALESCE(manifest,''),
		       package_urls, COALESCE(package_digest,''), released, COALESCE(result,''),
		       COALESCE(message,''), submitted_at
		  FROM abom_submission
		 WHERE repository = ? AND result = 'SUCCESS'
		 ORDER BY submitted_at DESC, row_id DESC LIMIT 1`, repository).
		Scan(&r.RowID, &r.Repository, &r.SHA, &r.Correlator, &r.Detector, &r.Manifest,
			&r.PackageURLs, &r.PackageDigest, &r.Released, &r.Result, &r.Message, &r.SubmittedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, nil
	}
	if err != nil {
		return Submission{}, err
	}
	r.Submitted = true
	return r, nil
}

// Submissions returns every recorded submission, newest first, so the current state of a
// repository is the first row for it.
func (s *Store) Submissions() ([]Submission, error) {
	rows, err := s.DB().Query(`
		SELECT row_id, repository, COALESCE(sha,''), correlator, detector, COALESCE(manifest,''),
		       package_urls, COALESCE(package_digest,''), released, COALESCE(result,''),
		       COALESCE(message,''), submitted_at
		  FROM abom_submission ORDER BY submitted_at DESC, row_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Submission
	for rows.Next() {
		var r Submission
		if err := rows.Scan(&r.RowID, &r.Repository, &r.SHA, &r.Correlator, &r.Detector,
			&r.Manifest, &r.PackageURLs, &r.PackageDigest, &r.Released, &r.Result, &r.Message,
			&r.SubmittedAt); err != nil {
			return nil, err
		}
		r.Submitted = true
		out = append(out, r)
	}
	return out, rows.Err()
}
