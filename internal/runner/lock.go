package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Lock serializes CodeQL invocations across every process on the machine.
//
// CodeQL keeps one compilation and dependency cache per machine, shared by every invocation. Two
// evaluations running against it at once can return wrong answers to both, which is a far worse
// outcome than being slow: the pipeline may fetch four repositories at a time, but only one of
// them may be inside CodeQL.
//
// Two halves, because neither is sufficient. The mutex serializes this process's own workers,
// which is the common case. The file lock serializes against a second abom process, and is
// released by the kernel if that process dies, which a lock file created with O_EXCL would not
// be.
//
// It does not make `make ql` or the CodeQL MCP server cooperate: they take no lock. Those stay a
// documented rule, and this covers what the pipeline itself controls.
type Lock struct {
	// Path names the lock file. Empty means no cross-process locking, which is what unit tests
	// and one-shot commands want.
	Path string

	mu   sync.Mutex
	file *os.File
}

// SharedLock is the lock every abom process takes before invoking CodeQL.
//
// One path per user per machine, matching the scope of the compilation cache it protects, so a
// crawl and a hand-run `expand-all` serialize against each other rather than corrupting it.
//
// Where there is no cache directory there is no correct place for that lock, and it returns one
// that only serializes this process. Claiming a narrower scope than the cache has would be worse
// than claiming none: it reads as protection and is not.
func SharedLock() *Lock {
	dir, err := os.UserCacheDir()
	if err != nil {
		return &Lock{}
	}
	return &Lock{Path: filepath.Join(dir, "codeql-abom", "codeql.lock")}
}

// Acquire blocks until the caller may invoke CodeQL, returning the release.
//
// A nil Lock acquires immediately, so a Runner without one still works.
func (l *Lock) Acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := l.lockProcess(ctx); err != nil {
		return nil, err
	}
	if l.Path == "" {
		return l.mu.Unlock, nil
	}

	if err := l.lockFile(ctx); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	return func() {
		if l.file != nil {
			// Unlock before closing: closing drops the lock anyway, but doing it explicitly
			// keeps the failure visible rather than implicit.
			_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
			l.file.Close()
			l.file = nil
		}
		l.mu.Unlock()
	}, nil
}

// lockProcess takes the in-process half without ignoring cancellation, which sync.Mutex alone
// cannot do. Stopping discovery must not have to wait out a database build.
func (l *Lock) lockProcess(ctx context.Context) error {
	acquired := make(chan struct{})
	go func() {
		l.mu.Lock()
		close(acquired)
	}()

	select {
	case <-acquired:
		return nil
	case <-ctx.Done():
		// The goroutine still holds the mutex once it wins, so hand it straight back rather
		// than leaking a hold nobody will release.
		go func() {
			<-acquired
			l.mu.Unlock()
		}()
		return ctx.Err()
	}
}

func (l *Lock) lockFile(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o755); err != nil {
		return fmt.Errorf("creating the lock directory: %w", err)
	}
	// Read-only: nothing is ever written to the lock file, only flocked. Opening it for writing
	// would claim an access this never uses and make every close a potential lost write.
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_RDONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", l.Path, err)
	}

	// Non-blocking, polled, so cancellation is observed. A blocking flock would ignore ctx.
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			l.file = f
			return nil
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return fmt.Errorf("locking %s: %w", l.Path, err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return ctx.Err()
		case <-time.After(lockPollInterval):
		}
	}
}

// lockPollInterval trades a little latency for cancellability. A CodeQL invocation runs for
// seconds at least, so waking 20 times a second costs nothing against it.
const lockPollInterval = 50 * time.Millisecond
