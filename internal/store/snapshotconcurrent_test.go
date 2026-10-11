package store

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestASnapshotReadsOneCommitWhileTheSourceKeepsChanging(t *testing.T) {
	s := newTestStore(t)
	initial := time.Unix(0, 0).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`INSERT INTO abom_discovery_state(id, running, changed_at) VALUES (1, 0, ?)`, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO abom_saved_view(name, predicate, created_at, vocabulary)
		VALUES ('snapshot', '', ?, 1)`, initial); err != nil {
		t.Fatal(err)
	}
	var other *Store
	var err error
	if s.db.postgres {
		other, err = openPostgres(s.dsn, Options{})
	} else {
		other, err = Open(s.dsn, Options{})
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	stop, ready, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var writes atomic.Int64
	var once sync.Once
	halt := func() { once.Do(func() { close(stop) }) }
	t.Cleanup(halt)
	go func() {
		for i := int64(1); ; i++ {
			select {
			case <-stop:
				finished <- nil
				return
			default:
			}
			err := updateSnapshotPair(other, time.Unix(i, 0).UTC().Format(time.RFC3339Nano))
			if err != nil {
				if i == 1 {
					close(ready)
				}
				finished <- err
				return
			}
			writes.Add(1)
			if i == 1 {
				close(ready)
			}
		}
	}()
	<-ready
	before := writes.Load()
	snapshot, cleanup, err := s.SnapshotStore(t.TempDir())
	halt()
	if writerErr := <-finished; writerErr != nil {
		t.Fatal(writerErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	}()
	if writes.Load() <= before {
		t.Fatal("source did not change while the snapshot was made")
	}
	var discovery, view string
	if err := snapshot.(*Store).db.QueryRow(`SELECT d.changed_at, v.created_at
		FROM abom_discovery_state d CROSS JOIN abom_saved_view v WHERE v.name = 'snapshot'`).
		Scan(&discovery, &view); err != nil {
		t.Fatal(err)
	}
	if discovery != view {
		t.Fatalf("snapshot mixed commits: discovery=%s view=%s", discovery, view)
	}
}

func updateSnapshotPair(s *Store, stamp string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE abom_discovery_state SET changed_at = ?`, stamp); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE abom_saved_view SET created_at = ? WHERE name = 'snapshot'`, stamp); err != nil {
		return err
	}
	return tx.Commit()
}
