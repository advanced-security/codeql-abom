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
	ticker := time.NewTicker(LeaseDuration / 3)
	work, scoped, release, err := hold(ctx, backend, ticker.C)
	if err != nil {
		ticker.Stop()
		return nil, nil, nil, err
	}
	return work, scoped, func() error {
		ticker.Stop()
		return release()
	}, nil
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
