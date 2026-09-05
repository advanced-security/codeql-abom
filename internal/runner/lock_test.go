package runner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Two CodeQL processes sharing the compilation cache can return wrong answers to both, so the
// pipeline may run four repositories at once but only one CodeQL invocation.
func TestOnlyOneCodeQLInvocationHoldsTheLockAtATime(t *testing.T) {
	lock := &Lock{Path: filepath.Join(t.TempDir(), "codeql.lock")}

	var mu sync.Mutex
	inside, overlaps := 0, 0
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := lock.Acquire(context.Background())
			if err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			defer release()

			mu.Lock()
			inside++
			if inside > 1 {
				overlaps++
			}
			mu.Unlock()

			time.Sleep(time.Millisecond)

			mu.Lock()
			inside--
			mu.Unlock()
		}()
	}
	wg.Wait()

	if overlaps != 0 {
		t.Errorf("%d overlapping holders, want the lock to admit one at a time", overlaps)
	}
}

// A CodeQL invocation that fails still has to hand the lock back, or one bad repository stalls
// every later round.
func TestTheLockIsReleasedAfterAFailure(t *testing.T) {
	lock := &Lock{Path: filepath.Join(t.TempDir(), "codeql.lock")}

	func() {
		release, err := lock.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		defer release()
	}()

	done := make(chan struct{})
	go func() {
		release, err := lock.Acquire(context.Background())
		if err == nil {
			release()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the lock was never released")
	}
}

// Waiting for the lock must observe cancellation, or stopping discovery would hang behind
// whatever CodeQL is doing.
func TestWaitingForTheLockRespectsCancellation(t *testing.T) {
	lock := &Lock{Path: filepath.Join(t.TempDir(), "codeql.lock")}
	release, err := lock.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := lock.Acquire(ctx); err == nil {
		t.Error("a canceled context still acquired the lock")
	}
}

// The lock file is the cross-process half: another abom process on the same machine has to see
// it, so it must be a real file at the configured path rather than a mutex alone.
func TestTheLockFileIsCreatedWhereItWasConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "codeql.lock")
	lock := &Lock{Path: path}

	release, err := lock.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if _, err := os.Stat(path); err != nil {
		t.Errorf("stat %s: %v", path, err)
	}
}

// A runner with no lock configured still runs. Unit tests and one-shot commands have no second
// process to contend with, and requiring a lock file would make the zero value useless.
func TestAnUnconfiguredLockIsNotAnError(t *testing.T) {
	var lock *Lock

	release, err := lock.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	release()
}

// The point of the lock is that the invocations take it. Naming them one by one is deliberate:
// a new CodeQL call that touches the shared cache has to be added here consciously.
func TestEveryCacheContendingInvocationTakesTheLock(t *testing.T) {
	dir := t.TempDir()
	held := make(chan string, 3)

	run := func(name string, call func(*Runner) error) {
		t.Helper()
		lock := &Lock{Path: filepath.Join(dir, name+".lock")}
		r := &Runner{CodeQLPath: filepath.Join(dir, "absent-codeql"), Lock: lock}

		// Holding it first means the call can only proceed by waiting, so a call that never
		// asks for the lock finishes immediately and fails the test.
		release, err := lock.Acquire(context.Background())
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}

		done := make(chan struct{})
		go func() {
			// The binary does not exist, so the call fails — after the lock is taken, which is
			// the only thing under test.
			_ = call(r)
			close(done)
		}()

		select {
		case <-done:
			t.Errorf("%s ran without waiting for the lock", name)
		case <-time.After(150 * time.Millisecond):
			held <- name
		}
		release()
		<-done
	}

	run("create", func(r *Runner) error {
		return r.CreateDatabase(context.Background(), dir, filepath.Join(dir, "db"), "actions")
	})
	run("analyze", func(r *Runner) error {
		return r.AnalyzeQueries(context.Background(), "/db", []string{"x.qls"},
			filepath.Join(dir, "out.sarif"))
	})
	run("query", func(r *Runner) error {
		return r.RunQuery(context.Background(), "/db", "x.ql", filepath.Join(dir, "out.bqrs"))
	})

	if len(held) != 3 {
		t.Errorf("%d of 3 invocations waited for the lock", len(held))
	}
}
