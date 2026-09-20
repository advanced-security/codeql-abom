package expand

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Hold serializes this process's workers and nothing beyond them, while a cache directory is
// persistent and named on the command line. Two processes over one cache would miss, promote and
// evict the same entries with nothing between them, and a Trim in one can remove the tree the
// other is building a database from. There is no protocol for that, so sharing is refused.
func TestASecondProcessCannotClaimACacheAlreadyInUse(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkouts")
	first := &CheckoutCache{Dir: dir}
	if err := first.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	defer first.Release()

	second := &CheckoutCache{Dir: dir}
	err := second.Claim()
	if !errors.Is(err, ErrCacheInUse) {
		t.Fatalf("Claim on a cache already in use = %v, want ErrCacheInUse", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatal("the test directory is not absolute, so the message would not name the cache")
	}
}

// The lock is held for as long as the cache is, not for the life of the machine. A run that
// finishes gives the directory back to the next one.
func TestAReleasedCacheCanBeClaimedAgain(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkouts")
	first := &CheckoutCache{Dir: dir}
	if err := first.Claim(); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	first.Release()

	second := &CheckoutCache{Dir: dir}
	if err := second.Claim(); err != nil {
		t.Errorf("Claim after Release: %v", err)
	}
	second.Release()
}

// The cache retains fetched source from private repositories, where the temporary work directory
// it replaces was private by construction. A staging directory other local users can walk into
// turns a performance option into a disclosure.
func TestStagingIsNotReadableByOtherUsers(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	if _, err := cache.Stage(); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	info, err := os.Stat(filepath.Join(cache.Dir, stagingDir))
	if err != nil {
		t.Fatalf("stat staging: %v", err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("staging mode = %#o, want nothing outside the owner", mode)
	}
}

// The parent of an entry is created by the promotion, not by the command that made the cache
// root, so tightening only the root would leave every owner directory open.
func TestARetainedEntryIsNotReadableByOtherUsers(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	patterns := []string{"/.github/"}
	entry := retain(t, cache, "actions", "checkout", "aaaa1111", patterns, 16)

	for dir := filepath.Dir(entry); dir != cache.Dir; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s mode = %#o, want nothing outside the owner", dir, mode)
		}
	}
}
