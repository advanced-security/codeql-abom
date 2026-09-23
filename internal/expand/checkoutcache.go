package expand

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CheckoutCache retains fetched source trees under the commit they came from, so a commit is
// fetched once however many references name it.
//
// The identity is the commit and the filter it was fetched under, never the reference: tags
// move, two tags can name one commit, and a branch names a different commit every week. A hit
// says only that the bytes are already here. Whether the analysis runs is decided separately, by
// the ledger, which is why reusing a tree cannot quietly skip work.
//
// A nil *CheckoutCache is a disabled cache, and every method tolerates one.
type CheckoutCache struct {
	// Dir holds the cache, outside WorkDir so entries outlive the round that fetched them.
	Dir string
	// MaxBytes is the ceiling Trim enforces. Zero or less retains without bound.
	MaxBytes int64

	mu sync.Mutex
	// holds carries a lock per commit currently being worked on, so two references resolving to
	// one commit produce one clone rather than two racing ones. Counted rather than kept: a
	// server runs for weeks over millions of commits, and a map that only grows is a leak.
	holds map[string]*commitHold
	// lock is this process's claim on the directory, held for as long as the cache is in use.
	lock *os.File

	hits atomic.Int64
}

// cacheMarker names the file describing an entry. It sits beside the tree rather than inside it,
// because the tree is what the database is built from and a file the fetch did not produce would
// change both the database and its digest.
const cacheMarker = "fetch.json"

// stagingDir holds fetches that have not been promoted. A leading dot keeps it out of the owner
// namespace, which cacheSegment refuses to write a dot into.
const stagingDir = ".staging"

// stagingLifetime is how long an unpromoted fetch is left alone. Above any clone, so a fetch in
// flight is never reclaimed under the process performing it; a crash leaves one behind, and that
// is what this reclaims.
const stagingLifetime = 24 * time.Hour

// cacheRecord is the marker's contents. It repeats the identity the path already encodes so a
// directory found on its own says what it is, and carries the filter because a tree fetched
// under narrower patterns cannot answer a wider request.
type cacheRecord struct {
	Owner     string   `json:"owner"`
	Repo      string   `json:"repo"`
	Commit    string   `json:"commit"`
	Patterns  []string `json:"patterns"`
	Bytes     int64    `json:"bytes"`
	FetchedAt string   `json:"fetched_at"`
}

// stagedTree is the tree within an entry. The same layout in staging and in place, so promotion
// is a rename rather than a copy.
func stagedTree(entry string) string { return filepath.Join(entry, "src") }

// Reuse reports the held tree for a commit, if one was fetched under the same filter.
//
// A miss is silent and costs a fetch; a hit under the wrong filter would cost correctness, so
// the patterns must match exactly. Over-inclusion changes the database that is built, and
// under-inclusion loses files the analysis is looking for.
func (c *CheckoutCache) Reuse(owner, repo, sha string, patterns []string) (string, bool) {
	if c == nil || c.Dir == "" {
		return "", false
	}
	entry, ok := c.entry(owner, repo, sha, patterns)
	if !ok {
		return "", false
	}
	held, err := readRecord(entry)
	if err != nil || !slices.Equal(held.Patterns, patterns) {
		return "", false
	}
	tree := stagedTree(entry)
	if info, err := os.Stat(tree); err != nil || !info.IsDir() {
		return "", false
	}
	// Recency is read from the marker, so an entry serving a crawl is not the one evicted.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(entry, cacheMarker), now, now)
	c.hits.Add(1)
	return tree, true
}

// Stage opens a directory for a fetch whose commit is not known yet.
//
// A fetch writes here and is promoted once it has a SHA, so a partial clone is never visible
// under a commit's name and an interrupted one cannot be mistaken for a complete tree.
func (c *CheckoutCache) Stage() (string, error) {
	if c == nil || c.Dir == "" {
		return "", fmt.Errorf("checkout cache: no directory configured")
	}
	root := filepath.Join(c.Dir, stagingDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("checkout cache: preparing staging: %w", err)
	}
	dir, err := os.MkdirTemp(root, "fetch-")
	if err != nil {
		return "", fmt.Errorf("checkout cache: staging a fetch: %w", err)
	}
	return dir, nil
}

