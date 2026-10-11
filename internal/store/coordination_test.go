package store

import (
	"errors"
	"testing"
	"time"
)

// A coordinator that lost its lease must not finish an attempt or publish what it buffered: its
// successor has already taken the work over.
func TestSchedulerOwnershipAndIngestionReceiptsAreFenced(t *testing.T) {
	var s Backend = newTestStore(t)
	first, acquired, err := s.AcquireLease("discovery", "first", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first acquisition: %+v %v %v", first, acquired, err)
	}
	if _, acquired, err := s.AcquireLease("discovery", "second", time.Minute); err != nil || acquired {
		t.Fatalf("a second coordinator acquired a live lease: %v %v", acquired, err)
	}
	renewed, ok, err := s.RenewLease(first, time.Minute)
	if err != nil || !ok || renewed.Fence != first.Fence {
		t.Fatalf("renewal changed ownership: %+v %v %v", renewed, ok, err)
	}
	checkReceiptReplay(t, s, first)
	if began, err := s.BeginReceipt("attempt-two", "digest-two", first); err != nil || !began {
		t.Fatal("second receipt was not acquired", err)
	}
	fenced, err := s.WithLease(first)
	if err != nil {
		t.Fatal(err)
	}
	writer := fenced.NewWriter(0)
	if err := writer.AddNode(Node{Key: "stale/producer", Kind: "action"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReleaseLease(first); err != nil {
		t.Fatal(err)
	}
	second, acquired, err := s.AcquireLease("discovery", "second", time.Minute)
	if err != nil || !acquired || second.Fence <= first.Fence {
		t.Fatalf("takeover did not advance the fence: %+v %v %v", second, acquired, err)
	}
	if _, ok, err := s.RenewLease(first, time.Minute); err != nil || ok {
		t.Fatalf("a former coordinator renewed somebody else's lease: %v %v", ok, err)
	}
	if err := s.CompleteReceipt("attempt-two", "digest-two", first); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a former coordinator completed an attempt: %v", err)
	}
	if err := writer.Flush(); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("a former coordinator wrote buffered graph facts: %v", err)
	}
	if _, err := s.NodeID("stale/producer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the rejected producer's graph facts were persisted: %v", err)
	}
	if began, err := s.BeginReceipt("attempt-two", "digest-two", second); err != nil || !began {
		t.Fatalf("takeover did not recover the incomplete attempt: %v %v", began, err)
	}
	if err := s.AbortReceipt("attempt-two", second); err != nil {
		t.Fatal(err)
	}
	if began, err := s.BeginReceipt("attempt-two", "digest-two", second); err != nil || !began {
		t.Fatalf("a failed attempt could not retry: %v %v", began, err)
	}
}

func checkReceiptReplay(t *testing.T, s Backend, lease Lease) {
	t.Helper()
	if began, err := s.BeginReceipt("attempt-one", "digest-one", lease); err != nil || !began {
		t.Fatalf("receipt acquisition: %v %v", began, err)
	}
	if _, err := s.BeginReceipt("attempt-one", "digest-one", lease); !errors.Is(err, ErrReceiptBusy) {
		t.Fatalf("an unfinished attempt looked completed: %v", err)
	}
	if err := s.CompleteReceipt("attempt-one", "digest-one", lease); err != nil {
		t.Fatal(err)
	}
	if began, err := s.BeginReceipt("attempt-one", "digest-one", lease); err != nil || began {
		t.Fatalf("a committed attempt was accepted twice: %v %v", began, err)
	}
	if _, err := s.BeginReceipt("attempt-one", "different-digest", lease); err == nil {
		t.Fatal("the same attempt ID accepted different evidence")
	}
}
