package expand

import (
	"os"
	"path/filepath"
	"testing"
)

// The digest decides whether an analysis is skipped, so anything that varies between two clones
// of one commit makes it unique and the check inert — silently, because an inert check looks
// exactly like a correct one that never matches. `.git` holds packfiles that differ per clone.
func TestTwoCheckoutsOfTheSameContentDigestTheSame(t *testing.T) {
	first := tree(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\n",
		"action.yml":               "name: thing\n",
		".git/objects/pack/a.pack": "one clone's packfile",
	})
	second := tree(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\n",
		"action.yml":               "name: thing\n",
		".git/objects/pack/b.pack": "another clone's packfile",
	})

	if digestOf(t, first) != digestOf(t, second) {
		t.Error("two clones of one commit disagreed, so the content check can never match")
	}
}

// The other half: content the extractor reads must move the digest, or a changed workflow would
// be waived by a run that never saw it.
func TestChangedWorkflowContentMovesTheDigest(t *testing.T) {
	before := tree(t, map[string]string{".github/workflows/ci.yml": "name: ci\n"})
	after := tree(t, map[string]string{
		".github/workflows/ci.yml": "name: ci\non: pull_request\n",
	})

	if digestOf(t, before) == digestOf(t, after) {
		t.Error("a changed workflow left the digest alone")
	}
}

// Two files swapping names is the same bytes in a different tree, and the extractor reads paths.
func TestMovingContentBetweenPathsMovesTheDigest(t *testing.T) {
	before := tree(t, map[string]string{
		".github/workflows/a.yml": "name: one\n",
		".github/workflows/b.yml": "name: two\n",
	})
	after := tree(t, map[string]string{
		".github/workflows/a.yml": "name: two\n",
		".github/workflows/b.yml": "name: one\n",
	})

	if digestOf(t, before) == digestOf(t, after) {
		t.Error("swapping two files' contents left the digest alone")
	}
}

// A checkout with nothing in it is a fetch that produced nothing, not a tree that matches every
// other empty tree. Reporting no digest keeps it from matching anything at all.
func TestAnEmptyCheckoutHasNoDigest(t *testing.T) {
	if got := digestOf(t, tree(t, nil)); got != "" {
		t.Errorf("an empty checkout digested to %q, which would match every other empty one", got)
	}
}

func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}
	return dir
}

func digestOf(t *testing.T, dir string) string {
	t.Helper()
	got, err := CheckoutDigest(dir, sparsePatterns)
	if err != nil {
		t.Fatalf("CheckoutDigest: %v", err)
	}
	return got
}

// A commit that changes only its lock changes which commits its workflows run, with every
// `uses:` as it was. Content-equivalence must not waive it, or the new selections would never be
// read: the digest has to cover the lock even though the extractor does not.
func TestALockOnlyChangeMovesTheDigest(t *testing.T) {
	workflow := "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n"
	before := tree(t, map[string]string{
		".github/workflows/ci.yml":       workflow,
		".github/workflows/actions.lock": "version: 'v0.0.2'\n# sha1-11bd71901bbe5b1630ceea73d27597364c9af683\n",
	})
	after := tree(t, map[string]string{
		".github/workflows/ci.yml":       workflow,
		".github/workflows/actions.lock": "version: 'v0.0.2'\n# sha1-11d5960a326750d5838078e36cf38b85af677262\n",
	})

	if digestOf(t, before) == digestOf(t, after) {
		t.Error("a lock-only commit digests like its predecessor, so its selections would be skipped")
	}
}
