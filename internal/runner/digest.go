package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// digestedExtensions are the files that can change what a query computes.
//
// `.yml` covers both the manifest and `codeql-pack.lock.yml`, and the lock file is the point:
// it is the resolved dependency closure, so a dependency bump moves the digest even though no
// file here was touched. Expected outputs and documentation are excluded because invalidating
// the corpus for a typo fix would make the digest too expensive to trust.
var digestedExtensions = map[string]bool{
	".ql": true, ".qll": true, ".qls": true, ".yml": true, ".yaml": true,
}

// PackDigest is the content identity of a CodeQL pack.
//
// A pack version cannot serve as one here: ours track the CodeQL CLI, so editing a query leaves
// the version alone, and upstream versions are only immutable by convention that nothing in the
// lock file lets us verify. Hashing the directory settles both with one rule.
//
// Whole-pack rather than per-query: resolving transitive imports to hash only what a query
// actually reaches would be more precise and much more code. This over-invalidates — editing one
// library re-runs every query in the pack — which is sound and cheap.
func PackDigest(dir string) (string, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("reading pack %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("pack %s is not a directory", dir)
	}

	var paths []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Caches and version control hold no query text and are not reproducible.
			if name := d.Name(); name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if digestedExtensions[strings.ToLower(filepath.Ext(path))] {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking pack %s: %w", dir, err)
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, path := range paths {
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return "", fmt.Errorf("relating %s: %w", path, err)
		}
		// Forward slashes and a length prefix, so the digest is identical on every platform and
		// two files cannot be concatenated into a third that hashes the same.
		rel = filepath.ToSlash(rel)
		fmt.Fprintf(h, "%d:%s\x00", len(rel), rel)

		f, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("opening %s: %w", path, err)
		}
		body := sha256.New()
		if _, err := io.Copy(body, f); err != nil {
			f.Close()
			return "", fmt.Errorf("reading %s: %w", path, err)
		}
		f.Close()
		h.Write(body.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// DigestCache remembers a pack's digest for the life of a round.
//
// A crawl resolves the same handful of packs for every batch, and hashing an installed upstream
// pack means reading a few hundred files. Once per round is enough: a pack cannot change while
// the round that resolved it is running.
type DigestCache struct {
	mu sync.Mutex
	by map[string]string
}

// Of returns the digest of the pack containing queryPath, or "" when the query is in no pack.
func (c *DigestCache) Of(packDir string) (string, error) {
	if packDir == "" {
		return "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if digest, ok := c.by[packDir]; ok {
		return digest, nil
	}
	digest, err := PackDigest(packDir)
	if err != nil {
		return "", err
	}
	if c.by == nil {
		c.by = map[string]string{}
	}
	c.by[packDir] = digest
	return digest, nil
}

// Reset forgets every digest, which is how the next round starts.
func (c *DigestCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by = nil
}
