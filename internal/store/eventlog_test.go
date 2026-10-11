package store

import (
	"bytes"
	"path/filepath"
	"testing"
)

func appendDelivery(t *testing.T, s *Store, id, event, payload string) bool {
	t.Helper()
	fresh, err := s.AppendDelivery(EventDelivery{DeliveryID: id, Event: event, Payload: []byte(payload)})
	if err != nil {
		t.Fatalf("AppendDelivery(%s): %v", id, err)
	}
	return fresh
}

func pendingDeliveries(t *testing.T, s *Store, want int) []EventDelivery {
	t.Helper()
	pending, err := s.PendingDeliveries(0, 100)
	if err != nil || len(pending) != want {
		t.Fatalf("PendingDeliveries = %d, %v; want %d", len(pending), err, want)
	}
	return pending
}

// GitHub redelivers on request; acting on or counting both would report a push that happened once twice.
func TestARedeliveredDeliveryIsRecordedOnce(t *testing.T) {
	s := newTestStore(t)

	if !appendDelivery(t, s, "d-1", "push", `{"first":true}`) {
		t.Fatal("the first delivery was reported as a repeat")
	}
	if appendDelivery(t, s, "d-1", "push", `{"second":true}`) {
		t.Error("a redelivery was reported as new")
	}

	got, found, err := s.Delivery("d-1")
	if err != nil || !found {
		t.Fatalf("Delivery(d-1) = found %v, %v", found, err)
	}
	if !bytes.Equal(got.Payload, []byte(`{"first":true}`)) {
		t.Errorf("payload = %s, want the one first recorded", got.Payload)
	}
	log, err := s.EventLog(10)
	if err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	if log.Received != 1 {
		t.Errorf("Received = %d, want 1", log.Received)
	}
}

// The order deliveries committed is the order they are processed in, whatever their ids say.
func TestPendingDeliveriesComeBackInTheOrderTheyWereRecorded(t *testing.T) {
	s := newTestStore(t)
	for _, id := range []string{"zz", "aa", "mm"} {
		appendDelivery(t, s, id, "push", `{}`)
	}

	pending, err := s.PendingDeliveries(0, 10)
	if err != nil {
		t.Fatalf("PendingDeliveries: %v", err)
	}
	var ids []string
	for _, d := range pending {
		ids = append(ids, d.DeliveryID)
		if d.Status != EventPending || d.ReceivedAt == "" || string(d.Payload) != `{}` {
			t.Errorf("%s came back as %+v", d.DeliveryID, d)
		}
	}
	if len(ids) != 3 || ids[0] != "zz" || ids[1] != "aa" || ids[2] != "mm" {
		t.Fatalf("pending order = %v, want [zz aa mm]", ids)
	}

	after, err := s.PendingDeliveries(pending[0].ID, 10)
	if err != nil {
		t.Fatalf("PendingDeliveries(after): %v", err)
	}
	if len(after) != 2 || after[0].DeliveryID != "aa" {
		t.Errorf("after the first = %+v, want aa then mm", after)
	}
}

// A second settlement would mean a delivery was acted on twice, with only the last one recorded.
func TestADeliveryIsSettledExactlyOnce(t *testing.T) {
	s := newTestStore(t)
	appendDelivery(t, s, "d-1", "push", `{}`)
	pending := pendingDeliveries(t, s, 1)

	settled, err := s.SettleDelivery(pending[0].ID, EventDone, "unchanged", "probe agreed")
	if err != nil || !settled {
		t.Fatalf("first SettleDelivery = %v, %v", settled, err)
	}
	again, err := s.SettleDelivery(pending[0].ID, EventIgnored, "not owned", "second opinion")
	if err != nil {
		t.Fatalf("second SettleDelivery: %v", err)
	}
	if again {
		t.Error("a settled delivery was settled again")
	}

	got, _, _ := s.Delivery("d-1")
	if got.Status != EventDone || got.Reason != "unchanged" || got.ProcessedAt == "" {
		t.Errorf("delivery = %+v, want the first outcome kept", got)
	}
	if rest, _ := s.PendingDeliveries(0, 10); len(rest) != 0 {
		t.Errorf("still pending: %+v", rest)
	}
}

// Waiting is not an outcome: a deferred delivery stays pending, says why, and comes back.
func TestADeferredDeliveryStaysPendingAndSaysWhy(t *testing.T) {
	s := newTestStore(t)
	appendDelivery(t, s, "d-1", "push", `{}`)
	pending := pendingDeliveries(t, s, 1)

	if err := s.DeferDelivery(pending[0].ID, "analysis running", "acme/service is being analyzed"); err != nil {
		t.Fatalf("DeferDelivery: %v", err)
	}
	got, _, _ := s.Delivery("d-1")
	if got.Status != EventPending || got.Reason != "analysis running" || got.Attempts != 1 ||
		got.ProcessedAt != "" {
		t.Errorf("delivery = %+v, want pending, attempted once, with the reason", got)
	}
	if again, _ := s.PendingDeliveries(0, 10); len(again) != 1 {
		t.Errorf("pending = %+v, want the deferred delivery back", again)
	}
}

