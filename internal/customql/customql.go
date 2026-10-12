// Package customql owns the directory an operator writes their own CodeQL queries in.
//
// A real pack on disk rather than rows in `abom.db`: that is what lets the same pack be tested,
// versioned and published independently of ABOM, which promotion depends on and which a query
// stored as a database row could never support.
package customql

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// ActionsAll is the codeql/actions-all both scaffolded packs pin: the version ABOM's own queries
// and ql-mcp's tool queries resolve, so a query is written and run against one library.
const ActionsAll = "0.6.3"

// Postprocess is the query, relative to the test pack, that reads a fixture's `# $ Alert` markers.
const Postprocess = "utils/InlineExpectationsTestQuery.ql"

// Scaffold creates the pack if it is not already there, and reports whether it wrote anything.
//
// Idempotent, and it never overwrites: the operator's queries and dependencies live here.
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
		"qlpack.yml":          manifestFor(cliVersion),
		"test/qlpack.yml":     testManifestFor(cliVersion),
		"test/" + Postprocess: postprocessQuery,
		SuitePath:             suiteFile,
		"src/README.md":       srcReadme,
		"test/README.md":      testReadme,
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return false, fmt.Errorf("custom queries: preparing %s: %w", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return false, fmt.Errorf("custom queries: writing %s: %w", name, err)
		}
	}
	return true, nil
}

// queryName is a plain file stem: it becomes src/<name>.ql and test/<name>/, and nothing else.
var queryName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// ScaffoldTest lays out the test of src/<name>.ql, where its fixtures go and a .qlref naming it,
// and returns the test directory. It never overwrites: a .qlref already there says what the test is
// of, and a postprocess query already there is the operator's.
func ScaffoldTest(dir, name string) (string, error) {
	if !queryName.MatchString(name) {
		return "", fmt.Errorf("custom queries: %q is not a query file name", name)
	}
	if _, err := os.Stat(filepath.Join(dir, "qlpack.yml")); err != nil {
		return "", fmt.Errorf("custom queries: %s holds no pack: %w", dir, err)
	}
	if err := createOnce(filepath.Join(dir, "test", filepath.FromSlash(Postprocess)), postprocessQuery); err != nil {
		return "", fmt.Errorf("custom queries: supplying the postprocess query: %w", err)
	}
	tests := filepath.Join(dir, "test", name)
	if err := os.MkdirAll(filepath.Join(tests, ".github", "workflows"), 0o755); err != nil {
		return "", fmt.Errorf("custom queries: preparing the test of %s: %w", name, err)
	}
	qlref := fmt.Sprintf("query: src/%s.ql\npostprocess: %s\n", name, Postprocess)
	if err := createOnce(filepath.Join(tests, name+".qlref"), qlref); err != nil {
		return "", fmt.Errorf("custom queries: naming the query of %s: %w", name, err)
	}
	return tests, nil
}