// Retain promotes a staged fetch to the commit it turned out to be, returning the tree in place.
//
// The rename is within one filesystem and so is atomic: a reader either finds a complete entry
// or none at all.
func (c *CheckoutCache) Retain(
	staged, owner, repo, sha string, patterns []string,
) (string, error) {
	if c == nil || c.Dir == "" {
		return "", fmt.Errorf("checkout cache: no directory configured")
	}
	entry, ok := c.entry(owner, repo, sha, patterns)
	if !ok {
		return "", fmt.Errorf("checkout cache: %s/%s@%s cannot name an entry", owner, repo, sha)
	}
	if held, err := readRecord(entry); err == nil && slices.Equal(held.Patterns, patterns) {
		// Somebody else promoted this commit first. Their entry is as good as this one, and
		// replacing it would delete a tree another unit may be reading.
		if err := os.RemoveAll(staged); err != nil {
			return "", fmt.Errorf("checkout cache: discarding a duplicate fetch: %w", err)
		}
		return stagedTree(entry), nil
	}

	size, err := treeBytes(stagedTree(staged))
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(cacheRecord{
		Owner:     owner,
		Repo:      repo,
		Commit:    sha,
		Patterns:  patterns,
		Bytes:     size,
		FetchedAt: time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return "", fmt.Errorf("checkout cache: describing %s/%s@%s: %w", owner, repo, sha, err)
	}
	if err := os.WriteFile(filepath.Join(staged, cacheMarker), body, 0o600); err != nil {
		return "", fmt.Errorf("checkout cache: writing %s: %w", cacheMarker, err)
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		return "", fmt.Errorf("checkout cache: preparing %s: %w", filepath.Dir(entry), err)
	}
	// Whatever is there is the remains of a run that did not finish under this same filter: a
	// tree fetched under a different one lives under its own name and is left alone.
	if err := os.RemoveAll(entry); err != nil {
		return "", fmt.Errorf("checkout cache: replacing %s: %w", entry, err)
	}
	if err := os.Rename(staged, entry); err != nil {
		return "", fmt.Errorf("checkout cache: retaining %s/%s@%s: %w", owner, repo, sha, err)
	}
	return stagedTree(entry), nil
}

// commitHold is the lock on one commit and the number of units that still want it. The count is
// what lets the entry be forgotten again.
type commitHold struct {
	mu      sync.Mutex
	waiting int
}

// Hold serializes work on one commit. Two references that resolve to the same commit arrive
// together, and without this both miss the cache and both clone.
//
// The returned release is idempotent, and the last one to run forgets the commit.
func (c *CheckoutCache) Hold(owner, repo, sha string) func() {
	if c == nil || c.Dir == "" {
		return func() {}
	}
	key := strings.ToLower(owner + "/" + repo + "@" + sha)
	c.mu.Lock()
	if c.holds == nil {
		c.holds = make(map[string]*commitHold)
	}
	held, ok := c.holds[key]
	if !ok {
		held = &commitHold{}
		c.holds[key] = held
	}
	// Counted before the lock is taken, so an entry a waiter is queued on is not forgotten and
	// replaced by a second lock that would let both through at once.
	held.waiting++
	c.mu.Unlock()

	held.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			held.mu.Unlock()
			c.mu.Lock()
			held.waiting--
			if held.waiting == 0 {
				delete(c.holds, key)
			}
			c.mu.Unlock()
		})
	}
}

// Hits counts the trees served from the cache since the process started.
func (c *CheckoutCache) Hits() int64 {
	if c == nil {
		return 0
	}
	return c.hits.Load()
}

// Trim reclaims abandoned fetches and evicts entries until the cache is under its ceiling,
// least recently used first. It reports how many entries it removed.
//
// Called between rounds rather than during one: an entry a unit is analyzing must not be
// evicted from under it.
func (c *CheckoutCache) Trim() (int, error) {
	if c == nil || c.Dir == "" {
		return 0, nil
	}
	removed, err := c.reclaimStaging()
	if err != nil {
		return removed, err
	}
	entries, total, err := c.held()
	if err != nil {
		return removed, err
	}
	// An entry that cannot describe itself can never be a hit, so it occupies disk with nothing
	// to offer whatever the ceiling says.
	for _, held := range entries {
		if held.described {
			continue
		}
		if err := os.RemoveAll(held.path); err == nil {
			removed++
		}
	}
	if c.MaxBytes <= 0 || total <= c.MaxBytes {
		return removed, nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].used.Before(entries[j].used) })
	for _, held := range entries {
		if total <= c.MaxBytes {
			break
		}
		if !held.described {
			continue
		}
		if err := os.RemoveAll(held.path); err != nil {
			// One unremovable entry is not a reason to abandon the ceiling; the next is tried.
			continue
		}
		total -= held.bytes
		removed++
	}
	return removed, nil
}

