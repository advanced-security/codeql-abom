package coordination

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type backend struct {
	store.Backend
	held     bool
	renewed  bool
	releases atomic.Int64
}

func (b *backend) AcquireLease(resource, owner string, ttl time.Duration) (store.Lease, bool, error) {
	return store.Lease{Resource: resource, Owner: owner, Fence: 1}, b.held, nil
}
func (b *backend) WithLease(store.Lease) (store.Backend, error) { return b, nil }
func (b *backend) RenewLease(lease store.Lease, ttl time.Duration) (store.Lease, bool, error) {
	return lease, b.renewed, nil
}
func (b *backend) ReleaseLease(store.Lease) error {
	b.releases.Add(1)
	return nil
}

func TestLosingOwnershipCancelsWorkAndIsReported(t *testing.T) {
	b := &backend{held: true}
	ticks := make(chan time.Time, 1)
	ctx, _, release, err := hold(t.Context(), b, ticks)
	if err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("work was not canceled when the lease was lost")
	}
	if err := release(); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("lease loss was hidden: %v", err)
	}
	_ = release()
	if b.releases.Load() != 1 {
		t.Fatal("cleanup released ownership more than once")
	}
}

func TestASecondCoordinatorCannotStartWork(t *testing.T) {
	b := &backend{}
	ctx, scoped, release, err := hold(context.Background(), b, nil)
	if !errors.Is(err, ErrCoordinatorActive) || ctx != nil || scoped != nil || release != nil {
		t.Fatalf("an unavailable lease returned usable work: %v", err)
	}
}
