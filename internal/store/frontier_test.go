package store

import (
	"testing"
)

func queueRow(t *testing.T, s *Store, key, status string, attempts int) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, depth, status, attempts, score, enqueued_at)
		VALUES (?, 'octo', ?, 1, ?, ?, 1, '2026-01-01T00:00:00Z')`,
		key, key, status, attempts); err != nil {
		t.Fatalf("inserting %s: %v", key, err)
	}
}

func externalNode(t *testing.T, s *Store, key, kind string) {
	t.Helper()
	if _, err := s.DB().Exec(
		`INSERT INTO abom_node (node_key, node_kind, is_external) VALUES (?, ?, 1)`,
		key, kind); err != nil {
		t.Fatalf("inserting node %s: %v", key, err)
	}
}

// A dependency named only by an upstream project's own CI is not in this organization's supply
// chain, and expansion is right to skip it. But it is still drawn in the Explorer, so a drained
// queue that says nothing about it reads as "everything has been analyzed" when it has not.
func TestTheQueueCountsDependenciesItWillNeverAnalyze(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "actions/checkout", "done", 0)
	externalNode(t, s, "actions/checkout", "action")
	externalNode(t, s, "actions-r-us/actions-tagger", "action")
	externalNode(t, s, "actions/reusable-workflows/.github/workflows/licensed.yml", "reusable_workflow")
	// Nothing can be cloned for an image, so it is out of scope for a different reason and must
	// not be folded into this count.
	externalNode(t, s, "docker.io/library/node", "docker_image")

	counts, err := s.FrontierCounts()
	if err != nil {
		t.Fatalf("FrontierCounts: %v", err)
	}
	if counts[FrontierUpstreamOnly] != 2 {
		t.Errorf("counts[%s] = %d, want the two dependencies no seed repository names",
			FrontierUpstreamOnly, counts[FrontierUpstreamOnly])
	}
}

// A dependency that has already failed twice is not in the same position as one nobody has
// touched, and showing them under one word hides every retry the crawler is spending.
func TestTheQueueSeparatesUntriedWorkFromRetries(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "fresh", "pending", 0)
	queueRow(t, s, "failed-once", "pending", 2)

	queued, err := s.FrontierSnapshot(FrontierQueued, 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot(queued): %v", err)
	}
	if len(queued) != 1 || queued[0].NodeKey != "fresh" {
		t.Errorf("queued = %+v, want only the dependency nothing has been spent on", queued)
	}

	retrying, err := s.FrontierSnapshot(FrontierRetrying, 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot(retrying): %v", err)
	}
	if len(retrying) != 1 || retrying[0].NodeKey != "failed-once" {
		t.Errorf("retrying = %+v, want only the dependency that has already failed", retrying)
	}
}

// An empty result and an absent one look identical on screen. Naming every state means a view
// can say "none" instead of leaving a gap the reader has to interpret.
func TestFrontierCountsNameEveryState(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "fresh", "pending", 0)

	counts, err := s.FrontierCounts()
	if err != nil {
		t.Fatalf("FrontierCounts: %v", err)
	}
	for _, state := range []string{FrontierQueued, FrontierRetrying, FrontierWaiting, "running", "done",
		"unavailable"} {
		if _, ok := counts[state]; !ok {
			t.Errorf("counts omit %q, so a view cannot tell none from unknown", state)
		}
	}
	if counts[FrontierQueued] != 1 || counts[FrontierRetrying] != 0 {
		t.Errorf("counts = %v, want one queued and no retries", counts)
	}
}

// The Discovery view dates work in flight so a worker stuck on one dependency is visible, and
// says when a failed one comes back, so "retrying" is a time rather than a mood.
func TestTheQueueCarriesTheTimesAViewNeeds(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "running-now", "running", 0)
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET claimed_at='2026-01-01T01:00:00Z' WHERE node_key='running-now'`,
	); err != nil {
		t.Fatalf("recording a claim: %v", err)
	}

	items, err := s.FrontierSnapshot("running", 10)
	if err != nil {
		t.Fatalf("FrontierSnapshot: %v", err)
	}
	if len(items) != 1 || items[0].ClaimedAt == "" {
		t.Errorf("items = %+v, want the claim time available to the view", items)
	}
}

