package expand

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// originWithLinks is originRepo with symbolic links beside the files, because git stores a link
// as a blob of its own and the lock reader follows one.
func originWithLinks(t *testing.T, files, links map[string]string) string {
	t.Helper()
	requireGit(t)

	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	for name, target := range links {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatalf("linking %s: %v", name, err)
		}
	}
	for _, args := range [][]string{
		{"init", "--initial-branch=main"},
		{"config", "user.email", "abom@example.test"},
		{"config", "user.name", "ABOM Test"},
		{"add", "-A"},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// lsTree lists a commit the way GitHub's tree API does: every path, recursively, trees included,
// with its mode, type and object id. Read from git itself so the listing is not this test's guess.
func lsTree(t *testing.T, repo string) Listing {
	t.Helper()
	cmd := exec.Command("git", "ls-tree", "-r", "-t", "-z", "--full-tree", "HEAD")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-tree: %v", err)
	}
	var l Listing
	for _, record := range bytes.Split(out, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, path, ok := strings.Cut(string(record), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			t.Fatalf("unreadable ls-tree record %q", record)
		}
		l.Entries = append(l.Entries, ListedEntry{
			Path: path, Mode: fields[0], Type: fields[1], SHA: fields[2],
		})
	}
	return l
}

// gitBlob is git's object id for a file's bytes, spelled out here so a fake cannot inherit a bug
// in the code under test.
func gitBlob(body string) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(body), body)))
	return hex.EncodeToString(sum[:])
}

func listingOf(files map[string]string) Listing {
	var l Listing
	for path, body := range files {
		l.Entries = append(l.Entries, ListedEntry{
			Path: path, Mode: "100644", Type: "blob", SHA: gitBlob(body),
		})
	}
	return l
}

// The checkout and the listing are two readers of one commit, and a settle trusts that they say
// the same thing. The tree holds what the filter takes at every depth, what it leaves out, a
// dot-directory git checks out and the digest does not count, a link, an executable and CRLF
// bytes, so a reader that disagrees with git or with the other reader about any of them fails.
func TestACheckoutAndAListingOfOneCommitDigestTheSame(t *testing.T) {
	origin := originWithLinks(t, map[string]string{
		".github/workflows/ci.yml":             "on: push\n",
		".github/workflows/crlf.yml":           "on: push\r\njobs: {}\r\n",
		".github/workflows/actions.lock":       "version: 'v0.0.2'\n",
		".github/actions/build/action.yml":     "runs:\n  using: composite\n",
		".github/actions/build/scripts/run.sh": "echo hi\n",
		".github/.cache/state":                 "a dot-directory under .github\n",
		".github/empty.txt":                    "",
		"action.yml":                           "name: root\n",
		"pkg/deep/action.yaml":                 "name: deep\n",
		"pkg/action.yml/inner.txt":             "a directory named like a manifest\n",
		"sub/.github/workflows/nested.yml":     "on: push\n",
		".hidden/action.yml":                   "name: hidden\n",
		"README.md":                            "# readme\n",
		"dist/index.js":                        "built\n",
	}, map[string]string{
		".github/workflows/linked.yml": "ci.yml",
	})

	tree := fetch(t, origin)
	fromCheckout, err := CheckoutDigest(tree.Dir, sparsePatterns)
	if err != nil {
		t.Fatalf("CheckoutDigest: %v", err)
	}
	listing := lsTree(t, origin)
	fromListing := ListingDigest(listing, sparsePatterns)

	if fromCheckout == "" || fromListing == "" {
		t.Fatalf("checkout digest %q, listing digest %q; both must describe the tree", fromCheckout, fromListing)
	}
	if fromCheckout != fromListing {
		t.Errorf("the checkout digested to %q and the listing of the same commit to %q",
			fromCheckout, fromListing)
	}

	// Content the filter leaves out cannot move the digest, and content it takes must.
	for path, counted := range map[string]bool{
		"README.md":                        false,
		"sub/.github/workflows/nested.yml": false,
		".hidden/action.yml":               false,
		".github/.cache/state":             false,
		".github/workflows/ci.yml":         true,
		".github/workflows/linked.yml":     true,
		"pkg/action.yml/inner.txt":         true,
		"pkg/deep/action.yaml":             true,
	} {
		changed := Listing{Entries: append([]ListedEntry(nil), listing.Entries...)}
		found := false
		for i, e := range changed.Entries {
			if e.Path == path {
				changed.Entries[i].SHA = gitBlob("something else")
				found = true
			}
		}
		if !found {
			t.Fatalf("%s is not in the listing, so the fixture does not exercise it", path)
		}
		if moved := ListingDigest(changed, sparsePatterns) != fromListing; moved != counted {
			t.Errorf("changing %s moved the digest: %v, want %v", path, moved, counted)
		}
	}
}

