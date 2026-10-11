package store

import (
	"reflect"
	"testing"
)

func TestAStoreSnapshotIsIndependentAndDoesNotWriteToItsSource(t *testing.T) {
	s := newTestStore(t)
	writer := NewWriter(s, 0)
	seedGraph(t, s, writer)
	if err := writer.Flush(); err != nil {
		t.Fatal(err)
	}
	before, err := s.DatabaseState()
	if err != nil {
		t.Fatal(err)
	}
	snapshotter, ok := any(s).(interface {
		SnapshotStore(string) (Backend, func() error, error)
	})
	if !ok {
		t.Fatal("store does not offer a backend-neutral snapshot")
	}
	snapshot, cleanup, err := snapshotter.SnapshotStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cleanup(); err != nil {
			t.Error(err)
		}
	})
	after, err := s.DatabaseState()
	if err != nil || before != after {
		t.Fatalf("snapshot changed its source: before=%v after=%v error=%v", before, after, err)
	}
	want, err := s.Summary()
	if err != nil {
		t.Fatal(err)
	}
	got, err := snapshot.Summary()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot summary=%+v error=%v want=%+v", got, err, want)
	}
	if _, err := s.UpsertRepository("source/new", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.UpsertRepository("snapshot/new", ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM repository WHERE repository_full_name = 'snapshot/new'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot write reached the source: count=%d error=%v", count, err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("snapshot cleanup is not idempotent: %v", err)
	}
	if _, err := s.Summary(); err != nil {
		t.Fatalf("snapshot cleanup damaged the source: %v", err)
	}
}
