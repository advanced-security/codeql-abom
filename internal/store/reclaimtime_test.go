package store

import (
	"testing"
	"time"
)

func TestReclaimComparesInstantsRatherThanTrimmedTimestampStrings(t *testing.T) {
	s := newTestStore(t)
	instant := time.Date(2026, 10, 9, 0, 0, 0, 120000000, time.UTC)
	q := s.Frontier(FrontierOptions{Now: func() time.Time { return instant }})
	if _, err := q.Enqueue([]Item{{NodeKey: "vendor/action", Owner: "vendor", Repo: "action", Depth: 1}}); err != nil {
		t.Fatal(err)
	}
	if items, err := q.Claim(1); err != nil || len(items) != 1 {
		t.Fatalf("claim: %+v %v", items, err)
	}
	instant = instant.Add(3400 * time.Microsecond)
	if n, err := q.ReclaimStale(0); err != nil || n != 1 {
		t.Fatalf("the older claim was not reclaimed: %d %v", n, err)
	}
}
