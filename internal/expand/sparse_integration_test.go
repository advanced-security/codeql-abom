package expand

import (
	"archive/zip"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The sparse filter decides what an actions database is built from; the extractor decides what it
// indexes. If the filter is narrower than the extractor, the database is missing files and every
// query answers from an incomplete tree — silently, because a partial database is still a valid
// one. Only a database CodeQL actually built can say which paths those are, which is why this
// runs under `make itest` rather than as a unit test.
func TestTheSparseFilterCoversEverythingTheExtractorIndexes(t *testing.T) {
	db := os.Getenv("ABOM_ITEST_SPARSE_DB")
	if db == "" {
		t.Skip("set ABOM_ITEST_SPARSE_DB to a prebuilt actions database to run this test")
	}

	// Read as a rule, not restated: editing sparsePatterns without revisiting the reading below
	// would leave this test asserting something the code no longer says.
	if want := []string{"/.github/", "action.yml", "action.yaml"}; !slices.Equal(sparsePatterns, want) {
		t.Fatalf("sparsePatterns is %v; this test reads them as %v and must be updated with them",
			sparsePatterns, want)
	}

	indexed := indexedPaths(t, db)
	if len(indexed) == 0 {
		t.Fatal("the database indexed nothing, so this proves nothing about the filter")
	}
	// Without a path outside `.github/` this test cannot fail, and a check that cannot fail is
	// worse than none: narrowing the filter to `/.github/` passes against a source tree that has
	// no root manifest, which is how a scope of `.github/**` would look correct.
	exercises := slices.ContainsFunc(indexed, func(p string) bool {
		return !strings.HasPrefix(p, ".github/")
	})
	if !exercises {
		t.Fatalf("%s indexed only .github/ paths, so it cannot exercise the filter", db)
	}

	for _, p := range indexed {
		base := path.Base(p)
		if strings.HasPrefix(p, ".github/") || base == "action.yml" || base == "action.yaml" {
			continue
		}
		t.Errorf("the extractor indexed %q, which the sparse filter would never have fetched", p)
	}
}

// indexedPaths lists what the database's source archive holds, relative to the source root.
func indexedPaths(t *testing.T, db string) []string {
	t.Helper()

	manifest, err := os.ReadFile(filepath.Join(db, "codeql-database.yml"))
	if err != nil {
		t.Fatalf("reading the database manifest: %v", err)
	}
	var root string
	for _, line := range strings.Split(string(manifest), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "sourceLocationPrefix:"); ok {
			root = strings.TrimSpace(rest)
			break
		}
	}
	if root == "" {
		t.Fatal("the manifest names no sourceLocationPrefix, so entries cannot be made relative")
	}
	// Archive entries are the absolute source path without its leading separator.
	prefix := strings.TrimPrefix(root, "/") + "/"

	zr, err := zip.OpenReader(filepath.Join(db, "src.zip"))
	if err != nil {
		t.Fatalf("opening the source archive: %v", err)
	}
	defer func() { _ = zr.Close() }()

	var out []string
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rel, ok := strings.CutPrefix(f.Name, prefix)
		if !ok {
			continue
		}
		out = append(out, rel)
	}
	return out
}
