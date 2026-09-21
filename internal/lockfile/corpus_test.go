package lockfile

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot walks up from the test's working directory to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("locating the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory, so the repository cannot be located")
		}
		dir = parent
	}
}

// The pinned parser has to accept the lock this repository's own pinned CLI writes. If a version
// of one stops reading the other, this is where that shows, rather than in a crawl that quietly
// records every workflow as unsupported.
func TestThePinnedParserReadsThisRepositorysOwnLockfile(t *testing.T) {
	got, err := Read(repoRoot(t))
	if err != nil {
		t.Fatalf("reading this repository's lockfile: %v", err)
	}
	if got.Coverage != Covered {
		t.Fatalf("coverage = %q (%s), want %q", got.Coverage, got.Reason, Covered)
	}
	if got.Schema != "v0.0.2" {
		t.Errorf("schema = %q, want v0.0.2; the compatibility baseline moved", got.Schema)
	}
	// A workflow whose only lockable reference is reached through a composite action still has to
	// resolve, because the lockfile records the transitive closure per workflow.
	if _, ok := got.Selected(".github/workflows/self-audit.yml", "actions/checkout@v7.0.1"); !ok {
		t.Error("no selection for a reference this repository's lockfile covers")
	}
}

// The corpus fixture is deliberately broken, and the classification it earns is the one that keeps
// a bad lock from reaching live-tag resolution. Vendored from upstream's CWE-829-Lockfile test.
func TestTheCorpusFixtureIsClassifiedUnsupportedRatherThanCovered(t *testing.T) {
	root := filepath.Join(repoRoot(t), "testdata", "corpus", "globex-industries", "dependency-lock")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the locked corpus fixture is missing, so this proves nothing: %v", err)
	}
	got, err := Read(root)
	if err != nil {
		t.Fatalf("reading the corpus fixture: %v", err)
	}
	if got.Coverage != Unsupported {
		t.Fatalf("coverage = %q, want %q", got.Coverage, Unsupported)
	}
	if got.Schema != "future-version" {
		t.Errorf("schema = %q, want %q as written", got.Schema, "future-version")
	}
	if _, ok := got.Selected(".github/workflows/rust-ci.yml", "missing/action@v1"); ok {
		t.Error("an unsupported lockfile reported a selection")
	}
}
