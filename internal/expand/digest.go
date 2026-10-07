package expand

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// treeScheme labels a digest taken over git's blob ids. Digests recorded before it hashed file
// contents and carry no label, so no digest of one scheme can ever equal one of the other.
const treeScheme = "tree1"

// modeLink is git's mode for a symbolic link, which it stores as a blob holding the target.
const modeLink = "120000"

// ListedEntry is one path of a commit's tree as GitHub lists it.
type ListedEntry struct {
	Path string
	Mode string
	// Type is `blob`, `tree` or `commit`, the last being a submodule.
	Type string
	SHA  string
}

// Listing is a commit's whole tree, read without fetching any of it.
type Listing struct {
	Entries []ListedEntry
	// Truncated is GitHub saying the listing stopped short of the tree.
	Truncated bool
}

// contentEntry is one path a commit's indexed content holds, named by git's id for its bytes.
type contentEntry struct {
	path string
	blob string
	// link separates a symbolic link from a file holding its target, which git gives one id.
	link bool
}

// CheckoutDigest is the content identity of a checkout, which is what a CodeQL database is built
// from. Two checkouts sharing a digest cannot produce different results, because the extractor
// reads nothing else and the evaluator is deterministic.
//
// Each file is named by the id git gives its bytes, so ListingDigest can compute the same value
// from a commit's tree listing without fetching it. Both go through pathFilter and contentDigest:
// a second statement of which paths count would let the readers drift, with the drift visible
// only as a skipped analysis. `.git` is excluded with every other dot-directory but `.github`,
// because it differs between two clones of one commit and would make every digest unique.
func CheckoutDigest(dir string, patterns []string) (string, error) {
	entries, outside, err := checkoutEntries(dir, parseFilter(patterns))
	if err != nil {
		return "", err
	}
	// Git took a file this statement does not count, so a digest would omit something the
	// extractor reads and call two different trees identical.
	if len(outside) > 0 {
		return "", nil
	}
	return contentDigest(entries), nil
}

// checkoutEntries reads a checkout's indexed content, and the paths git checked out that the
// filter does not count.
func checkoutEntries(dir string, filter pathFilter) ([]contentEntry, []string, error) {
	var entries []contentEntry
	var outside []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && hiddenDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("relating %s: %w", path, err)
		}
		rel = filepath.ToSlash(rel)
		switch {
		case !d.Type().IsRegular() && d.Type()&fs.ModeSymlink == 0:
			// Sockets and devices are nothing git stores.
		case !filter.counts(rel):
			outside = append(outside, rel)
		case d.Type().IsRegular():
			blob, err := fileBlob(path)
			if err != nil {
				return err
			}
			entries = append(entries, contentEntry{path: rel, blob: blob})
		default:
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("reading link %s: %w", path, err)
			}
			entries = append(entries, contentEntry{path: rel, blob: blobID([]byte(target)), link: true})
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walking checkout %s: %w", dir, err)
	}
	return entries, outside, nil
}

// ListingDigest is CheckoutDigest computed from GitHub's listing of a commit's tree.
//
// What the listing cannot vouch for has no digest: a truncated listing says nothing about the
// rest of the tree, an entry without an id names no content, and a filter this cannot evaluate
// as git does might count files a checkout would never hold.
func ListingDigest(l Listing, patterns []string) string {
	filter := parseFilter(patterns)
	if l.Truncated || filter.opaque {
		return ""
	}
	var entries []contentEntry
	for _, e := range l.Entries {
		// A directory is implied by the paths under it, and a submodule is an empty directory in
		// a checkout: neither has bytes the extractor reads.
		if e.Type != "blob" || !filter.counts(e.Path) {
			continue
		}
		if e.SHA == "" {
			return ""
		}
		entries = append(entries, contentEntry{
			path: e.Path, blob: strings.ToLower(e.SHA), link: e.Mode == modeLink,
		})
	}
	return contentDigest(entries)
}

