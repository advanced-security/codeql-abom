package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// poolDDL holds the rate each pool may spend at and what each spent.
//
// Its own tables rather than `abom_observation`, which is what a CodeQL query said about a subject,
// or `abom_cycle_spend`, which counts a cycle's work rather than what a provider charged for it.
// A pool with no rate row has no rate: absence is the instruction "leave the work alone", which a
// rate of zero would contradict by holding it for ever.
const poolDDL = `
CREATE TABLE IF NOT EXISTS abom_pool_rate (
  pool     TEXT NOT NULL,
  unit     TEXT NOT NULL,
  per_hour INTEGER NOT NULL CHECK (per_hour > 0),
  burst    INTEGER NOT NULL DEFAULT 1 CHECK (burst > 0),
  set_at   TEXT NOT NULL,
  PRIMARY KEY (pool, unit)
);
CREATE TABLE IF NOT EXISTS abom_pool_spend (
  pool     TEXT NOT NULL,
  unit     TEXT NOT NULL,
  -- The UTC minute the spend fell in.
  bucket   TEXT NOT NULL,
  requests INTEGER NOT NULL DEFAULT 0,
  -- What the provider charged, never a difference between two readings of what is left.
  charged  INTEGER NOT NULL DEFAULT 0,
  -- What the provider last reported left in that minute; NULL where it reported nothing.
  reported_remaining INTEGER,
  reported_limit     INTEGER,
  reported_reset_at  TEXT,
  PRIMARY KEY (pool, unit, bucket)
);`

// PoolRate is the rate and burst an operator set for one pool.
type PoolRate struct {
	Pool    string `json:"pool"`
	Unit    string `json:"unit"`
	PerHour int    `json:"perHour"`
	Burst   int    `json:"burst"`
	SetAt   string `json:"setAt"`
}

// PoolSpend is what one pool spent in one minute, and what the provider last reported in it.
type PoolSpend struct {
	Pool       string
	Unit       string
	Bucket     time.Time
	Requests   int
	Charged    int
	HasReading bool
	Remaining  int
	Limit      int
	ResetAt    time.Time
}

// PoolAmount is spend over an interval.
type PoolAmount struct {
	Requests int `json:"requests"`
	Charged  int `json:"charged"`
}

// PoolReported is what a provider last reported to a pool, and the minute it said so in.
type PoolReported struct {
	Remaining int        `json:"remaining"`
	Limit     *int       `json:"limit"`
	ResetAt   *time.Time `json:"resetAt"`
	At        time.Time  `json:"at"`
}

// PoolMeter is one pool's spend over the minutes kept. Only a pool with a minute kept has one.
type PoolMeter struct {
	Pool     string     `json:"pool"`
	Unit     string     `json:"unit"`
	LastHour PoolAmount `json:"lastHour"`
	Total    PoolAmount `json:"total"`
	// Since is the oldest minute kept, which Total counts from; retention moves it forward.
	Since    time.Time     `json:"since"`
	Reported *PoolReported `json:"reported"`
}

// SetPoolRate records the rate and burst the operator chose for a pool.
func (s *Store) SetPoolRate(r PoolRate) error {
	if r.PerHour < 1 || r.Burst < 1 {
		return fmt.Errorf("a rate needs at least one an hour and a burst of at least one; "+
			"got %d and %d — clear the rate to remove it", r.PerHour, r.Burst)
	}
	_, err := s.db.Exec(`
		INSERT INTO abom_pool_rate (pool, unit, per_hour, burst, set_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pool, unit) DO UPDATE SET
			per_hour = excluded.per_hour, burst = excluded.burst, set_at = excluded.set_at`,
		r.Pool, r.Unit, r.PerHour, r.Burst, now())
	if err != nil {
		return fmt.Errorf("setting the rate of %s in %s: %w", r.Pool, r.Unit, err)
	}
	return nil
}

// ClearPoolRate removes a pool's rate, leaving its work alone.
func (s *Store) ClearPoolRate(pool, unit string) error {
	if _, err := s.db.Exec(`DELETE FROM abom_pool_rate WHERE pool = ? AND unit = ?`, pool, unit); err != nil {
		return fmt.Errorf("clearing the rate of %s in %s: %w", pool, unit, err)
	}
	return nil
}

