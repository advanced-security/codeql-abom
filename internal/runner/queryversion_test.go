package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// versionPack lays out a pack and returns its directory. It reuses digest_test.go's writer, so
// one fixture convention covers both content hashes in this package.
func versionPack(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	files["codeql-pack.yml"] = "name: advanced-security/test-pack\nversion: 1.0.0\n"
	writePack(t, dir, files)
	return dir
}

func versionOf(t *testing.T, dir, query string) string {
	t.Helper()
	v, err := QueryVersion(filepath.Join(dir, query))
	if err != nil {
		t.Fatalf("QueryVersion(%s): %v", query, err)
	}
	if v == "" {
		t.Fatalf("QueryVersion(%s) is empty", query)
	}
	return v
}

// The version is what a catalog tracks a query by, so the same source has to produce the same
// answer every time. A version that moved on its own would make every run look like a new one.
func TestAQueryVersionIsTheSameOnASecondReading(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":  "class A extends int {}\n",
	})
	if first, second := versionOf(t, dir, "Edges.ql"), versionOf(t, dir, "Edges.ql"); first != second {
		t.Errorf("one query read twice versioned %s then %s", first, second)
	}
}

// Editing the query is the obvious half.
func TestAQueryVersionMovesWhenTheQueryItselfChanges(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":  "class A extends int {}\n",
	})
	before := versionOf(t, dir, "Edges.ql")
	if err := os.WriteFile(filepath.Join(dir, "Edges.ql"),
		[]byte("import Lib\nfrom int i where i > 0 select i\n"), 0o600); err != nil {
		t.Fatalf("editing the query: %v", err)
	}
	if after := versionOf(t, dir, "Edges.ql"); after == before {
		t.Error("the query was edited and its version did not move")
	}
}

// The half that matters. A library edit changes what the query *means* while leaving the query
// file untouched, so a version reading only the query would report one identity for two queries
// that answer differently.
func TestAQueryVersionMovesWhenALibraryItImportsChanges(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":  "class A extends int {}\n",
	})
	before := versionOf(t, dir, "Edges.ql")
	if err := os.WriteFile(filepath.Join(dir, "Lib.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the library: %v", err)
	}
	if after := versionOf(t, dir, "Edges.ql"); after == before {
		t.Error("an imported library was edited and the query's version did not move")
	}
}

// Transitively, or a shared base library could change under a query that imports it two hops away
// and nothing would say so.
func TestAQueryVersionMovesWhenALibraryTwoHopsAwayChanges(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql": "import Mid\nfrom int i select i\n",
		"Mid.qll":  "import Base\n",
		"Base.qll": "class A extends int {}\n",
	})
	before := versionOf(t, dir, "Edges.ql")
	if err := os.WriteFile(filepath.Join(dir, "Base.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the base library: %v", err)
	}
	if after := versionOf(t, dir, "Edges.ql"); after == before {
		t.Error("a library two hops away was edited and the query's version did not move")
	}
}

// Every dependent query moves, which is what makes "re-run what this edit affects" answerable.
func TestEveryQueryImportingALibraryMovesWhenItChanges(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"One.ql":  "import Lib\nfrom int i select i\n",
		"Two.ql":  "import Lib\nfrom int j select j\n",
		"Lib.qll": "class A extends int {}\n",
	})
	before := map[string]string{
		"One.ql": versionOf(t, dir, "One.ql"),
		"Two.ql": versionOf(t, dir, "Two.ql"),
	}
	if err := os.WriteFile(filepath.Join(dir, "Lib.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the library: %v", err)
	}
	for _, q := range []string{"One.ql", "Two.ql"} {
		if versionOf(t, dir, q) == before[q] {
			t.Errorf("%s imports the edited library and its version did not move", q)
		}
	}
}

