// Package customql owns the directory an operator writes their own CodeQL queries in.
//
// A real pack on disk rather than rows in `abom.db`: that is what lets the same pack be tested,
// versioned and published independently of ABOM, which promotion depends on and which a query
// stored as a database row could never support.
package customql

import (
	"fmt"
	"os"
	"path/filepath"
)

// Pack is the name the operator's pack carries.
//
// Named apart from everything under `ql/` deliberately: the CLI refuses a pack name found twice
// on one search path, and both directories are on it.
const Pack = "abom/custom-queries"

// Suite is the suite reference a tier binds, and the one an on-demand run resolves ids from.
const Suite = Pack + ":codeql-suites/abom-custom.qls"

// SuitePath is where that suite lives inside the pack.
const SuitePath = "codeql-suites/abom-custom.qls"

// Scaffold creates the pack if it is not already there, and reports whether it wrote anything.
//
// Idempotent, and it never overwrites: the operator's queries live here, and a scaffold that
// rewrote a manifest would silently undo a dependency they added.
func Scaffold(dir, cliVersion string) (bool, error) {
	if dir == "" {
		return false, fmt.Errorf("custom queries: no directory named")
	}
	manifest := filepath.Join(dir, "qlpack.yml")
	if _, err := os.Stat(manifest); err == nil {
		return false, nil
	}
	// `codeql-pack.yml` is what a repository pack uses and `qlpack.yml` what a registry-installed
	// one does; the CLI reads either. This writes the second so the directory looks like
	// something the operator could publish rather than part of this repository.
	for _, sub := range []string{"src", filepath.Join("test", "queries")} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return false, fmt.Errorf("custom queries: preparing %s: %w", sub, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "codeql-suites"), 0o755); err != nil {
		return false, fmt.Errorf("custom queries: preparing the suite directory: %w", err)
	}

	files := map[string]string{
		"qlpack.yml":      manifestFor(cliVersion),
		"test/qlpack.yml": testManifestFor(cliVersion),
		SuitePath:         suiteFile,
		"src/README.md":   srcReadme,
		"test/README.md":  testReadme,
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return false, fmt.Errorf("custom queries: writing %s: %w", name, err)
		}
	}
	return true, nil
}

// manifestFor pins the pack to the CodeQL the rest of ABOM runs.
//
// The authoring environment is part of a query's provenance: a query unit-tested against one
// version of the `actions` library and executed under another can behave differently, and nothing
// downstream would say which one produced a result.
func manifestFor(cliVersion string) string {
	version := cliVersion
	if version == "" {
		version = "0.0.1"
	}
	return fmt.Sprintf(`name: %s
version: %s
library: false
dependencies:
  codeql/actions-all: "*"
suites: codeql-suites
`, Pack, version)
}

// TestPack is the pack the query tests live in, which is a separate pack from the queries.
const TestPack = Pack + "-tests"

// testManifestFor declares the extractor, without which a test cannot build its database.
//
// `codeql test run` refuses a test pack that does not name one — "does not declare which extractor
// to use" — and the failure reads as a broken query rather than a missing line. Scaffolding it is
// the difference between a directory an operator can write a test in and one they have to debug
// first.
func testManifestFor(cliVersion string) string {
	version := cliVersion
	if version == "" {
		version = "0.0.1"
	}
	return fmt.Sprintf(`name: %s
version: %s
library: false
extractor: actions
dependencies:
  %s: "*"
  codeql/actions-all: "*"
`, TestPack, version, Pack)
}

// A suite that selects the whole pack, so a query added to `src/` runs without anybody editing
// this file. A suite listing queries by name is one more thing to keep in step.
const suiteFile = `- queries: .
  from: abom/custom-queries
`

const srcReadme = `# Your queries

Every ` + "`.ql`" + ` here is run by the ` + "`abom-custom`" + ` suite, so adding one is enough — no
suite edit, and no ABOM change.

A query needs ` + "`@kind problem`" + ` or ` + "`@kind path-problem`" + ` metadata and an ` + "`@id`" + `.
ABOM reads the kind to decide how to run it, and the id is what the catalog, a filter and a policy
all name it by. A query whose kind ABOM cannot recognize is refused with its reason rather than run
and guessed at.

Write a test beside it under ` + "`../test/`" + ` before the query does what you want. A query written
before its negative case is a query nobody can tell is too broad.
`

const testReadme = `# Your tests

One directory per query, holding the fixture files, a ` + "`.qlref`" + ` naming the query under
` + "`src/`" + `, and the ` + "`.expected`" + ` output.

The fixture comes from the CLI at every step: build the tree, run the query, check in what the CLI
printed. A hand-written ` + "`.expected`" + ` agrees with whatever its author expected and proves
nothing.

ABOM runs ` + "`codeql test run`" + ` over this directory to decide whether a version may leave the
` + "`dev`" + ` stage.
`