// PoolRates lists every rate an operator set.
func (s *Store) PoolRates() ([]PoolRate, error) {
	rows, err := s.db.Query(`SELECT pool, unit, per_hour, burst, set_at FROM abom_pool_rate ORDER BY pool, unit`)
	if err != nil {
		return nil, fmt.Errorf("reading pool rates: %w", err)
	}
	defer rows.Close()
	out := []PoolRate{}
	for rows.Next() {
		var r PoolRate
		if err := rows.Scan(&r.Pool, &r.Unit, &r.PerHour, &r.Burst, &r.SetAt); err != nil {
			return nil, fmt.Errorf("scanning a pool rate: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddPoolSpend adds what pools spent. A minute written twice adds up, and a write carrying no
// reading keeps the one the minute already holds.
func (s *Store) AddPoolSpend(rows []PoolSpend) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording pool spend: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
		INSERT INTO abom_pool_spend
			(pool, unit, bucket, requests, charged, reported_remaining, reported_limit, reported_reset_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pool, unit, bucket) DO UPDATE SET
			requests = requests + excluded.requests,
			charged = charged + excluded.charged,
			reported_limit = CASE WHEN excluded.reported_remaining IS NULL
			                      THEN reported_limit ELSE excluded.reported_limit END,
			reported_reset_at = CASE WHEN excluded.reported_remaining IS NULL
			                         THEN reported_reset_at ELSE excluded.reported_reset_at END,
			reported_remaining = COALESCE(excluded.reported_remaining, reported_remaining)`)
	if err != nil {
		return fmt.Errorf("recording pool spend: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		var remaining, limit, reset any
		if r.HasReading {
			remaining = r.Remaining
			if r.Limit > 0 {
				limit = r.Limit
			}
			if !r.ResetAt.IsZero() {
				reset = r.ResetAt.UTC().Format(time.RFC3339)
			}
		}
		if _, err := stmt.Exec(r.Pool, r.Unit, r.Bucket.UTC().Format(time.RFC3339),
			r.Requests, r.Charged, remaining, limit, reset); err != nil {
			return fmt.Errorf("recording what %s spent: %w", r.Pool, err)
		}
	}
	return tx.Commit()
}

// PoolMeters reads each pool's spend since a time and over every minute kept, with the last reading it was given.
// A pool with no minute kept has no meter, which is a different answer from one that spent nothing.
func (s *Store) PoolMeters(since time.Time) ([]PoolMeter, error) {
	from := since.UTC().Format(time.RFC3339)
	rows, err := s.db.Query(`
		SELECT pool, unit,
		       COALESCE(SUM(CASE WHEN bucket >= ? THEN requests END), 0),
		       COALESCE(SUM(CASE WHEN bucket >= ? THEN charged END), 0),
		       SUM(requests), SUM(charged), MIN(bucket)
		FROM abom_pool_spend
		GROUP BY pool, unit
		ORDER BY pool, unit`, from, from)
	if err != nil {
		return nil, fmt.Errorf("reading pool spend: %w", err)
	}
	out := []PoolMeter{}
	for rows.Next() {
		var m PoolMeter
		var first string
		if err := rows.Scan(&m.Pool, &m.Unit, &m.LastHour.Requests, &m.LastHour.Charged,
			&m.Total.Requests, &m.Total.Charged, &first); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning pool spend: %w", err)
		}
		m.Since, _ = time.Parse(time.RFC3339, first)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("reading pool spend: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Reported, err = s.poolReported(out[i].Pool, out[i].Unit); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) poolReported(pool, unit string) (*PoolReported, error) {
	var r PoolReported
	var limit sql.NullInt64
	var reset sql.NullString
	var at string
	err := s.db.QueryRow(`
		SELECT reported_remaining, reported_limit, reported_reset_at, bucket
		FROM abom_pool_spend
		WHERE pool = ? AND unit = ? AND reported_remaining IS NOT NULL
		ORDER BY bucket DESC LIMIT 1`, pool, unit).Scan(&r.Remaining, &limit, &reset, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading what %s was last told: %w", pool, err)
	}
	if limit.Valid {
		v := int(limit.Int64)
		r.Limit = &v
	}
	if t, err := time.Parse(time.RFC3339, reset.String); reset.Valid && err == nil {
		r.ResetAt = &t
	}
	r.At, _ = time.Parse(time.RFC3339, at)
	return &r, nil
}

// FreshnessWork is the work queued behind the pools a freshness estimate is derived for.
type FreshnessWork struct {
	// MovingReferences are the read references the recheck sweep asks about: resolved and not
	// pinned to a commit, whenever they were last asked.
	MovingReferences int `json:"movingReferences"`
	// QueuedReferences are the repository references waiting to be fetched.
	QueuedReferences int `json:"queuedReferences"`
}

// ReadFreshnessWork counts the work queued behind the probing and fetching pools, inside bound.
func (s *Store) ReadFreshnessWork(bound QueueBound) (FreshnessWork, error) {
	var w FreshnessWork
	inside, args := bound.Admits("")
	err := s.db.QueryRow(`
		SELECT
		  (SELECT COUNT(*) FROM abom_frontier
		   WHERE status IN ('done', 'nothing')
		     AND resolved_sha IS NOT NULL AND resolved_sha <> ''
		     AND (analyzed_ref IS NULL OR analyzed_ref <> resolved_sha)
		     AND NOT (length(ref) = 40 AND ref NOT GLOB '*[^0-9a-fA-F]*')
		     AND `+inside+`),
		  (SELECT COUNT(*) FROM (SELECT DISTINCT owner, repo, ref FROM abom_frontier
		                         WHERE status = 'pending' AND `+inside+`))`,
		append(append([]any{}, args...), args...)...).
		Scan(&w.MovingReferences, &w.QueuedReferences)
	if err != nil {
		return w, fmt.Errorf("counting the work queued behind the pools: %w", err)
	}
	return w, nil
}
