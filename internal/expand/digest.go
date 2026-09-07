package expand

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
)

// IndexedDigest is the content identity of a checkout, which is what a CodeQL database is built
// from. Two checkouts sharing a digest cannot produce different results, because the extractor
// reads nothing else and the evaluator is deterministic.
//
// The whole tree rather than a second copy of the sparse rule: the fetch has already narrowed
// this to what is worth indexing, and restating that rule here would give it two definitions
// that could drift, with the drift only visible as a skipped analysis. Dot-directories are
// excluded except `.github`, which is most of the point — `.git` differs between two clones of
// the same commit, so including it would make every digest unique and the check inert.
func IndexedDigest(dir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && name != ".github" && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		// Symlinks and the like are not read by the extractor and are not reproducible.
		if !d.Type().IsRegular() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking checkout %s: %w", dir, err)
	}
	// An empty tree hashes to a real value, and every empty tree to the same one. Reporting no
	// digest instead keeps "there was nothing to index" from matching itself across repositories.
	if len(paths) == 0 {
		return "", nil
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
