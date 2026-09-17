package store

import (
	"database/sql"
	"errors"
	"time"
)

// enumerationStateDDL remembers what reading an account established, so the next read can be
// bounded by what moved rather than by how big the account is.
//
// `listed_at` and `swept_at` are separate columns because they are different claims. A full
// listing is the only thing that can establish a repository is gone; a sorted sweep reads a prefix
// and can never mention one. Folding them into a single "last read" would let an account that has
// not checked for deletions in a week report that it checked a minute ago.
//
// Every column is nullable, and an absent row is its own answer: an account nobody has listed has
// an unknown shape, not zero repositories. See plans/api/org-enumeration.md.
const enumerationStateDDL = `
CREATE TABLE IF NOT EXISTS abom_enumeration_state (
  owner        TEXT PRIMARY KEY COLLATE NOCASE,
  -- Which route answered, stated rather than inferred, so a figure that differs between routes
  -- can be attributed instead of argued about.
  route        TEXT,
  listed_at    TEXT,
  swept_at     TEXT,
  high_created TEXT,
  high_pushed  TEXT,
  -- How many pages the last full listing took. A sweep is two ordered walks, so an account that
  -- fits on one page costs 2 to sweep and 1 to read whole: the incremental read is only a saving
  -- above two pages, and below that it also gives up seeing deletions.
  listed_pages INTEGER
);`

// EnumerationMarks is what the last read of one account established.
type EnumerationMarks struct {
	Owner string `json:"owner"`
	// Route names which of GraphQL or REST answered last.
	Route string `json:"route,omitempty"`
	// ListedAt is when every page was last read. Zero means never, and until it is non-zero
	// nothing in the corpus can say a repository was deleted rather than merely unseen.
	ListedAt time.Time `json:"listedAt,omitzero"`
	// SweptAt is when an incremental read last ran.
	SweptAt time.Time `json:"sweptAt,omitzero"`
	// HighCreated and HighPushed are the newest creation and push a read has seen. They are what
	// the next sorted walk stops below, and they only ever move forward.
	HighCreated time.Time `json:"highCreated,omitzero"`
	HighPushed  time.Time `json:"highPushed,omitzero"`
	// ListedPages is how many pages the last full listing took. Zero means no full listing has
	// run, which is not the same as an account that took no pages to read.
	ListedPages int `json:"listedPages"`
}

// EnumerationState reports the marks for one account, and whether it has ever been read.
func (s *Store) EnumerationState(owner string) (EnumerationMarks, bool, error) {
	var (
		marks                                      = EnumerationMarks{Owner: owner}
		route                                      sql.NullString
		listedAt, sweptAt, highCreated, highPushed sql.NullString
		pages                                      sql.NullInt64
	)
	err := s.db.QueryRow(`
SELECT route, listed_at, swept_at, high_created, high_pushed, listed_pages
  FROM abom_enumeration_state WHERE owner = ?`, owner).
		Scan(&route, &listedAt, &sweptAt, &highCreated, &highPushed, &pages)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return marks, false, nil
	case err != nil:
		return marks, false, err
	}
	marks.Route = route.String
	marks.ListedAt = parseMark(listedAt)
	marks.SweptAt = parseMark(sweptAt)
	marks.HighCreated = parseMark(highCreated)
	marks.HighPushed = parseMark(highPushed)
	marks.ListedPages = int(pages.Int64)
	return marks, true, nil
}

// RecordFullListing records that every page of an account was read, with the marks it established
// and what it cost.
func (s *Store) RecordFullListing(
	owner, route string, pages int, highCreated, highPushed time.Time,
) error {
	if err := s.recordEnumeration(owner, route, "listed_at", highCreated, highPushed); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`UPDATE abom_enumeration_state SET listed_pages = ? WHERE owner = ?`, pages, owner)
	return err
}

// RecordSweep records an incremental read. It advances the marks and deliberately leaves
// `listed_at` alone, because a prefix of a sorted order has not seen the whole account.
func (s *Store) RecordSweep(owner, route string, highCreated, highPushed time.Time) error {
	return s.recordEnumeration(owner, route, "swept_at", highCreated, highPushed)
}

// recordEnumeration writes one read, stamping the named column and moving the marks forward.
//
// MAX over the stored value rather than assignment: a sweep that found nothing carries the zero
// time, and letting that win would drag the high-water backwards and turn the next early stop into
// a full walk. String comparison is correct here because RFC 3339 in UTC sorts chronologically,
// and every value written by parseMark's counterpart below is in that form.
func (s *Store) recordEnumeration(owner, route, stamp string, highCreated, highPushed time.Time) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(`
INSERT INTO abom_enumeration_state (owner, route, `+stamp+`, high_created, high_pushed)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(owner) DO UPDATE SET
  route        = excluded.route,
  `+stamp+`    = excluded.`+stamp+`,
  high_created = MAX(COALESCE(abom_enumeration_state.high_created, ''), COALESCE(excluded.high_created, '')),
  high_pushed  = MAX(COALESCE(abom_enumeration_state.high_pushed, ''), COALESCE(excluded.high_pushed, ''))`,
		owner, route, now, formatMark(highCreated), formatMark(highPushed))
	return err
}

// formatMark renders a time for storage, and a zero time as NULL rather than as year one.
func formatMark(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseMark reads a stored time, answering the zero time for anything it cannot.
func parseMark(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return time.Time{}
	}
	return t
}