// A version that moved for any edit anywhere in the pack would re-run every query whenever one
// was touched, which is the cost this exists to let somebody avoid.
func TestAQueryVersionIsUnmovedByAFileItDoesNotImport(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql":     "import Lib\nfrom int i select i\n",
		"Lib.qll":      "class A extends int {}\n",
		"Unrelated.ql": "from int i select i\n",
		"Other.qll":    "class B extends int {}\n",
	})
	before := versionOf(t, dir, "Edges.ql")
	if err := os.WriteFile(filepath.Join(dir, "Other.qll"),
		[]byte("class B extends int { B() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the unrelated library: %v", err)
	}
	if after := versionOf(t, dir, "Edges.ql"); after != before {
		t.Error("a library the query does not import moved its version")
	}
}

// Libraries import each other. Following the closure without tracking what has been seen would
// not return.
func TestAnImportCycleTerminates(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql": "import A\nfrom int i select i\n",
		"A.qll":    "import B\n",
		"B.qll":    "import A\n",
	})
	// Off the test goroutine, so a closure that does not terminate fails here rather than hanging
	// until the whole package times out.
	done := make(chan error, 1)
	go func() {
		_, err := QueryVersion(filepath.Join(dir, "Edges.ql"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("QueryVersion over a cycle: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a cycle of imports did not terminate")
	}
}

// An import this cannot resolve is named rather than skipped. Skipping it would make the version
// claim the query depends on nothing it could not find, which is absent-is-not-zero arriving
// inside a content hash.
func TestAnUnresolvableImportStillContributesToTheVersion(t *testing.T) {
	before := versionOf(t, versionPack(t, map[string]string{
		"Edges.ql": "import actions\nfrom int i select i\n",
	}), "Edges.ql")
	after := versionOf(t, versionPack(t, map[string]string{
		"Edges.ql": "import somethingElse\nfrom int i select i\n",
	}), "Edges.ql")
	if before == after {
		t.Error("two queries importing different unresolvable modules share a version")
	}
}

// ABOM's own layout, which the pack-local fixtures above do not cover: the queries live in
// `ql/actions/graph-extraction/src` and the libraries they import live in a sibling pack at
// `ql/actions/lib`. Resolving only within the query's own pack would record `abom.actions.Edges`
// as a name and never notice it changing -- so every claim about re-running what an edit affects
// would be false for exactly the queries this repository ships.
func TestALibraryInASiblingPackOfTheWorkspaceStillMovesTheVersion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "codeql-workspace.yml"),
		[]byte("provide:\n  - \"*/codeql-pack.yml\"\n"), 0o600); err != nil {
		t.Fatalf("writing the workspace: %v", err)
	}
	queries := filepath.Join(root, "queries")
	lib := filepath.Join(root, "lib")
	writePack(t, queries, map[string]string{
		"codeql-pack.yml": "name: advanced-security/test-queries\nversion: 1.0.0\n",
		"Edges.ql":        "import abom.actions.Edges\nfrom int i select i\n",
	})
	writePack(t, lib, map[string]string{
		"codeql-pack.yml":        "name: advanced-security/test-lib\nversion: 1.0.0\n",
		"abom/actions/Edges.qll": "class A extends int {}\n",
	})

	before := versionOf(t, queries, "Edges.ql")
	if err := os.WriteFile(filepath.Join(lib, "abom", "actions", "Edges.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the sibling library: %v", err)
	}
	if after := versionOf(t, queries, "Edges.ql"); after == before {
		t.Error("a library in a sibling pack moved and the query's version did not")
	}
}

// A dotted module name is a path. Resolving it as a flat filename would miss the library and
// quietly produce a version that never moves when it changes.
func TestADottedImportResolvesThroughDirectories(t *testing.T) {
	dir := versionPack(t, map[string]string{
		"Edges.ql":       "import abom.Edges\nfrom int i select i\n",
		"abom/Edges.qll": "class A extends int {}\n",
	})
	before := versionOf(t, dir, "Edges.ql")
	if err := os.WriteFile(filepath.Join(dir, "abom", "Edges.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o600); err != nil {
		t.Fatalf("editing the nested library: %v", err)
	}
	if after := versionOf(t, dir, "Edges.ql"); after == before {
		t.Error("a library reached by a dotted import moved and the version did not")
	}
}