// A link and a file holding the link's target are different trees to the lock reader, which
// follows one and reads the other, and git gives both the same blob id.
func TestALinkAndAFileWithItsTargetDigestDifferently(t *testing.T) {
	file := Listing{Entries: []ListedEntry{
		{Path: ".github/workflows/ci.yml", Mode: "100644", Type: "blob", SHA: gitBlob("other.yml")},
	}}
	link := Listing{Entries: []ListedEntry{
		{Path: ".github/workflows/ci.yml", Mode: "120000", Type: "blob", SHA: gitBlob("other.yml")},
	}}
	if ListingDigest(file, sparsePatterns) == "" {
		t.Fatal("the file has no digest, so this proves nothing")
	}
	if ListingDigest(file, sparsePatterns) == ListingDigest(link, sparsePatterns) {
		t.Error("a link digested like a file holding its target")
	}
}

// Nothing to index is not a tree that matches every other empty one, from either reader.
func TestNothingIndexableHasNoDigestFromEitherReader(t *testing.T) {
	outside := map[string]string{"README.md": "# readme\n", "src/index.js": "1\n"}
	if got := ListingDigest(listingOf(outside), sparsePatterns); got != "" {
		t.Errorf("a listing of only excluded files digested to %q", got)
	}
	if got := ListingDigest(Listing{}, sparsePatterns); got != "" {
		t.Errorf("an empty listing digested to %q", got)
	}
	if got, err := CheckoutDigest(tree(t, outside), sparsePatterns); err != nil || got != "" {
		t.Errorf("a checkout of only excluded files digested to %q, %v", got, err)
	}
	if got, err := CheckoutDigest(t.TempDir(), sparsePatterns); err != nil || got != "" {
		t.Errorf("an empty checkout digested to %q, %v", got, err)
	}
}

// GitHub stops listing past a size and says so. The listed part says nothing about the rest, so
// a digest over it would call two different trees identical.
func TestATruncatedListingHasNoDigest(t *testing.T) {
	l := listingOf(map[string]string{".github/workflows/ci.yml": "on: push\n"})
	if ListingDigest(l, sparsePatterns) == "" {
		t.Fatal("the whole listing has no digest, so this proves nothing")
	}
	l.Truncated = true
	if got := ListingDigest(l, sparsePatterns); got != "" {
		t.Errorf("a truncated listing digested to %q", got)
	}
}

// A pattern the listing cannot evaluate the way git does is a filter it cannot apply, and a
// guess at it would count files the checkout never held.
func TestAFilterTheListingCannotEvaluateYieldsNoDigest(t *testing.T) {
	l := listingOf(map[string]string{".github/workflows/ci.yml": "on: push\n"})
	if ListingDigest(l, []string{"/.github/"}) == "" {
		t.Fatal("the literal filter has no digest, so this proves nothing")
	}
	for _, patterns := range [][]string{
		{"/.github/", "*.yml"},
		{"/.github/", "!/.github/workflows/"},
		{"/.github/", "action.y?ml"},
	} {
		if got := ListingDigest(l, patterns); got != "" {
			t.Errorf("patterns %v digested to %q", patterns, got)
		}
	}
}

// Both readers must answer for a fetch the production checkout made, not only for a tree this
// package wrote, or the agreement above is between two of this test's own assumptions.
func TestTheProductionCheckoutAgreesWithItsOwnCommitsListing(t *testing.T) {
	origin := originRepo(t, map[string]string{
		".github/workflows/ci.yml": "on: push\n",
		"action.yml":               "name: thing\n",
		"README.md":                "# readme\n",
	})
	c := newCheckout(t, origin)
	tree, err := c.Fetch(context.Background(), filepath.Join(t.TempDir(), "work"),
		"", filepath.Base(origin), "")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	fromCheckout, err := CheckoutDigest(tree.Dir, c.SparsePatterns())
	if err != nil {
		t.Fatalf("CheckoutDigest: %v", err)
	}
	if fromListing := ListingDigest(lsTree(t, origin), c.SparsePatterns()); fromCheckout != fromListing {
		t.Errorf("checkout %q, listing %q", fromCheckout, fromListing)
	}
}

// A statement narrower than git hides one file from both readers, which then agree and both omit
// it. Only the checkout can see that gap, and it refuses rather than describe a tree without a
// file the extractor reads.
func TestACheckoutHoldingAFileTheFilterDoesNotCountHasNoDigest(t *testing.T) {
	inside := map[string]string{".github/workflows/ci.yml": "on: push\n"}
	if got, err := CheckoutDigest(tree(t, inside), []string{"/.github/"}); err != nil || got == "" {
		t.Fatalf("the checkout inside its filter digested to %q, %v; this proves nothing", got, err)
	}
	inside["README.md"] = "# readme\n"
	if got, err := CheckoutDigest(tree(t, inside), []string{"/.github/"}); err != nil || got != "" {
		t.Errorf("a checkout holding a file its filter does not count digested to %q, %v", got, err)
	}
}
