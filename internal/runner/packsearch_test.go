package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A query the operator wrote has to be resolvable by name, and a pack that has never been
// published is not in the package cache. Without its directory on the search path the CLI reports
// it as misspelled rather than as local.
func TestTheOperatorsPackIsOnTheSearchPath(t *testing.T) {
	dir := t.TempDir()
	withCustomRoot(t, dir)

	args := localPackArgs()
	if len(args) != 2 || args[0] != "--additional-packs" {
		t.Fatalf("localPackArgs returned %v, want a search path", args)
	}
	if !strings.Contains(args[1], dir) {
		t.Errorf("the search path %q does not include the operator's directory", args[1])
	}
}

// Naming a directory must not break the crawl before anything is written in it: it is created
// when the operator first writes a query.
func TestADirectoryThatIsNotThereYetIsSkippedRatherThanRefused(t *testing.T) {
	withCustomRoot(t, filepath.Join(t.TempDir(), "not-created-yet"))

	for _, arg := range localPackArgs() {
		if strings.Contains(arg, "not-created-yet") {
			t.Errorf("a directory that does not exist reached the search path: %q", arg)
		}
	}
}

// The path is absolute because the CLI is invoked from directories this process does not choose,
// and a relative entry would resolve against whichever one that happened to be.
func TestTheSearchPathIsAbsolute(t *testing.T) {
	dir := t.TempDir()
	withCustomRoot(t, dir)

	for _, entry := range strings.Split(localPackArgs()[1], string(os.PathListSeparator)) {
		if !filepath.IsAbs(entry) {
			t.Errorf("search path entry %q is relative", entry)
		}
	}
}

// With nothing configured the behavior is what it was: this repository's packs alone.
func TestWithNoCustomDirectoryOnlyThisRepositorysPacksAreSearched(t *testing.T) {
	withCustomRoot(t, "")

	args := localPackArgs()
	if len(args) == 0 {
		// `ql/` is there when the tests run from the repository root; if it is not, there is
		// nothing to assert and nothing is broken.
		return
	}
	if strings.Contains(args[1], string(os.PathListSeparator)) {
		t.Errorf("the search path holds more than this repository's packs: %q", args[1])
	}
}

// A pack an operator could publish writes `qlpack.yml`; this repository's own write
// `codeql-pack.yml`. `PackOf` has always read both, and a suite lister that reads only one makes
// a deployed query resolvable by the CLI and invisible to the control that would bind it.
func TestASuiteIsFoundUnderEitherManifestSpelling(t *testing.T) {
	for _, manifest := range []string{"codeql-pack.yml", "qlpack.yml"} {
		t.Run(manifest, func(t *testing.T) {
			dir := t.TempDir()
			write(t, filepath.Join(dir, manifest),
				"name: acme/pack\nversion: 1.0.0\nlibrary: false\nsuites: codeql-suites\n")
			write(t, filepath.Join(dir, "codeql-suites", "deployed.qls"), "- queries: .\n")

			suites, err := AvailableSuites(dir)
			if err != nil {
				t.Fatalf("AvailableSuites: %v", err)
			}
			want := "acme/pack:codeql-suites/deployed.qls"
			if !slices.Contains(suites, want) {
				t.Errorf("a pack declaring %s offers %v, want %q", manifest, suites, want)
			}
		})
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func withCustomRoot(t *testing.T, dir string) {
	t.Helper()
	before := CustomPackRoot()
	SetCustomPackRoot(dir)
	t.Cleanup(func() { SetCustomPackRoot(before) })
}