// heldEntry is one cached commit as Trim sees it.
type heldEntry struct {
	path      string
	bytes     int64
	used      time.Time
	described bool
}

// held reads every entry and what it occupies. The recorded size is trusted rather than
// re-walked: a crawl's worth of trees is millions of files, and the size was measured when the
// tree was promoted and cannot change afterwards.
func (c *CheckoutCache) held() ([]heldEntry, int64, error) {
	owners, err := os.ReadDir(c.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("checkout cache: reading %s: %w", c.Dir, err)
	}
	var entries []heldEntry
	var total int64
	for _, owner := range owners {
		if !owner.IsDir() || owner.Name() == stagingDir {
			continue
		}
		for _, path := range c.commitDirs(owner.Name()) {
			held := heldEntry{path: path}
			record, err := readRecord(path)
			if err == nil {
				info, err := os.Stat(filepath.Join(path, cacheMarker))
				if err != nil {
					continue
				}
				held.described = true
				held.bytes = record.Bytes
				held.used = info.ModTime()
				total += record.Bytes
			}
			entries = append(entries, held)
		}
	}
	return entries, total, nil
}

// commitDirs lists every commit held under one owner.
func (c *CheckoutCache) commitDirs(owner string) []string {
	repos, err := os.ReadDir(filepath.Join(c.Dir, owner))
	if err != nil {
		return nil
	}
	var dirs []string
	for _, repo := range repos {
		if !repo.IsDir() {
			continue
		}
		commits, err := os.ReadDir(filepath.Join(c.Dir, owner, repo.Name()))
		if err != nil {
			continue
		}
		for _, commit := range commits {
			if !commit.IsDir() {
				continue
			}
			dirs = append(dirs, filepath.Join(c.Dir, owner, repo.Name(), commit.Name()))
		}
	}
	return dirs
}

// reclaimStaging removes fetches nothing promoted. A staged directory older than any clone
// belongs to a process that is gone.
func (c *CheckoutCache) reclaimStaging() (int, error) {
	root := filepath.Join(c.Dir, stagingDir)
	staged, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("checkout cache: reading %s: %w", root, err)
	}
	removed := 0
	cutoff := time.Now().Add(-stagingLifetime)
	for _, entry := range staged {
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err == nil {
			removed++
		}
	}
	return removed, nil
}

// entry is the directory a commit is held in, or false when the names cannot safely be one.
func (c *CheckoutCache) entry(owner, repo, sha string, patterns []string) (string, bool) {
	ownerSegment, ok := cacheSegment(owner)
	if !ok {
		return "", false
	}
	repoSegment, ok := cacheSegment(repo)
	if !ok {
		return "", false
	}
	// The filter joins the commit rather than standing beside it, because the two together are
	// one tree: the same commit under two filters is two different sets of files, and a single
	// slot for both would have each fetch delete the other's still-valid tree. A commit is
	// fixed-width hex and the id is hex, so the joined name still reads back unambiguously.
	commitSegment, ok := cacheSegment(sha + "-" + filterID(patterns))
	if !ok {
		return "", false
	}
	// Three segments rather than a joined name: foo/bar_baz and foo_bar/baz are different
	// repositories, and one joined segment cannot tell them apart.
	return filepath.Join(c.Dir, ownerSegment, repoSegment, commitSegment), true
}

// cacheSegment lowercases one path segment and reports whether the name can be one.
//
// Owner and repository names arrive from somebody else's workflow file, so they are input: a
// separator or a parent reference in one would write outside the cache. Refused rather than
// rewritten, because a rewritten name is a different repository. Case is folded because
// Actions/Checkout names the same repository as actions/checkout, and on a case-insensitive
// filesystem the two paths are one directory whatever this decides.
func cacheSegment(name string) (string, bool) {
	if name == "" || len(name) > 255 {
		return "", false
	}
	// A leading dot refuses "." and ".." along with the cache's own staging directory.
	if strings.HasPrefix(name, ".") {
		return "", false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return "", false
		}
	}
	return strings.ToLower(name), true
}

// readRecord reads an entry's marker.
func readRecord(entry string) (cacheRecord, error) {
	body, err := os.ReadFile(filepath.Join(entry, cacheMarker))
	if err != nil {
		return cacheRecord{}, err
	}
	var record cacheRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return cacheRecord{}, err
	}
	return record, nil
}

// treeBytes sums what a tree occupies. Shared by both caches, so it names neither.
func treeBytes(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			// A file that vanished between listing and measuring is not worth failing over.
			return nil //nolint:nilerr // a measurement, not a correctness claim
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("measuring %s: %w", dir, err)
	}
	return total, nil
}
