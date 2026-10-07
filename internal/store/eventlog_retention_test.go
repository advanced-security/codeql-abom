package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// retainedAt is the moment these tests prune at; the cutoff is what HistoryRetention keeps behind it.
var retainedAt = time.Date(2026, 10, 5, 16, 0, 0, 0, time.UTC)

func setReceivedAt(t *testing.T, s *Store, deliveryID string, at time.Time) {
	t.Helper()
	if _, err := s.DB().Exec(`UPDATE abom_event_delivery SET received_at = ? WHERE delivery_id = ?`,
		at.UTC().Format(time.RFC3339Nano), deliveryID); err != nil {
		t.Fatalf("dating delivery %s: %v", deliveryID, err)
	}
}

func settleDeliveryAs(t *testing.T, s *Store, deliveryID, status string) {
	t.Helper()
	d, found, err := s.Delivery(deliveryID)
	if err != nil || !found {
		t.Fatalf("Delivery(%s) = found %v, %v", deliveryID, found, err)
	}
	if settled, err := s.SettleDelivery(d.ID, status, "a reason", "a detail"); err != nil || !settled {
		t.Fatalf("SettleDelivery(%s, %s) = %v, %v", deliveryID, status, settled, err)
	}
}

func pruneBefore(t *testing.T, s *Store, before time.Time) HistoryPruned {
	t.Helper()
	pruned, err := s.PruneHistory(before)
	if err != nil {
		t.Fatalf("PruneHistory(%v): %v", before, err)
	}
	return pruned
}