// Being named by an advisory cuts across the queue states rather than being one of them, so it
// must not displace a dependency's real state: the reader still needs to know whether anything
// has looked inside it. Filtering is all this does — deciding whether the observed reference
// actually falls in the vulnerable range is the advisory layer's job, not the queue's.
func TestTheQueueCanSelectDependenciesNamedByAnAdvisory(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "codeql-action", "done", 0)
	queueRow(t, s, "unheard-of", "done", 0)
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET owner='github', repo='codeql-action' WHERE node_key='codeql-action'`,
	); err != nil {
		t.Fatalf("naming the dependency: %v", err)
	}
	if _, err := s.AddAdvisories([]Advisory{sampleAdvisory()}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	named, err := s.FrontierSnapshot(FrontierAdvisories, 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot(advisories): %v", err)
	}
	if len(named) != 1 || named[0].NodeKey != "codeql-action" {
		t.Fatalf("named = %+v, want only the dependency an advisory names", named)
	}
	if named[0].Status != "done" {
		t.Errorf("Status = %q, want the queue state kept rather than replaced", named[0].Status)
	}

	counts, err := s.FrontierCounts()
	if err != nil {
		t.Fatalf("FrontierCounts: %v", err)
	}
	if counts[FrontierAdvisories] != 1 {
		t.Errorf("counts[%s] = %d, want 1", FrontierAdvisories, counts[FrontierAdvisories])
	}
	if counts["done"] != 2 {
		t.Errorf("counts[done] = %d, want both, since an advisory is not a state", counts["done"])
	}
}

// A withdrawn advisory was retracted by the people who published it, so continuing to flag what
// it named would be this tool asserting a verdict its source has taken back.
func TestAWithdrawnAdvisoryStopsFlaggingItsDependency(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "codeql-action", "done", 0)
	if _, err := s.DB().Exec(
		`UPDATE abom_frontier SET owner='github', repo='codeql-action' WHERE node_key='codeql-action'`,
	); err != nil {
		t.Fatalf("naming the dependency: %v", err)
	}
	withdrawn := sampleAdvisory()
	withdrawn.WithdrawnAt = "2025-03-01T00:00:00Z"
	if _, err := s.AddAdvisories([]Advisory{withdrawn}); err != nil {
		t.Fatalf("AddAdvisories: %v", err)
	}

	named, err := s.FrontierSnapshot(FrontierAdvisories, 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot(advisories): %v", err)
	}
	if len(named) != 0 {
		t.Errorf("named = %+v, want nothing flagged by a retracted advisory", named)
	}
}

// A row held for code scanning is queued and will not be claimed until code scanning reports, or
// its ceiling passes; counted as queued it reads as work about to run, and as a retry it reads as
// a failure. It is neither, whatever it failed before.
func TestTheQueueSeparatesWorkHeldForCodeScanning(t *testing.T) {
	s := newTestStore(t)
	queueRow(t, s, "fresh", "pending", 0)
	queueRow(t, s, "held", "pending", 0)
	queueRow(t, s, "held-after-a-failure", "pending", 2)
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET awaiting_code_scanning = 'c2',
		next_attempt_at = '2026-10-07T12:10:00Z' WHERE node_key LIKE 'held%'`); err != nil {
		t.Fatal(err)
	}

	counts, err := s.FrontierCounts()
	if err != nil {
		t.Fatalf("FrontierCounts: %v", err)
	}
	if counts[FrontierQueued] != 1 || counts[FrontierRetrying] != 0 || counts[FrontierWaiting] != 2 {
		t.Errorf("counts = %v, want one queued, none retrying and two waiting", counts)
	}
	waiting, err := s.FrontierSnapshot(FrontierWaiting, 100)
	if err != nil {
		t.Fatalf("FrontierSnapshot(waiting): %v", err)
	}
	if len(waiting) != 2 || waiting[0].AwaitingCodeScanning != "c2" || waiting[0].NextAttemptAt == "" {
		t.Errorf("waiting = %+v, want both held rows with the commit and the ceiling they wait on", waiting)
	}
	for _, state := range []string{FrontierQueued, FrontierRetrying} {
		items, err := s.FrontierSnapshot(state, 100)
		if err != nil {
			t.Fatalf("FrontierSnapshot(%s): %v", state, err)
		}
		for _, it := range items {
			if it.AwaitingCodeScanning != "" {
				t.Errorf("%s lists %s, which is held for code scanning", state, it.NodeKey)
			}
		}
	}
}

// Queuing a dependency again adds no work, and a claimed one is not handed out a second time while
// it is being analyzed.
func TestAQueuedDependencyIsClaimedOnceAndSettles(t *testing.T) {
	q := newTestStore(t).Frontier(FrontierOptions{})
	item := Item{NodeKey: "acme/action", Owner: "acme", Repo: "action", Ref: "v1", Depth: 1}
	for _, want := range []int{1, 0} {
		if n, err := q.Enqueue([]Item{item}); err != nil || n != want {
			t.Fatalf("enqueue=%d, %v; want %d", n, err, want)
		}
	}
	items, err := q.Claim(1)
	if err != nil || len(items) != 1 || items[0].NodeKey != item.NodeKey {
		t.Fatalf("claim=%+v, %v", items, err)
	}
	if second, err := q.Claim(1); err != nil || len(second) != 0 {
		t.Fatalf("claimed twice: %+v, %v", second, err)
	}
	if err := q.Complete(item.Key(), Resolution{}); err != nil {
		t.Fatal(err)
	}
	if counts, err := q.Stats(); err != nil || counts[StatusDone] != 1 {
		t.Fatalf("completed queue=%v, %v", counts, err)
	}
}
