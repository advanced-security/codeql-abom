package coordination

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const LeaseDuration = 30 * time.Second

var ErrCoordinatorActive = errors.New("another coordinator owns this database's discovery lease")

// Hold fences a coordinator's writes and cancels its work if renewal fails.
func Hold(ctx context.Context, backend store.Backend) (context.Context, store.Backend, func() error, error) {
	return HoldWaiting(ctx, backend, 0)
}

// HoldWaiting is Hold, asking again every few seconds for up to wait while another coordinator
// owns the lease. A replacement for one that died holding it then starts when the lease lapses.
func HoldWaiting(ctx context.Context, backend store.Backend, wait time.Duration) (context.Context, store.Backend, func() error, error) {
	ticker := time.NewTicker(LeaseDuration / 3)
	var retries, giveUp <-chan time.Time
	if wait > 0 {
		retry := time.NewTicker(LeaseDuration / 6)
		defer retry.Stop()
		deadline := time.NewTimer(wait)
		defer deadline.Stop()
		retries, giveUp = retry.C, deadline.C
	}
	work, scoped, release, err := waitFor(ctx, backend, retries, giveUp, ticker.C)
	if err != nil {
		ticker.Stop()
		return nil, nil, nil, err
	}
	return work, scoped, func() error {
		ticker.Stop()
		return release()
	}, nil
}

func waitFor(ctx context.Context, backend store.Backend, retries, giveUp, ticks <-chan time.Time) (context.Context, store.Backend, func() error, error) {
	for {
		work, scoped, release, err := hold(ctx, backend, ticks)
		if !errors.Is(err, ErrCoordinatorActive) || (retries == nil && giveUp == nil) {
			return work, scoped, release, err
		}
		select {
		case <-retries:
		case <-giveUp:
			return nil, nil, nil, err
		case <-ctx.Done():
			return nil, nil, nil, errors.Join(err, ctx.Err())
		}
	}
}

func hold(ctx context.Context, backend store.Backend, ticks <-chan time.Time) (context.Context, store.Backend, func() error, error) {
	lease, acquired, err := backend.AcquireLease("discovery", rand.Text(), LeaseDuration)
	if err != nil {
		return nil, nil, nil, err
	}
	if !acquired {
		return nil, nil, nil, ErrCoordinatorActive
	}
	scoped, err := backend.WithLease(lease)
	if err != nil {
		return nil, nil, nil, errors.Join(err, backend.ReleaseLease(lease))
	}
	work, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var failure error
	go func() {
		defer close(done)
		for {
			select {
			case <-work.Done():
				return
			case <-ticks:
				_, renewed, err := backend.RenewLease(lease, LeaseDuration)
				if err != nil || !renewed {
					if err == nil {
						err = store.ErrLeaseLost
					}
					failure = fmt.Errorf("renewing coordinator lease: %w", err)
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var released error
	release := func() error {
		once.Do(func() {
			cancel()
			<-done
			released = errors.Join(failure, backend.ReleaseLease(lease))
		})
		return released
	}
	return work, scoped, release, nil
}