func keptDeliveryIDs(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT delivery_id FROM abom_event_delivery ORDER BY row_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// A pending delivery has not been processed; pruning it would drop a change nobody acted on.
func TestAPendingDeliveryIsNeverPrunedHoweverOld(t *testing.T) {
	s := newTestStore(t)
	appendDelivery(t, s, "waiting", "push", `{}`)
	appendDelivery(t, s, "deferred", "push", `{}`)
	deferred, _, _ := s.Delivery("deferred")
	if err := s.DeferDelivery(deferred.ID, "analysis running", "acme/service is being analyzed"); err != nil {
		t.Fatal(err)
	}
	setReceivedAt(t, s, "waiting", retainedAt.AddDate(-5, 0, 0))
	setReceivedAt(t, s, "deferred", retainedAt.AddDate(-1, 0, 0))

	if pruned := pruneBefore(t, s, retainedAt.Add(-HistoryRetention)); pruned.Deliveries != 0 {
		t.Errorf("pruned %d deliveries, want none: both are still pending", pruned.Deliveries)
	}
	if got := keptDeliveryIDs(t, s); !reflect.DeepEqual(got, []string{"waiting", "deferred"}) {
		t.Errorf("kept %v, want both pending deliveries", got)
	}
}

// Retention is a cutoff in time: everything settled before it goes, and nothing at or after it.
func TestSettledDeliveriesBeforeTheCutoffArePrunedAndTheRestKept(t *testing.T) {
	s := newTestStore(t)
	cutoff := retainedAt.Add(-HistoryRetention)
	for _, d := range []struct {
		id, status string
		at         time.Time
	}{
		{"old-done", EventDone, cutoff.Add(-time.Hour)},
		{"old-ignored", EventIgnored, cutoff.Add(-24 * time.Hour)},
		{"old-failed", EventFailed, cutoff.Add(-time.Second)},
		{"old-pending", EventPending, cutoff.Add(-48 * time.Hour)},
		{"at-cutoff", EventDone, cutoff},
		// As text this sorts before the cutoff, though it arrived half a second after it.
		{"just-after", EventIgnored, cutoff.Add(500 * time.Millisecond)},
		{"recent", EventFailed, retainedAt.Add(-time.Hour)},
	} {
		appendDelivery(t, s, d.id, "push", `{}`)
		if d.status != EventPending {
			settleDeliveryAs(t, s, d.id, d.status)
		}
		setReceivedAt(t, s, d.id, d.at)
	}

	pruned := pruneBefore(t, s, cutoff)
	if pruned.Deliveries != 3 {
		t.Errorf("pruned %d deliveries, want the 3 settled before the cutoff", pruned.Deliveries)
	}
	want := []string{"old-pending", "at-cutoff", "just-after", "recent"}
	if got := keptDeliveryIDs(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("kept %v, want %v", got, want)
	}
	if again := pruneBefore(t, s, cutoff); again != (HistoryPruned{}) {
		t.Errorf("a second prune at the same cutoff removed %+v, want nothing", again)
	}
}

// A meter's total is "since" its oldest minute, so pruning must move that forward with what it removes.
func TestPruningMeterMinutesMovesEachMetersSinceForward(t *testing.T) {
	s := newTestStore(t)
	cutoff := retainedAt.Add(-HistoryRetention)
	const pool, unit = "enumeration", "REST requests"
	if err := s.AddPoolSpend([]PoolSpend{
		{Pool: pool, Unit: unit, Bucket: cutoff.Add(-2 * time.Hour), Requests: 10, Charged: 10},
		{Pool: pool, Unit: unit, Bucket: cutoff.Add(-time.Minute), Requests: 5, Charged: 4},
		{Pool: pool, Unit: unit, Bucket: cutoff, Requests: 3, Charged: 3},
		{Pool: pool, Unit: unit, Bucket: retainedAt.Add(-time.Minute), Requests: 1, Charged: 1},
		{Pool: "advisories", Unit: unit, Bucket: cutoff.Add(-time.Hour), Requests: 2, Charged: 2},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := s.PoolMeters(retainedAt.Add(-time.Hour))
	if err != nil || len(before) != 2 {
		t.Fatalf("PoolMeters before pruning = %+v, %v; want advisories and enumeration", before, err)
	}

	if pruned := pruneBefore(t, s, cutoff); pruned.Buckets != 3 {
		t.Errorf("pruned %d meter minutes, want the 3 before the cutoff", pruned.Buckets)
	}
	after, err := s.PoolMeters(retainedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Pool != pool {
		t.Fatalf("meters after pruning = %+v, want only enumeration: advisories kept no minute", after)
	}
	if m := after[0]; !m.Since.Equal(cutoff) || m.Total.Requests != 4 || m.Total.Charged != 4 {
		t.Errorf("enumeration reads %d requests and %d charged since %v, want 4 and 4 since %v",
			m.Total.Requests, m.Total.Charged, m.Since, cutoff)
	}
}

// Check 5 samples recent runs; one recorded before the cutoff has no recent log to compare with.
func TestPruningRemovesWorkflowRunsRecordedBeforeTheCutoff(t *testing.T) {
	s := newTestStore(t)
	cutoff := retainedAt.Add(-HistoryRetention)
	for id, at := range map[int64]time.Time{1: cutoff.Add(-time.Hour), 2: cutoff, 3: retainedAt} {
		recordCompletedRun(t, s, WorkflowRun{Repository: "acme/service", RunID: id, CompletedAt: at})
		setRecordedAt(t, s, id, at)
	}

	if pruned := pruneBefore(t, s, cutoff); pruned.Runs != 1 {
		t.Errorf("pruned %d runs, want the one recorded before the cutoff", pruned.Runs)
	}
	if got := runIDs(recentRunsOf(t, s, 10)); !reflect.DeepEqual(got, []int64{3, 2}) {
		t.Errorf("kept runs %v, want [3 2]", got)
	}
}

// Counts are read together, so a prune that fails partway must leave all three tables as they were.
func TestAPruneThatFailsRemovesNothing(t *testing.T) {
	s := newTestStore(t)
	cutoff := retainedAt.Add(-HistoryRetention)
	appendDelivery(t, s, "old", "push", `{}`)
	settleDeliveryAs(t, s, "old", EventDone)
	setReceivedAt(t, s, "old", cutoff.Add(-time.Hour))
	if err := s.AddPoolSpend([]PoolSpend{{Pool: "fetching", Unit: "git requests",
		Bucket: cutoff.Add(-time.Hour), Requests: 1, Charged: 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`DROP TABLE abom_workflow_run`); err != nil {
		t.Fatal(err)
	}

	if pruned, err := s.PruneHistory(cutoff); err == nil {
		t.Fatalf("pruning without the run table = %+v, want an error", pruned)
	}
	if got := keptDeliveryIDs(t, s); !reflect.DeepEqual(got, []string{"old"}) {
		t.Errorf("kept %v after a failed prune, want the delivery back", got)
	}
	if meters, err := s.PoolMeters(cutoff); err != nil || len(meters) != 1 {
		t.Errorf("meters after a failed prune = %+v, %v; want the minute back", meters, err)
	}
}

// Counts cover only the deliveries kept, so the log says since when, or a total reads as all-time.
func TestTheEventLogSaysSinceWhenItsCountsRun(t *testing.T) {
	s := newTestStore(t)
	cutoff := retainedAt.Add(-HistoryRetention)

	empty, err := s.EventLog(10)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Since != nil {
		t.Errorf("an empty log is counted since %v, want no since", empty.Since)
	}
	if body, _ := json.Marshal(empty); strings.Contains(string(body), `"since"`) {
		t.Errorf("an empty log marshals as %s, want since absent", body)
	}

	appendDelivery(t, s, "old", "push", `{}`)
	appendDelivery(t, s, "new", "push", `{}`)
	settleDeliveryAs(t, s, "old", EventDone)
	setReceivedAt(t, s, "old", cutoff.Add(-time.Hour))
	setReceivedAt(t, s, "new", retainedAt.Add(-time.Hour))

	log, err := s.EventLog(10)
	if err != nil {
		t.Fatal(err)
	}
	if log.Received != 2 || log.Since == nil || !log.Since.Equal(cutoff.Add(-time.Hour)) {
		t.Errorf("received %d since %v, want 2 since %v", log.Received, log.Since, cutoff.Add(-time.Hour))
	}

	pruneBefore(t, s, cutoff)
	log, err = s.EventLog(10)
	if err != nil {
		t.Fatal(err)
	}
	if log.Received != 1 || log.Since == nil || !log.Since.Equal(retainedAt.Add(-time.Hour)) {
		t.Errorf("after pruning, received %d since %v, want 1 since %v", log.Received, log.Since,
			retainedAt.Add(-time.Hour))
	}
}
