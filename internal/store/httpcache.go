package store

import "time"

// httpCacheDDL remembers the validator GitHub gave for a URL, and the answer it belonged to.
//
// Keyed on the URL because that is what `If-None-Match` is scoped to. A page of a paginated list
// is its own URL and so its own entry, which is what lets a partly changed list cost only the
// pages that actually changed.
const httpCacheDDL = `
CREATE TABLE IF NOT EXISTS abom_http_cache (
  url       TEXT PRIMARY KEY,
  etag      TEXT NOT NULL,
  body      BLOB NOT NULL,
  stored_at TEXT NOT NULL
);`

// HTTPCache is the store's answer to catalog.ResponseCache.
//
// Nothing here returns an error, and that is the point: a cache that could fail a read would be
// worse than no cache at all. Every failure is a miss, and a miss asks GitHub in full, which is
// what every read cost before this existed.
type HTTPCache struct{ s *Store }

// ResponseCache returns a validator cache backed by this database.
func (s *Store) ResponseCache() *HTTPCache { return &HTTPCache{s: s} }

// Lookup returns the validator and body held for url.
func (c *HTTPCache) Lookup(url string) (string, []byte, bool) {
	var etag string
	var body []byte
	if err := c.s.db.QueryRow(
		`SELECT etag, body FROM abom_http_cache WHERE url = ?`, url,
	).Scan(&etag, &body); err != nil {
		return "", nil, false
	}
	return etag, body, true
}

// Save records a validator and the answer it belongs to, replacing whatever the URL held before.
func (c *HTTPCache) Save(url, etag string, body []byte) {
	// Deliberately unchecked: see the type comment. A write that fails leaves the next read
	// unconditional, which is correct and merely costs what it used to.
	_, _ = c.s.db.Exec(`
INSERT INTO abom_http_cache (url, etag, body, stored_at) VALUES (?, ?, ?, ?)
ON CONFLICT(url) DO UPDATE SET
  etag = excluded.etag, body = excluded.body, stored_at = excluded.stored_at`,
		url, etag, body, time.Now().UTC().Format(time.RFC3339Nano))
}
