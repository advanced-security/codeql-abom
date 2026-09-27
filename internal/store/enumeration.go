package store

import (
	"database/sql"
	"errors"
	"strings"
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
// an unknown shape, not zero repositories. See docs/architecture.md on reading an account.
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
);
-- A declared repository a full listing of its account did not name, and when. Only a full listing
-- writes a row, and seeing the repository in any listing removes it: a prefix cannot establish
-- absence, but it can establish presence. A row takes the repository out of the declared scope
-- and removes nothing from the graph; what it alone reached is what the prune offers.
CREATE TABLE IF NOT EXISTS abom_repository_absence (
  repository TEXT PRIMARY KEY COLLATE NOCASE,
  missing_at TEXT NOT NULL
);`

// RecordRepositoryListing records which of an account's declared repositories a listing named,
// and returns how many a full listing did not.
//
// A repository named is present, whichever listing named it. One a full listing did not name is
// missing from then on, keeping when it first went missing across later listings; a sweep leaves
// that alone either way, since the repositories it did not reach were never looked for.
func (s *Store) RecordRepositoryListing(owner string, seen []string, full bool) (int, error) {
	held, err := queryAll(s, "reading declared repositories for listing", func(rows *sql.Rows) (string, error) {
		var name string
		return name, rows.Scan(&name)
	}, `SELECT repository_full_name FROM repository r
	     WHERE LOWER(SUBSTR(r.repository_full_name, 1, INSTR(r.repository_full_name, '/') - 1)) = LOWER(?)
	       AND `+declaredFullName("r.repository_full_name"), owner)
	if err != nil {
		return 0, err
	}
	present := make(map[string]bool, len(seen))
	for _, name := range seen {
		present[strings.ToLower(name)] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	missing := 0
	at := now()
	for _, name := range held {
		if present[strings.ToLower(name)] {
			_, err = tx.Exec(`DELETE FROM abom_repository_absence WHERE repository = ?`, name)
		} else if full {
			missing++
			_, err = tx.Exec(`INSERT INTO abom_repository_absence (repository, missing_at) VALUES (?, ?)
				ON CONFLICT(repository) DO NOTHING`, name, at)
		}
		if err != nil {
			return 0, err
		}
	}
	return missing, tx.Commit()
}

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
	// Missing names the declared repositories a full listing of this account did not, which are
	// no longer the operator's. Empty says nothing is missing only where ListedAt is set.
	Missing []RepositoryAbsence `json:"missing"`
}

// RepositoryAbsence is a declared repository a full listing did not name, and since when.
type RepositoryAbsence struct {
	Repository string    `json:"repository"`
	MissingAt  time.Time `json:"missingAt"`
}

// EnumerationState reports the marks for one account, and whether it has ever been read.
func (s *Store) EnumerationState(owner string) (EnumerationMarks, bool, error) {
	var (
		marks                                      = EnumerationMarks{Owner: owner, Missing: []RepositoryAbsence{}}
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
		missing, err := s.missingFrom(owner)
		if err != nil {
			return marks, false, err
		}
		marks.Missing = missing
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
	// Into a local, so a read that fails leaves the empty list rather than a nil one.
	missing, err := s.missingFrom(owner)
	if err != nil {
		return marks, true, err
	}
	marks.Missing = missing
	return marks, true, nil
}

// missingFrom lists what a full listing of one account did not name and the declaration still
// covers, oldest absence first. One the declaration stopped covering is not something the operator
// lost, whatever a listing recorded about it before.
func (s *Store) missingFrom(owner string) ([]RepositoryAbsence, error) {
	rows, err := s.db.Query(`
		SELECT a.repository, a.missing_at FROM abom_repository_absence a
		WHERE LOWER(SUBSTR(a.repository, 1, INSTR(a.repository, '/') - 1)) = LOWER(?)
		  AND `+declaredFullName("a.repository")+`
		ORDER BY a.missing_at, a.repository`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RepositoryAbsence{}
	for rows.Next() {
		var a RepositoryAbsence
		var at sql.NullString
		if err := rows.Scan(&a.Repository, &at); err != nil {
			return nil, err
		}
		a.MissingAt = parseMark(at)
		out = append(out, a)
	}
	return out, rows.Err()
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

// MissingRepositories names every repository a full listing of its account no longer names,
// lower-cased, for a caller deciding in Go what the declared scope holds.
func (s *Store) MissingRepositories() (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT LOWER(repository) FROM abom_repository_absence`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
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
