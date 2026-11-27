package store

import (
	"database/sql"
	"errors"
	"time"
)

// sessionDDL holds who is signed in, so a restart or a rollout signs nobody out. A session is named
// by the digest of its cookie, never the cookie: a copy of the database signs nobody in.
const sessionDDL = `
CREATE TABLE IF NOT EXISTS abom_session (
  digest TEXT PRIMARY KEY,
  record TEXT NOT NULL,
  expires_at TEXT NOT NULL
);`

// SessionStore keeps signed-in sessions, each an opaque record the access package writes.
type SessionStore interface {
	// PutSession keeps a session's record until it expires, replacing what was kept for it, and
	// removes the sessions that have expired by now.
	PutSession(digest string, record []byte, now, expires time.Time) error
	// Session is the record kept for a live session, or false when there is none by now.
	Session(digest string, now time.Time) ([]byte, bool, error)
	DeleteSession(digest string) error
}

func (s *Store) PutSession(digest string, record []byte, now, expires time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM abom_session WHERE expires_at < ?`, sessionTime(now)); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO abom_session (digest, record, expires_at) VALUES (?, ?, ?)
		ON CONFLICT (digest) DO UPDATE SET record = excluded.record, expires_at = excluded.expires_at`,
		digest, string(record), sessionTime(expires))
	return err
}

func (s *Store) Session(digest string, now time.Time) ([]byte, bool, error) {
	var record string
	err := s.db.QueryRow(`SELECT record FROM abom_session WHERE digest = ? AND expires_at >= ?`,
		digest, sessionTime(now)).Scan(&record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(record), true, nil
}

func (s *Store) DeleteSession(digest string) error {
	_, err := s.db.Exec(`DELETE FROM abom_session WHERE digest = ?`, digest)
	return err
}

// sessionTime writes a time so that text order is time order: UTC, at a fixed width.
func sessionTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
