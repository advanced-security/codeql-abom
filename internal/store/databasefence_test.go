package store

import (
	"errors"
	"testing"
	"time"
)

func TestAStaleLeaseCannotWriteThroughAnyStoreExecutionPath(t *testing.T) {
	s := newTestStore(t)
	lease, acquired, err := s.AcquireLease("fenced-writes", "first", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("lease: %+v %t %v", lease, acquired, err)
	}
	fenced := s.withLease(lease)
	if _, err := fenced.UpsertRepository("before/valid", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseLease(lease); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := s.AcquireLease("fenced-writes", "second", time.Minute); err != nil || !acquired {
		t.Fatalf("successor lease: %t %v", acquired, err)
	}
	if _, err := fenced.UpsertRepository("after/stale", ""); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("standalone write escaped its fence: %v", err)
	}
	if tx, err := fenced.db.Begin(); !errors.Is(err, ErrLeaseLost) {
		if tx != nil {
			_ = tx.Rollback()
		}
		t.Fatalf("transaction escaped its fence: %v", err)
	}
	var id int64
	if err := fenced.db.QueryRow(`INSERT INTO repository(repository_full_name) VALUES (?) RETURNING row_id`,
		"returning/stale").Scan(&id); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("returning write escaped its fence: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repository WHERE repository_full_name LIKE '%/stale'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("stale rows persisted: %d %v", count, err)
	}
	if err := fenced.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Summary(); err != nil {
		t.Fatalf("closing a fenced facade closed its owner: %v", err)
	}
}
