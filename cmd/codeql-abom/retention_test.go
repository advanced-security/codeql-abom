package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// syncBuffer is a log the loop writes while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if text := strings.TrimSuffix(b.buf.String(), "\n"); text != "" {
		return strings.Split(text, "\n")
	}
	return []string{}
}

// fakeClock tells the loop the time the test sets and counts how often it was asked: once per prune.
type fakeClock struct {
	mu    sync.Mutex
	at    time.Time
	asked int
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked++
	return c.at
}

func (c *fakeClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = at
}

func (c *fakeClock) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.asked
}

// waitFor polls until cond holds; the bound only stops a broken loop hanging the suite.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for give := time.Now().Add(10 * time.Second); !cond(); time.Sleep(time.Millisecond) {
		if time.Now().After(give) {
			t.Fatalf("still waiting for %s", what)
		}
	}
}

// startRetention runs the loop until the test ends, and fails the test if it outlives its context.
func startRetention(t *testing.T, s *store.Store, every time.Duration, clock *fakeClock) (*syncBuffer, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	log := &syncBuffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		pruneHistory(ctx, s, every, clock.now, log)
	}()
	stop := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the retention loop did not stop when its context ended")
		}
	}
	t.Cleanup(cancel)
	return log, stop
}

func seedSettledDelivery(t *testing.T, s *store.Store, id, status string, received time.Time) {
	t.Helper()
	if _, err := s.AppendDelivery(store.EventDelivery{DeliveryID: id, Event: "push", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if status != store.EventPending {
		d, _, err := s.Delivery(id)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.SettleDelivery(d.ID, status, "a reason", "a detail"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DB().Exec(`UPDATE abom_event_delivery SET received_at = ? WHERE delivery_id = ?`,
		received.UTC().Format(time.RFC3339Nano), id); err != nil {
		t.Fatal(err)
	}
}

func seedRunRecordedAt(t *testing.T, s *store.Store, id int64, recorded time.Time) {
	t.Helper()
	if _, err := s.RecordWorkflowRun(store.WorkflowRun{Repository: "acme/service", RunID: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE abom_workflow_run SET recorded_at = ? WHERE run_id = ?`,
		recorded.UTC().Format(time.RFC3339), id); err != nil {
		t.Fatal(err)
	}
}

// Serve may run for weeks; the first prune cannot wait a period, or a restart loop would never prune.
func TestRetentionPrunesOnceAtStartWithoutWaitingAPeriod(t *testing.T) {
	s := newTestStore(t)
	clock := &fakeClock{at: time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)}
	seedSettledDelivery(t, s, "old", store.EventDone, clock.at.Add(-store.HistoryRetention-time.Hour))

	log, stop := startRetention(t, s, time.Hour, clock)
	waitFor(t, "the prune at start", func() bool { return len(log.lines()) == 1 })
	stop()

	if clock.calls() != 1 {
		t.Errorf("pruned %d times inside an hour's period, want once at start", clock.calls())
	}
}

// One line per prune that removed something, and none for one that did not, or the log is all retention.
func TestRetentionSaysWhatEachPeriodRemovedAndNothingWhenItRemovedNothing(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: start}
	cutoff := start.Add(-store.HistoryRetention)
	seedSettledDelivery(t, s, "old-1", store.EventDone, cutoff.Add(-time.Hour))
	seedSettledDelivery(t, s, "old-2", store.EventFailed, cutoff.Add(-2*time.Hour))
	seedSettledDelivery(t, s, "waiting", store.EventPending, cutoff.Add(-3*time.Hour))
	seedSettledDelivery(t, s, "recent", store.EventIgnored, start.Add(-time.Hour))
	if err := s.AddPoolSpend([]store.PoolSpend{
		{Pool: "fetching", Unit: "git requests", Bucket: cutoff.Add(-time.Minute), Requests: 1, Charged: 1},
		{Pool: "fetching", Unit: "git requests", Bucket: start.Add(-2 * time.Minute), Requests: 1, Charged: 1},
		{Pool: "fetching", Unit: "git requests", Bucket: start.Add(-time.Minute), Requests: 1, Charged: 1},
	}); err != nil {
		t.Fatal(err)
	}
	seedRunRecordedAt(t, s, 1, cutoff.Add(-time.Hour))
	seedRunRecordedAt(t, s, 2, cutoff.Add(-2*time.Hour))
	seedRunRecordedAt(t, s, 3, start)

	log, stop := startRetention(t, s, time.Millisecond, clock)
	waitFor(t, "the first prune", func() bool { return len(log.lines()) >= 1 })
	asked := clock.calls()
	waitFor(t, "three more periods", func() bool { return clock.calls() >= asked+3 })
	if got := log.lines(); len(got) != 1 {
		t.Fatalf("logged %q, want one line: the periods after it removed nothing", got)
	}

	clock.set(start.AddDate(0, 0, 31))
	waitFor(t, "the prune a month on", func() bool { return len(log.lines()) >= 2 })
	stop()

	want := []string{
		"2026-10-05T16:00:00Z serve: kept 30 days of history: removed 2 deliveries, 1 meter minute and " +
			"2 workflow runs older than 2026-09-05T16:00:00Z",
		"2026-11-05T16:00:00Z serve: kept 30 days of history: removed 1 delivery, 2 meter minutes and " +
			"1 workflow run older than 2026-10-06T16:00:00Z",
	}
	got := log.lines()
	if len(got) != len(want) {
		t.Fatalf("logged %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d reads\n %s\nwant\n %s", i+1, got[i], want[i])
		}
	}
	if d, found, err := s.Delivery("waiting"); err != nil || !found || d.Status != store.EventPending {
		t.Errorf("the pending delivery reads %+v (found %v, %v), want it kept however old", d, found, err)
	}
}

// A prune that failed is said, and the next period tries again rather than the loop giving up.
func TestAFailedPruneIsLoggedAndTheLoopCarriesOn(t *testing.T) {
	s := newTestStore(t)
	start := time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)
	clock := &fakeClock{at: start}
	seedSettledDelivery(t, s, "old", store.EventDone, start.Add(-store.HistoryRetention-time.Hour))
	if _, err := s.DB().Exec(`DROP TABLE abom_workflow_run`); err != nil {
		t.Fatal(err)
	}

	log, stop := startRetention(t, s, time.Millisecond, clock)
	waitFor(t, "the failure to be said", func() bool { return len(log.lines()) >= 1 })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	removed := "2026-10-05T16:00:00Z serve: kept 30 days of history: removed 1 delivery, 0 meter minutes and " +
		"0 workflow runs older than 2026-09-05T16:00:00Z"
	waitFor(t, "a prune after the failure", func() bool {
		got := log.lines()
		return len(got) > 0 && got[len(got)-1] == removed
	})
	stop()

	got := log.lines()
	failed := got[:len(got)-1]
	for _, line := range failed {
		if !strings.HasPrefix(line, "2026-10-05T16:00:00Z serve: keeping 30 days of history: ") ||
			!strings.Contains(line, "abom_workflow_run") {
			t.Errorf("a failed prune reads %q, want the time, what it was keeping and why it failed", line)
		}
	}
	if len(failed) == 0 {
		t.Errorf("logged %q, want the failure before the prune that succeeded", got)
	}
}
