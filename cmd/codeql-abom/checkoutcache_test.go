package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The cache holds fetched source from repositories a token could read and other local users may
// not. The temporary work directory it replaces was private by construction, so a persistent
// cache that anyone can walk into turns a performance option into a disclosure.
func TestTheCheckoutCacheIsNotReadableByOtherUsers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkouts")

	cache, err := checkoutCache(dir, 16)
	if err != nil {
		t.Fatalf("checkoutCache: %v", err)
	}
	defer cache.Release()

	info, err := os.Stat(cache.Dir)
	if err != nil {
		t.Fatalf("stat %s: %v", cache.Dir, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("cache mode = %#o, want nothing outside the owner", mode)
	}
}

// MkdirAll leaves an existing directory's mode alone, so a cache made by an earlier version — or
// pointed at a directory somebody else created — would stay open with nothing to say so.
func TestAnExistingCacheDirectoryIsTightened(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkouts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("preparing an open cache: %v", err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	cache, err := checkoutCache(dir, 16)
	if err != nil {
		t.Fatalf("checkoutCache: %v", err)
	}
	defer cache.Release()

	info, err := os.Stat(cache.Dir)
	if err != nil {
		t.Fatalf("stat %s: %v", cache.Dir, err)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("an existing cache kept mode %#o, want nothing outside the owner", mode)
	}
}

// Both expand-all and serve take --cache-dir, and the entrypoint is shared. Claiming here is
// what makes the refusal cover both rather than whichever one remembered to ask.
func TestPointingASecondRunAtACacheInUseIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "checkouts")
	first, err := checkoutCache(dir, 16)
	if err != nil {
		t.Fatalf("checkoutCache: %v", err)
	}
	defer first.Release()

	if _, err := checkoutCache(dir, 16); err == nil {
		t.Error("a second run took the same cache, so each could evict what the other reads")
	}
}

// No directory is no cache, and no cache is not an error: retention is opt-in.
func TestNoCacheDirectoryIsNoCache(t *testing.T) {
	cache, err := checkoutCache("", 16)
	if err != nil {
		t.Fatalf("checkoutCache: %v", err)
	}
	if cache != nil {
		t.Errorf("cache = %+v, want none", cache)
	}
}
