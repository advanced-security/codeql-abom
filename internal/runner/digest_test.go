package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func writePack(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
}

func samplePack() map[string]string {
	return map[string]string{
		"codeql-pack.yml":                   "name: advanced-security/x\nversion: 2.26.4\n",
		"codeql-pack.lock.yml":              "dependencies:\n  codeql/actions-all:\n    version: 0.6.0\n",
		"AbomEdges.ql":                      "select 1\n",
		"AbomEdges.qll":                     "predicate p() { any() }\n",
		"codeql-suites/abom-graph.qls":      "- query: AbomEdges.ql\n",
		"test/AbomEdges/AbomEdges.expected": "|\n",
		"README.md":                         "notes\n",
	}
}

func digestOf(t *testing.T, dir string) string {
	t.Helper()
	d, err := PackDigest(dir)
	if err != nil {
		t.Fatalf("PackDigest: %v", err)
	}
	if d == "" {
		t.Fatal("PackDigest returned an empty digest")
	}
	return d
}

// The digest is the query identity, so it has to be stable: an unchanged pack that hashed
// differently between rounds would re-run the whole corpus every time.
func TestAPackThatHasNotChangedHashesTheSame(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, samplePack())

	first := digestOf(t, dir)
	second := digestOf(t, dir)

	if first != second {
		t.Errorf("the same pack hashed %s then %s", first, second)
	}
}

// The defect this exists to fix: a pack version pinned to the CodeQL CLI does not move when a
// query is edited, so the run key would not move and a correct skip would wrongly skip.
func TestEditingAQueryChangesTheDigest(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, samplePack())
	before := digestOf(t, dir)

	writePack(t, dir, map[string]string{"AbomEdges.ql": "select 2\n"})

	if digestOf(t, dir) == before {
		t.Error("editing a query left the digest unchanged")
	}
}

// Libraries decide what queries mean, and resolving transitive imports to hash only the ones
// actually used would be more precise and much more code. Whole-pack hashing over-invalidates
// on purpose.
func TestEditingALibraryChangesTheDigest(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, samplePack())
	before := digestOf(t, dir)

	writePack(t, dir, map[string]string{"AbomEdges.qll": "predicate p() { none() }\n"})

	if digestOf(t, dir) == before {
		t.Error("editing a library left the digest unchanged")
	}
}

// The lock file is the resolved dependency closure. A dependency bump can change what a query
// reports without touching a line of it here, so the digest has to move with it.
func TestBumpingAResolvedDependencyChangesTheDigest(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, samplePack())
	before := digestOf(t, dir)

	writePack(t, dir, map[string]string{
		"codeql-pack.lock.yml": "dependencies:\n  codeql/actions-all:\n    version: 0.6.1\n",
	})

	if digestOf(t, dir) == before {
		t.Error("a resolved dependency bump left the digest unchanged")
	}
}

// Documentation and expected-output files do not change what a query computes, and letting them
// invalidate would re-analyze the corpus for a typo fix.
func TestFilesThatCannotChangeResultsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	writePack(t, dir, samplePack())
	before := digestOf(t, dir)

	writePack(t, dir, map[string]string{
		"README.md":                         "rewritten\n",
		"test/AbomEdges/AbomEdges.expected": "different\n",
	})

	if digestOf(t, dir) != before {
		t.Error("an unrelated file changed the digest")
	}
}

// The digest travels with the pack, not with where a machine happens to keep it, or every
// checkout would invalidate every pair.
func TestTheDigestDoesNotDependOnWhereThePackLives(t *testing.T) {
	a, b := filepath.Join(t.TempDir(), "one"), filepath.Join(t.TempDir(), "two")
	writePack(t, a, samplePack())
	writePack(t, b, samplePack())

	if digestOf(t, a) != digestOf(t, b) {
		t.Error("the same pack in two places hashed differently")
	}
}

// A pack that is not there is a configuration error worth reporting, not an empty digest that
// would silently collapse every query's identity into one.
func TestAMissingPackIsAnError(t *testing.T) {
	if _, err := PackDigest(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a missing pack produced a digest")
	}
}
