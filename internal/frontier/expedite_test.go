package frontier

import (
	"testing"
)

// Value ranking is right for a crawl and wrong for a person. Someone who has just fixed a
// dependency they own is asking for one specific answer, and a queue that files that request
// behind nine hundred better-scored items has effectively refused it.
func TestAnExpeditedItemIsClaimedAheadOfBetterScoredWork(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("mine", 1), item("everyone-elses", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonality(t, s, "mine", 1, 1)
	seedCommonality(t, s, "everyone-elses", 150, 90)
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}

	if n, err := q.Expedite("mine"); err != nil || n != 1 {
		t.Fatalf("Expedite = %d, %v; want 1 item", n, err)
	}

	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d items, want 2", len(claimed))
	}
	if claimed[0].NodeKey != "mine" {
		t.Errorf("first claimed = %q, want the expedited item", claimed[0].NodeKey)
	}
}

// Expediting everything is expediting nothing, so the ranking still decides among them. Without
// this the order within a batch of requests would be whatever the table happened to return.
func TestExpeditedItemsAreStillRankedAmongThemselves(t *testing.T) {
	q, s := newQueue(t)

	if _, err := q.Enqueue([]Item{item("low", 1), item("high", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	seedCommonality(t, s, "low", 1, 1)
	seedCommonality(t, s, "high", 150, 90)
	if err := q.Rescore(); err != nil {
		t.Fatalf("Rescore: %v", err)
	}
	if _, err := q.Expedite("low", "high"); err != nil {
		t.Fatalf("Expedite: %v", err)
	}

	claimed, err := q.Claim(2)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if claimed[0].NodeKey != "high" {
		t.Errorf("first claimed = %q, want the better-scored of two expedited items",
			claimed[0].NodeKey)
	}
}

// The request is served once. Leaving the mark on would let an item that was expedited months
// ago jump the queue again every time a tier requeue put it back.
func TestCompletingAnItemSpendsItsExpediting(t *testing.T) {
	q, _ := newQueue(t)

	if _, err := q.Enqueue([]Item{item("mine", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := q.Expedite("mine"); err != nil {
		t.Fatalf("Expedite: %v", err)
	}
	if _, err := q.Claim(1); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := q.Complete(Of("mine"), Resolution{Ref: "HEAD", SHA: "sha"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var marked int
	if err := sqliteDB(t, q).QueryRow(
		`SELECT COUNT(*) FROM abom_frontier WHERE expedited_at IS NOT NULL`).Scan(&marked); err != nil {
		t.Fatalf("reading expedited_at: %v", err)
	}
	if marked != 0 {
		t.Error("a completed item is still marked to jump the queue")
	}
}

// Expediting something that is not queued has to say so. Silently reporting success would have
// an operator waiting on work nobody scheduled.
func TestExpeditingSomethingNotQueuedReportsNothingMarked(t *testing.T) {
	q, _ := newQueue(t)

	n, err := q.Expedite("never-heard-of-it")
	if err != nil {
		t.Fatalf("Expedite: %v", err)
	}
	if n != 0 {
		t.Errorf("marked %d items, want 0", n)
	}
}
