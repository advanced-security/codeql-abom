package store

import "testing"

// An estimate follows from the work the crawl will do, and the crawl claims, probes and polls
// inside the owned closure only. Counting what is held outside it promises a queue nobody works.
func TestTheWorkBehindThePoolsIsCountedInsideTheBound(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "in/moving", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1"},
		pollRow{key: "in/empty", ref: "", status: "nothing", resolved: pollSHA, analyzed: "HEAD"},
		pollRow{key: "in/queued", ref: "v1", status: "pending"},
		pollRow{key: "out/moving", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1"},
		pollRow{key: "out/queued", ref: "v1", status: "pending"},
	)
	bound := BoundTo([]string{"In/Moving", "in/empty", "in/queued"})

	all, err := s.ReadFreshnessWork(QueueBound{})
	if err != nil {
		t.Fatal(err)
	}
	if all.MovingReferences != 3 || all.QueuedReferences != 2 {
		t.Errorf("unbounded = %+v, want 3 the sweep probes, nothing to analyze among them, and 2 queued", all)
	}
	inside, err := s.ReadFreshnessWork(bound)
	if err != nil {
		t.Fatal(err)
	}
	if inside.MovingReferences != 2 || inside.QueuedReferences != 1 {
		t.Errorf("bounded = %+v, want 2 moving and 1 queued inside the closure", inside)
	}

	polled, err := s.ReadPollWork(bound)
	if err != nil {
		t.Fatal(err)
	}
	if polled.References != 1 {
		t.Errorf("poll work = %+v, want in/moving alone", polled)
	}
	if none, err := s.ReadFreshnessWork(BoundTo(nil)); err != nil || none.QueuedReferences != 0 {
		t.Errorf("an empty closure = %+v, %v; want nothing queued inside it", none, err)
	}
}
