package expand

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// cacheLockName is the file a process flocks to claim the cache. A leading dot keeps it out of
// the owner namespace, which cacheSegment refuses to write a dot into.
const cacheLockName = ".lock"

// ErrCacheInUse reports that another process already owns the cache directory.
var ErrCacheInUse = errors.New("checkout cache: already claimed by another process")

// Claim takes this process's exclusive ownership of the cache directory.
//
// Hold serializes this process's own workers and nothing beyond them. A cache directory is
// persistent and named on the command line, so a crawl and a hand-run expand-all can be pointed
// at one: they would miss, promote and evict the same entries with nothing between them, and a
// Trim in one can remove the tree the other is building a database from. There is no
// cross-process protocol here, so sharing is refused rather than half-protected.
//
// The lock is advisory and held by the kernel, so a process that dies releases it. A lock file
// created with O_EXCL would need a liveness rule of its own, and a crashed crawl would lock its
// cache out until somebody deleted the file by hand.
func (c *CheckoutCache) Claim() error {
	if c == nil || c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o700); err != nil {
		return fmt.Errorf("checkout cache: preparing %s: %w", c.Dir, err)
	}
	// Opened through a root confined to the cache, so a symlink standing where the lock belongs
	// cannot make this flock a file elsewhere on the machine.
	root, err := os.OpenRoot(c.Dir)
	if err != nil {
		return fmt.Errorf("checkout cache: opening %s: %w", c.Dir, err)
	}
	defer func() { _ = root.Close() }()

	// Read-only: nothing is ever written to the lock file, only flocked.
	f, err := root.OpenFile(cacheLockName, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return fmt.Errorf("checkout cache: opening the lock in %s: %w", c.Dir, err)
	}
	// Non-blocking: waiting would be worse than refusing, because the other process holds this
	// for as long as it crawls.
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("%w: %s is in use; give each process its own --cache-dir",
				ErrCacheInUse, c.Dir)
		}
		return fmt.Errorf("checkout cache: locking %s: %w", c.Dir, err)
	}

	c.mu.Lock()
	c.lock = f
	c.mu.Unlock()
	return nil
}

// Release gives the cache directory up. A process that exits without calling this releases it
// anyway; this is for a run that ends while the process goes on to something else.
func (c *CheckoutCache) Release() {
	if c == nil {
		return
	}
	c.mu.Lock()
	f := c.lock
	c.lock = nil
	c.mu.Unlock()
	if f == nil {
		return
	}
	// Unlocked before closing: closing drops it anyway, but doing it explicitly keeps a failure
	// visible rather than implicit.
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}