// createOnce writes a file that is not there yet, leaving one that is.
func createOnce(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// ValidQueryName reports whether name is a plain file stem a query can be laid out under.
func ValidQueryName(name string) bool { return queryName.MatchString(name) }

// queryID is an @id as a query declares it: lower-case segments, owner first.
var queryID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(/[a-z0-9][a-z0-9-]*)+$`)

// ValidQueryID reports whether id can be declared by a query ABOM lays out.
func ValidQueryID(id string) bool { return queryID.MatchString(id) }

// Requested is what a request asks a new query to declare, and the help written beside it. An empty
// field asks for nothing, and the stub says what to write there instead.
type Requested struct {
	Title            string
	Description      string
	Precision        string
	ProblemSeverity  string
	SecuritySeverity *float64
	Tags             []string
	Recommendation   string
	References       []string
}

// qldocTag is a tag a QLDoc can list: no space, which would split it in two.
var qldocTag = regexp.MustCompile(`^[a-z0-9][a-z0-9._/+-]*$`)

// check refuses a value that would close the QLDoc, or begin a line that is not its own.
func (r Requested) check() error {
	for _, v := range []string{r.Title, r.Precision, r.ProblemSeverity} {
		if strings.Contains(v, "*/") || strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("custom queries: %q cannot be one line of a QLDoc", v)
		}
	}
	if strings.Contains(r.Description, "*/") {
		return errors.New("custom queries: a @description cannot hold */")
	}
	for _, tag := range r.Tags {
		if !qldocTag.MatchString(tag) {
			return fmt.Errorf("custom queries: %q is not a tag", tag)
		}
	}
	return nil
}

// ScaffoldQuery lays out a new query declaring id and what want asks of it, which reports nothing
// until somebody writes it, its help and its test. It refuses a query file that is already there
// rather than overwrite it.
func ScaffoldQuery(dir, name, id string, request int64, want Requested) (string, string, error) {
	if !ValidQueryID(id) {
		return "", "", fmt.Errorf("custom queries: %q is not a query id such as acme/checkout-tag-pin", id)
	}
	if err := want.check(); err != nil {
		return "", "", err
	}
	tests, err := ScaffoldTest(dir, name)
	if err != nil {
		return "", "", err
	}
	query := filepath.Join(dir, "src", name+".ql")
	f, err := os.OpenFile(query, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", "", fmt.Errorf("custom queries: laying out src/%s.ql: %w", name, err)
	}
	if _, err := f.WriteString(stubQuery(name, id, request, want)); err != nil {
		return "", "", fmt.Errorf("custom queries: laying out src/%s.ql: %w", name, errors.Join(err, f.Close()))
	}
	if err := f.Close(); err != nil {
		return "", "", err
	}
	if err := createOnce(filepath.Join(dir, "src", name+".md"), stubHelp(name, want)); err != nil {
		return "", "", fmt.Errorf("custom queries: laying out src/%s.md: %w", name, err)
	}
	return query, tests, nil
}

// stubQuery compiles and reports nothing, which is a failing test until the query is written.
func stubQuery(name, id string, request int64, want Requested) string {
	title := cmp.Or(want.Title, name)
	description := cmp.Or(want.Description, fmt.Sprintf("Asked for in ABOM request %d; say here what "+
		"this query reports and why it matters.", request))
	var out strings.Builder
	fmt.Fprintf(&out, "/**\n * @name %s\n * @description %s\n * @kind problem\n", title,
		strings.Join(strings.Split(strings.TrimSpace(description), "\n"), "\n *              "))
	fmt.Fprintf(&out, " * @problem.severity %s\n", cmp.Or(want.ProblemSeverity, "warning"))
	if s := want.SecuritySeverity; s != nil {
		fmt.Fprintf(&out, " * @security-severity %.1f\n", *s)
	}
	if want.Precision != "" {
		fmt.Fprintf(&out, " * @precision %s\n", want.Precision)
	}
	fmt.Fprintf(&out, " * @id %s\n", id)
	if len(want.Tags) > 0 {
		fmt.Fprintf(&out, " * @tags %s\n", strings.Join(want.Tags, "\n *       "))
	}
	out.WriteString(" */\n\nimport actions\n\nfrom Step step\nwhere none()\nselect step, \"Not written yet.\"\n")
	return out.String()
}

// HelpUnwritten marks a section of laid-out help that nobody has written, so help holding it is not
// read as written.
const HelpUnwritten = "<!-- ABOM: not written yet -->"

// stubHelp is the help a request already says, with its sections in the order CodeQL's own read.
func stubHelp(name string, want Requested) string {
	section := func(text string) string { return cmp.Or(strings.TrimSpace(text), HelpUnwritten) }
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n\n## Overview\n\n%s\n\n## Recommendation\n\n%s\n\n## Example\n\n%s\n",
		cmp.Or(want.Title, name), section(want.Description), section(want.Recommendation), HelpUnwritten)
	if len(want.References) > 0 {
		out.WriteString("\n## References\n\n")
		for _, ref := range want.References {
			fmt.Fprintf(&out, "- %s\n", ref)
		}
	}
	return out.String()
}

// manifestFor pins the pack to the CodeQL the rest of ABOM runs, and to its actions library.
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
  codeql/actions-all: %s
suites: codeql-suites
`, Pack, version, ActionsAll)
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
  codeql/actions-all: %s
`, TestPack, version, Pack, ActionsAll)
}

// postprocessQuery reads a fixture's `# $ Alert` comments as expectations of the query under test:
// a marked line it does not report is a missing result, and one it reports unmarked is unexpected.
// The actions pack ships no such query; this wires its YAML comments to codeql/util's framework.
const postprocessQuery = `/**
 * @kind test-postprocess
 */

private import codeql.Locations as Locations
private import codeql.actions.ast.internal.Yaml as Yaml
private import codeql.util.test.InlineExpectationsTest as T
import T::TestPostProcessing

private module Impl implements T::InlineExpectationsTestSig {
  class Location = Locations::Location;

  string getRelativeUrl(Locations::Location location) {
    exists(int startLine, int startColumn, int endLine, int endColumn |
      location.hasLocationInfo(_, startLine, startColumn, endLine, endColumn)
    |
      result =
        location.getFile().getRelativePath() + ":" + startLine + ":" + startColumn + ":" + endLine +
          ":" + endColumn
    )
  }

  class ExpectationComment extends Yaml::YamlComment {
    string getContents() { result = this.getText() }
  }
}

import T::TestPostProcessing::Make<Impl>
`

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

Mark each line the query must report with a ` + "`# $ Alert`" + ` comment, and leave the lines it must
not report unmarked. The ` + "`.qlref`" + ` names ` + "`" + Postprocess + "`" + ` as its postprocess, which
reads the marks: a marked line the query does not report, or an unmarked one it does, is a failure
the ` + "`.expected`" + ` shows under ` + "`testFailures`" + `. The mark goes on the line the result's location
ends on, so report the value on that line rather than the step or job around it.

Write the ` + "`.expected`" + ` before the query: one row per marked line, with the location, the
element and the message the query's help says it reports. It states what the query must produce,
so it never comes from the query's own output, and one that lists ` + "`testFailures`" + ` passes
without the query doing what its marks say.

Run the tests before the query does anything. They fail because the marked results are missing,
and ABOM records that failure as the evidence the test was written first.

ABOM runs ` + "`codeql test run`" + ` over this directory to decide whether a version may leave the
` + "`dev`" + ` stage.
`