// Every state is present so a reader can say "none", and each outcome carries its reason.
func TestTheEventLogCountsEachOutcomeWithItsReason(t *testing.T) {
	s := newTestStore(t)

	empty, err := s.EventLog(10)
	if err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	if empty.Received != 0 || empty.Last != nil || empty.Outcomes == nil || empty.Failures == nil {
		t.Errorf("empty log = %+v, want nothing received, no last delivery and empty lists", empty)
	}
	for _, status := range []string{EventPending, EventDone, EventIgnored, EventFailed} {
		if n, ok := empty.Statuses[status]; !ok || n != 0 {
			t.Errorf("empty log Statuses[%s] = %d (present %v), want 0 and present", status, n, ok)
		}
	}

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		appendDelivery(t, s, id, "push", `{}`)
	}
	pending := pendingDeliveries(t, s, 5)
	settle := func(i int, status, reason string) {
		if _, err := s.SettleDelivery(pending[i].ID, status, reason, reason+" detail"); err != nil {
			t.Fatalf("SettleDelivery: %v", err)
		}
	}
	settle(0, EventIgnored, "not owned")
	settle(1, EventIgnored, "not owned")
	settle(2, EventFailed, "probe failed")
	settle(3, EventDone, "unchanged")

	log, err := s.EventLog(10)
	if err != nil {
		t.Fatalf("EventLog: %v", err)
	}
	if log.Received != 5 {
		t.Errorf("Received = %d, want 5", log.Received)
	}
	if log.Last == nil || log.Last.DeliveryID != "e" {
		t.Errorf("Last = %+v, want e", log.Last)
	}
	want := map[string]int{EventPending: 1, EventDone: 1, EventIgnored: 2, EventFailed: 1}
	for status, n := range want {
		if log.Statuses[status] != n {
			t.Errorf("Statuses[%s] = %d, want %d", status, log.Statuses[status], n)
		}
	}
	found := false
	for _, o := range log.Outcomes {
		if o.Status == EventIgnored && o.Reason == "not owned" {
			found = o.Count == 2
		}
	}
	if !found {
		t.Errorf("Outcomes = %+v, want ignored/not owned counted twice", log.Outcomes)
	}
	if len(log.Failures) != 1 || log.Failures[0].DeliveryID != "c" ||
		log.Failures[0].Detail != "probe failed detail" {
		t.Errorf("Failures = %+v, want c with its detail", log.Failures)
	}
}

// A push is judged by what the queue holds, so the row is read as recorded, expedite mark included.
func TestAQueueRowIsReadAsRecorded(t *testing.T) {
	s := newTestStore(t)
	if _, found, err := s.QueueRow("acme/service", ""); err != nil || found {
		t.Fatalf("QueueRow on an empty queue = found %v, %v", found, err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
		                           resolved_sha, reason, checked_at, expedited_at)
		VALUES ('acme/service', 'release', 'acme', 'service', 0, 'pending', '2026-01-01T00:00:00Z',
		        'abc', 'ref moved to def', '2026-01-02T00:00:00Z', '2026-01-03T00:00:00Z')`); err != nil {
		t.Fatalf("seeding a row: %v", err)
	}

	row, found, err := s.QueueRow("acme/service", "release")
	if err != nil || !found {
		t.Fatalf("QueueRow = found %v, %v", found, err)
	}
	want := QueueRow{NodeKey: "acme/service", Ref: "release", Owner: "acme", Repo: "service",
		Status: "pending", Depth: 0, ResolvedSHA: "abc", Reason: "ref moved to def",
		CheckedAt: "2026-01-02T00:00:00Z", Expedited: true}
	if row != want {
		t.Errorf("QueueRow = %+v, want %+v", row, want)
	}
	if _, found, _ := s.QueueRow("acme/service", ""); found {
		t.Error("the default branch's row was reported for a row only release has")
	}
}

// Every other write shares this one connection, so the append's FULL sync must not outlive it.
func TestSQLiteAppendingADeliveryLeavesTheConnectionAtItsUsualSync(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "abom.db"), Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	appendDelivery(t, s, "d-1", "push", `{}`)

	var mode int
	if err := s.DB().QueryRow(`PRAGMA synchronous`).Scan(&mode); err != nil {
		t.Fatalf("reading synchronous: %v", err)
	}
	if mode != 1 {
		t.Errorf("synchronous = %d after an append, want 1 (NORMAL)", mode)
	}
}
