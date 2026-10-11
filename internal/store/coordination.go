package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const coordinationDDL = `
CREATE TABLE IF NOT EXISTS abom_coordination_lease (
  resource TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  fence INTEGER NOT NULL,
  expires_ms INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS abom_ingest_receipt (
  attempt_id TEXT PRIMARY KEY,
  digest TEXT NOT NULL,
  resource TEXT NOT NULL,
  owner TEXT NOT NULL,
  fence INTEGER NOT NULL,
  status TEXT NOT NULL,
  finished_at TEXT
);`

var (
	ErrLeaseLost   = errors.New("the coordinator no longer owns its lease")
	ErrReceiptBusy = errors.New("the ingestion attempt is still in progress")
)

type Lease struct {
	Resource  string
	Owner     string
	Fence     int64
	ExpiresAt time.Time
}

type CoordinationStore interface {
	WithLease(lease Lease) (Backend, error)
	ActiveLease() (Lease, bool)
	AcquireLease(resource, owner string, ttl time.Duration) (Lease, bool, error)
	RenewLease(lease Lease, ttl time.Duration) (Lease, bool, error)
	ReleaseLease(lease Lease) error
	BeginReceipt(id, digest string, lease Lease) (bool, error)
	CompleteReceipt(id, digest string, lease Lease) error
	AbortReceipt(id string, lease Lease) error
}

func leaseDuration(ttl time.Duration) error {
	if ttl < time.Millisecond {
		return errors.New("a coordinator lease lasts at least one millisecond")
	}
	return nil
}

func (s *Store) leaseNow(tx *transaction) (int64, error) {
	query := "SELECT CAST(unixepoch('subsec') * 1000 AS INTEGER)"
	if s.db.postgres {
		query = "SELECT CAST(EXTRACT(EPOCH FROM clock_timestamp()) * 1000 AS BIGINT)"
	}
	var ms int64
	if err := tx.QueryRow(query).Scan(&ms); err != nil {
		return 0, fmt.Errorf("reading the database clock: %w", err)
	}
	return ms, nil
}

func (s *Store) readLease(tx *transaction, resource string) (Lease, int64, error) {
	query := `SELECT owner, fence, expires_ms FROM abom_coordination_lease WHERE resource = ?`
	if s.db.postgres {
		query += " FOR UPDATE"
	}
	lease := Lease{Resource: resource}
	var expires int64
	if err := tx.QueryRow(query, resource).Scan(&lease.Owner, &lease.Fence, &expires); err != nil {
		return Lease{}, 0, err
	}
	lease.ExpiresAt = time.UnixMilli(expires).UTC()
	return lease, expires, nil
}

