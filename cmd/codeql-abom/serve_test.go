package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
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

// A crawl that was running is remembered, and a restart does not act on the memory unless the
// operator asked it to. Resuming on its own spends rate limit, disk and every core on a decision
// nobody took at that moment.
func TestARunningCrawlIsNotResumedUnlessResumingWasConfigured(t *testing.T) {
	s := openStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if shouldResume(s, false, &out, &out) {
		t.Error("discovery resumed without anybody configuring it to")
	}
	if !strings.Contains(out.String(), "left paused") {
		t.Errorf("a crawl that stopped resuming says nothing about it: %q", out.String())
	}
}

// Configured, it resumes — otherwise the preference is a control that does nothing.
func TestAConfiguredCrawlResumes(t *testing.T) {
	s := openStore(t)
	if err := s.SetDiscoveryIntent(true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDiscoveryResumeOnStart(true); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if !shouldResume(s, false, &out, &out) {
		t.Error("resume-on-start is set and the crawl did not resume")
	}
}

// The preference is about resuming, never about starting: an installation that was paused, or
// that nobody ever started, stays stopped however the preference is set.
func TestResumeOnStartDoesNotStartACrawlNobodyStarted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent func(*store.Store) error
	}{
		{"never started", func(*store.Store) error { return nil }},
		{"paused", func(s *store.Store) error { return s.SetDiscoveryIntent(false) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openStore(t)
			if err := tc.intent(s); err != nil {
				t.Fatal(err)
			}
			if err := s.SetDiscoveryResumeOnStart(true); err != nil {
				t.Fatal(err)
			}

			var out bytes.Buffer
			if shouldResume(s, false, &out, &out) {
				t.Error("a crawl started that the operator had not started")
			}
		})
	}
}

// `--expand` is somebody asking on this invocation, which is explicit enough on its own.
func TestExpandStartsDiscoveryWhateverIsRecorded(t *testing.T) {
	s := openStore(t)

	var out bytes.Buffer
	if !shouldResume(s, true, &out, &out) {
		t.Error("--expand did not start discovery")
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	return s
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