// contentDigest hashes a tree's indexed entries in path order.
//
// An empty tree hashes to a real value, and every empty tree to the same one. Reporting no digest
// instead keeps "there was nothing to index" from matching itself across commits.
func contentDigest(entries []contentEntry) string {
	if len(entries) == 0 {
		return ""
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	h := sha256.New()
	for _, e := range entries {
		kind := "file"
		if e.link {
			kind = "link"
		}
		// Length-prefixed, so two paths cannot be concatenated into a third that hashes the same.
		fmt.Fprintf(h, "%d:%s\x00%s:%s\x00", len(e.path), e.path, kind, e.blob)
	}
	return treeScheme + ":" + hex.EncodeToString(h.Sum(nil))
}

// fileBlob is git's object id for a file in a checkout, read from the bytes the extractor reads.
// SHA-1 because that is how git names a blob, which is what lets a listing be compared at all.
func fileBlob(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", info.Size())
	n, err := io.Copy(h, f)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if n != info.Size() {
		return "", fmt.Errorf("%s changed while it was read", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// blobID is git's object id for bytes held in memory.
func blobID(body []byte) string {
	h := sha1.New()
	h.Write([]byte("blob " + strconv.Itoa(len(body)) + "\x00"))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// hiddenDirectory reports a directory the digest never descends into.
func hiddenDirectory(name string) bool {
	return name != ".github" && strings.HasPrefix(name, ".")
}

// pathFilter states which paths of a commit are its indexed content: what the sparse filter
// fetches, under no dot-directory but `.github`.
//
// The patterns are read with git's non-cone sparse-checkout rules, for literal patterns only. A
// checkout is already narrowed by git, so applying this to one changes nothing wherever the two
// agree; a listing is the whole tree, and this is all that narrows it.
type pathFilter struct {
	patterns []sparsePattern
	// whole is a fetch that takes everything, which is what no patterns means.
	whole bool
	// opaque is a pattern this cannot evaluate as git would: a wildcard, a negation, an escape.
	opaque bool
}

type sparsePattern struct {
	parts    []string
	anchored bool
	dirOnly  bool
}

func parseFilter(patterns []string) pathFilter {
	f := pathFilter{whole: len(patterns) == 0}
	for _, raw := range patterns {
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if strings.ContainsAny(raw, "*?[\\ \t") || strings.HasPrefix(raw, "!") {
			f.opaque = true
			continue
		}
		p := sparsePattern{dirOnly: strings.HasSuffix(raw, "/")}
		body := strings.TrimSuffix(raw, "/")
		// A slash anywhere but the end anchors a pattern to the root, as in gitignore.
		p.anchored = strings.Contains(body, "/")
		p.parts = strings.Split(strings.TrimPrefix(body, "/"), "/")
		if slicesContainEmpty(p.parts) {
			f.opaque = true
			continue
		}
		f.patterns = append(f.patterns, p)
	}
	return f
}

func slicesContainEmpty(parts []string) bool {
	for _, p := range parts {
		if p == "" {
			return true
		}
	}
	return false
}

// counts reports whether a repository-relative path is indexed content.
func (f pathFilter) counts(path string) bool {
	parts := strings.Split(path, "/")
	for _, dir := range parts[:len(parts)-1] {
		if hiddenDirectory(dir) {
			return false
		}
	}
	// Only a checkout reaches here with an opaque filter, and git has already applied it.
	if f.whole || f.opaque {
		return true
	}
	for _, p := range f.patterns {
		if p.matches(parts) {
			return true
		}
	}
	return false
}

// coversDirectory reports whether everything under a directory counts, which is what lets two
// identical digests stand for identical readings of anything inside it.
func (f pathFilter) coversDirectory(dir string) bool {
	if f.opaque {
		return false
	}
	parts := strings.Split(dir, "/")
	for _, d := range parts {
		if hiddenDirectory(d) {
			return false
		}
	}
	if f.whole {
		return true
	}
	for _, p := range f.patterns {
		if p.matchesDirectory(parts) {
			return true
		}
	}
	return false
}

// matches applies one pattern to a file's path. A pattern matching a directory takes everything
// under it, and a pattern ending in a slash matches only directories.
//
// Names compare without case because git does on a case-insensitive filesystem, and a digest
// that missed a file git checked out would call two different trees identical.
func (p sparsePattern) matches(parts []string) bool {
	if p.matchesDirectory(parts[:len(parts)-1]) {
		return true
	}
	if p.dirOnly {
		return false
	}
	if p.anchored {
		return len(parts) == len(p.parts) && sameNames(parts, p.parts)
	}
	return strings.EqualFold(parts[len(parts)-1], p.parts[0])
}

// matchesDirectory reports whether the pattern matches a directory or any of its ancestors.
func (p sparsePattern) matchesDirectory(dirs []string) bool {
	if p.anchored {
		return len(dirs) >= len(p.parts) && sameNames(dirs[:len(p.parts)], p.parts)
	}
	for _, d := range dirs {
		if strings.EqualFold(d, p.parts[0]) {
			return true
		}
	}
	return false
}

func sameNames(a, b []string) bool {
	for i := range b {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}