func (s *Store) AcquireLease(resource, owner string, ttl time.Duration) (Lease, bool, error) {
	if resource == "" || owner == "" {
		return Lease{}, false, errors.New("a coordinator lease names its resource and owner")
	}
	if err := leaseDuration(ttl); err != nil {
		return Lease{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Lease{}, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO abom_coordination_lease (resource, owner, fence, expires_ms)
		VALUES (?, '', 0, 0) ON CONFLICT(resource) DO NOTHING`, resource); err != nil {
		return Lease{}, false, fmt.Errorf("creating coordinator lease: %w", err)
	}
	prior, expires, err := s.readLease(tx, resource)
	if err != nil {
		return Lease{}, false, err
	}
	now, err := s.leaseNow(tx)
	if err != nil {
		return Lease{}, false, err
	}
	if prior.Owner != "" && expires > now {
		return prior, false, nil
	}
	until := now + ttl.Milliseconds()
	next := Lease{Resource: resource, Owner: owner, Fence: prior.Fence + 1, ExpiresAt: time.UnixMilli(until).UTC()}
	if _, err := tx.Exec(`UPDATE abom_coordination_lease SET owner = ?, fence = ?, expires_ms = ?
		WHERE resource = ?`, owner, next.Fence, until, resource); err != nil {
		return Lease{}, false, fmt.Errorf("acquiring coordinator lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Lease{}, false, err
	}
	return next, true, nil
}

func (s *Store) validateLease(tx *transaction, lease Lease) error {
	held, expires, err := s.readLease(tx, lease.Resource)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("reading coordinator lease: %w", err)
	}
	now, err := s.leaseNow(tx)
	if err != nil {
		return err
	}
	if held.Owner != lease.Owner || held.Fence != lease.Fence || expires <= now {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) RenewLease(lease Lease, ttl time.Duration) (Lease, bool, error) {
	if err := leaseDuration(ttl); err != nil {
		return Lease{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Lease{}, false, err
	}
	defer tx.Rollback()
	if err := s.validateLease(tx, lease); errors.Is(err, ErrLeaseLost) {
		return lease, false, nil
	} else if err != nil {
		return Lease{}, false, err
	}
	now, err := s.leaseNow(tx)
	if err != nil {
		return Lease{}, false, err
	}
	until := now + ttl.Milliseconds()
	if _, err := tx.Exec(`UPDATE abom_coordination_lease SET expires_ms = ? WHERE resource = ?`,
		until, lease.Resource); err != nil {
		return Lease{}, false, fmt.Errorf("renewing coordinator lease: %w", err)
	}
	lease.ExpiresAt = time.UnixMilli(until).UTC()
	if err := tx.Commit(); err != nil {
		return Lease{}, false, err
	}
	return lease, true, nil
}

func (s *Store) ReleaseLease(lease Lease) error {
	result, err := s.db.Exec(`UPDATE abom_coordination_lease SET owner = '', expires_ms = 0
		WHERE resource = ? AND owner = ? AND fence = ?`, lease.Resource, lease.Owner, lease.Fence)
	if err != nil {
		return fmt.Errorf("releasing coordinator lease: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) BeginReceipt(id, digest string, lease Lease) (bool, error) {
	if id == "" || digest == "" {
		return false, errors.New("an ingestion receipt names its attempt and evidence digest")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := s.validateLease(tx, lease); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`INSERT INTO abom_ingest_receipt
		(attempt_id, digest, resource, owner, fence, status) VALUES (?, ?, ?, ?, ?, 'pending')
		ON CONFLICT(attempt_id) DO NOTHING`,
		id, digest, lease.Resource, lease.Owner, lease.Fence); err != nil {
		return false, fmt.Errorf("creating ingestion receipt: %w", err)
	}
	var previous, resource, state, owner string
	var fence int64
	query := `SELECT digest, resource, status, owner, fence FROM abom_ingest_receipt WHERE attempt_id = ?`
	if s.db.postgres {
		query += " FOR UPDATE"
	}
	err = tx.QueryRow(query, id).Scan(&previous, &resource, &state, &owner, &fence)
	switch {
	case err != nil:
		return false, err
	case previous != digest || resource != lease.Resource:
		return false, errors.New("an ingestion attempt cannot name different evidence")
	case state == "committed":
		return false, nil
	case state == "running" && owner == lease.Owner && fence == lease.Fence:
		return false, ErrReceiptBusy
	}
	if _, err := tx.Exec(`UPDATE abom_ingest_receipt SET owner = ?, fence = ?,
		status = 'running', finished_at = NULL WHERE attempt_id = ?`,
		lease.Owner, lease.Fence, id); err != nil {
		return false, fmt.Errorf("claiming ingestion receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) CompleteReceipt(id, digest string, lease Lease) error {
	return s.finishReceipt(id, digest, lease, true)
}

func (s *Store) AbortReceipt(id string, lease Lease) error {
	return s.finishReceipt(id, "", lease, false)
}

func (s *Store) finishReceipt(id, digest string, lease Lease, committed bool) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.validateLease(tx, lease); err != nil {
		return err
	}
	state, finished := "pending", ""
	if committed {
		state, finished = "committed", now()
	}
	result, err := tx.Exec(`UPDATE abom_ingest_receipt SET status = ?, finished_at = ?
		WHERE attempt_id = ? AND resource = ? AND owner = ? AND fence = ? AND status = 'running'
		  AND (? = '' OR digest = ?)`, state, nullable(finished), id, lease.Resource, lease.Owner,
		lease.Fence, digest, digest)
	if err != nil {
		return fmt.Errorf("settling ingestion receipt: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("the ingestion attempt is not owned by this coordinator")
	}
	return tx.Commit()
}
