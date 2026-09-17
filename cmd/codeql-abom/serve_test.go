package main

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A database migrated in place and never crawled carries no boundary on any edge, because the
// only thing that classifies them runs inside a discovery cycle. Every boundary filter then
// answers nothing, which reads as "no cross-owner dependencies" rather than "never measured".
func TestServingClassifiesEdgesNoCycleHasClassified(t *testing.T) {
	db := prunableDB(t)
	if unclassifiedEdgeCount(t, db) == 0 {
		t.Fatal("fixture arrives classified, so this test would pass against an empty change")
	}

	serveUntilOpened(t, db)

	if n := unclassifiedEdgeCount(t, db); n != 0 {
		t.Errorf("%d edges unclassified after serving; a boundary filter would answer nothing", n)
	}
}

// serveUntilOpened runs the open-time work and stops. The context is canceled before the call,
// so serve reaches its shutdown select immediately rather than waiting for a signal that a test
// has no way to send.
//
// The writer is locked because serve hands it to the goroutine that prints the listening banner
// and returns without waiting for it: reading a plain bytes.Buffer afterwards is a race, and the
// detector finds it every run.
func serveUntilOpened(t *testing.T, db string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := &lockedWriter{}
	if err := runServe(ctx, []string{"--db", db, "--addr", "127.0.0.1:0"}, out, out); err != nil {
		t.Fatalf("serve --db %s: %v (%s)", db, err, out.String())
	}
}

type lockedWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func unclassifiedEdgeCount(t *testing.T, db string) int {
	t.Helper()
	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer s.Close()
	var n int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM abom_edge_live WHERE boundary IS NULL`).Scan(&n); err != nil {
		t.Fatalf("counting unclassified edges: %v", err)
	}
	return n
}
