package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A machine-wide lock file in the OS temp directory is world-writable: any other local user can
// hold it, or leave a symlink where it is about to be created. Declining the cross-process half is
// the safer answer, and the in-process half still serializes this process's own workers.
func TestSharedLockNeverFallsBackToTheOSTempDirectory(t *testing.T) {
	// What os.UserCacheDir reads, per platform. Emptying them all is the only way to reach the
	// branch this covers.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")

	l := SharedLock()

	if l.Path != "" {
		t.Errorf("Path = %q, want no cross-process lock when there is no cache directory", l.Path)
	}
	if tmp := os.TempDir(); l.Path != "" && strings.HasPrefix(l.Path, tmp) {
		t.Errorf("Path = %q is under the OS temp directory %q", l.Path, tmp)
	}
}

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

// A path that could not be opened is named in the error, and these paths descend from the cache
// directory the environment chose. A newline in one would print as a second line of output --
// which reads as a separate log entry that somebody else wrote.
func TestAFailingPathCannotForgeALineInTheError(t *testing.T) {
	// A file standing where the lock directory belongs: the name is taken, so creating it is
	// tolerated, and opening it as a directory is what fails.
	blocker := filepath.Join(t.TempDir(), "a\nb")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatalf("writing the blocker: %v", err)
	}

	lock := &Lock{Path: filepath.Join(blocker, "codeql.lock")}
	release, err := lock.Acquire(context.Background())
	if err == nil {
		release()
		t.Fatal("a file standing in for the lock directory was accepted")
	}
	if strings.Contains(err.Error(), "\n") {
		t.Errorf("the error spans more than one line: %q", err.Error())
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

// A lock file is opened at a path derived from the user's cache directory, which comes from the
// environment. Following a symlink out of that directory would flock a file ABOM was never meant
// to touch, and O_CREATE|O_RDONLY follows one silently.
func TestALockRefusesToFollowASymlinkOutOfItsDirectory(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "elsewhere.lock")
	if err := os.WriteFile(outside, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codeql.lock")
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	lock := &Lock{Path: path}
	release, err := lock.Acquire(context.Background())
	if err == nil {
		release()
		t.Fatal("acquired a lock through a symlink pointing outside its own directory")
	}
}

// The same redirection one level up. `Mkdir` returning ErrExist says only that the name is
// taken, not that it is a directory, so a symlink planted where ABOM's own directory belongs
// survives that check -- and reopening the path by name afterwards follows it. The lock would
// then be held on a file somewhere else entirely, which serializes nothing and says nothing.
func TestALockRefusesADirectorySymlinkOutOfTheCacheParent(t *testing.T) {
	cache := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cache, "codeql-abom")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	lock := &Lock{Path: filepath.Join(cache, "codeql-abom", "codeql.lock")}
	release, err := lock.Acquire(context.Background())
	if err == nil {
		release()
		t.Error("acquired a lock through a directory symlink leaving the cache parent")
	}
	if _, err := os.Stat(filepath.Join(outside, "codeql.lock")); err == nil {
		t.Errorf("the lock file was created outside the cache directory, in %s", outside)
	}
}

// os.UserCacheDir names a directory; it does not create one. On a fresh account nothing has
// written to `~/.cache` yet, and the lock has to work there rather than failing before the first
// CodeQL invocation of that account's life.
func TestALockIsTakenEvenWhenTheCacheRootDoesNotExistYet(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "cache", "codeql-abom")

	lock := &Lock{Path: filepath.Join(absent, "codeql.lock")}
	release, err := lock.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire with no cache root yet: %v", err)
	}
	defer release()

	if _, err := os.Stat(filepath.Join(absent, "codeql.lock")); err != nil {
		t.Errorf("the lock file was not created: %v", err)
	}
}

// The cache root is one missing element; a relocated XDG_CACHE_HOME can leave several. The chain
// is built inside a root anchored at the deepest directory that does exist, so finding it means
// walking up more than one level -- a search that stopped at the first parent would create
// nothing and give no reason.
func TestALockIsTakenWhenSeveralPathElementsAreMissing(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "cache", "deeply", "nested", "codeql-abom")

	lock := &Lock{Path: filepath.Join(absent, "codeql.lock")}
	release, err := lock.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire with several elements missing: %v", err)
	}
	defer release()

	if _, err := os.Stat(filepath.Join(absent, "codeql.lock")); err != nil {
		t.Errorf("the lock file was not created: %v", err)
	}
}
