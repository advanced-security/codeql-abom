package authoring

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

func write(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func query(id string) string {
	return "/**\n * @name " + id + "\n * @kind problem\n * @problem.severity warning\n * @id " + id +
		"\n */\n\nimport actions\n\nfrom Job job\nselect job, \"a job\"\n"
}

// workspace is a scaffolded pack holding one query per id, each with a test directory of its own
// named after it.
func workspace(t *testing.T, names map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := customql.Scaffold(root, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	for name, id := range names {
		write(t, root, "src/"+name+".ql", query(id))
		write(t, root, "test/"+name+"/"+name+".qlref", "src/"+name+".ql\n")
		write(t, root, "test/"+name+"/"+name+".expected", "| a job |\n")
		write(t, root, "test/"+name+"/.github/workflows/ci.yml", "on: push\njobs:\n  a:\n    runs-on: x\n")
	}
	return root
}

// A catalog row, a request and a test run all name a query by its id, so the id is how the file
// on disk is found.
func TestAQueryIsFoundByTheIDItDeclares(t *testing.T) {
	root := workspace(t, map[string]string{"SelfHostedRunner": "acme/self-hosted-runner"})

	got, found, err := Find(root, "acme/self-hosted-runner")
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	path := filepath.Join(root, "src", "SelfHostedRunner.ql")
	version, err := runner.QueryVersion(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Query{
		ID: "acme/self-hosted-runner", Kind: "problem", Path: path, Rel: "src/SelfHostedRunner.ql",
		Version: version,
		Tests:   []string{filepath.Join(root, "test", "SelfHostedRunner", "SelfHostedRunner.qlref")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("found\n\t%+v\nwant\n\t%+v", got, want)
	}
}

// Find runs for every event an agent session records, under the lock that orders them, so it
// versions the query it finds and nothing else. Versioning the whole pack made each tool call wait
// on every query in it: 211ms a call at 300 queries.
func TestFindingAQueryVersionsThatQueryAlone(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a", "B": "acme/b", "C": "acme/c"})
	var versioned [][]string
	before := queryVersions
	queryVersions = func(paths []string) ([]string, error) {
		versioned = append(versioned, slices.Clone(paths))
		return before(paths)
	}
	t.Cleanup(func() { queryVersions = before })

	got, found, err := Find(root, "acme/b")
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	if len(versioned) != 1 || !reflect.DeepEqual(versioned[0], []string{got.Path}) {
		t.Errorf("versioned %v, want only %s", versioned, got.Path)
	}
}

// One query's failing test must not fail another: a test belongs to the query its .qlref names
// and to no other.
func TestATestBelongsOnlyToTheQueryItsQlrefNames(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a", "B": "acme/b"})

	queries, err := Workspace(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("listed %d queries, want 2: %+v", len(queries), queries)
	}
	for _, q := range queries {
		name := strings.TrimSuffix(filepath.Base(q.Rel), ".ql")
		want := []string{filepath.Join(root, "test", name, name+".qlref")}
		if !reflect.DeepEqual(q.Tests, want) {
			t.Errorf("%s is tested by %v, want %v", q.ID, q.Tests, want)
		}
	}
}

// The CLI runs every test in a directory it is handed, so two tests sharing one directory are
// still two queries' evidence and must be told apart by file.
func TestTwoTestsInOneDirectoryEachBelongToTheirOwnQuery(t *testing.T) {
	root := workspace(t, nil)
	for _, name := range []string{"A", "B"} {
		write(t, root, "src/"+name+".ql", query("acme/"+strings.ToLower(name)))
		write(t, root, "test/Both/"+name+".qlref", "src/"+name+".ql\n")
	}

	for _, name := range []string{"A", "B"} {
		got, _, err := Find(root, "acme/"+strings.ToLower(name))
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{filepath.Join(root, "test", "Both", name+".qlref")}; !reflect.DeepEqual(got.Tests, want) {
			t.Errorf("%s is tested by %v, want %v", name, got.Tests, want)
		}
	}
}

// Nothing testing a query is an answer to report, not a reason to leave the query out.
func TestAQueryNothingTestsIsListedWithNoTests(t *testing.T) {
	root := workspace(t, nil)
	write(t, root, "src/Untested.ql", query("acme/untested"))

	got, found, err := Find(root, "acme/untested")
	if err != nil || !found {
		t.Fatalf("Find: found=%v err=%v", found, err)
	}
	if got.Tests == nil || len(got.Tests) != 0 {
		t.Errorf("tested by %#v, want an empty list", got.Tests)
	}
}

// The CLI also reads a .qlref written as YAML, which is how a test with inline expectations names
// its query; a test the CLI runs and this cannot see would grade the query on nothing.
func TestAQlrefWrittenAsYAMLStillNamesItsQuery(t *testing.T) {
	root := workspace(t, nil)
	write(t, root, "src/A.ql", query("acme/a"))
	write(t, root, "test/A/A.qlref",
		"query: src/A.ql\npostprocess: utils/test/InlineExpectationsTestQuery.ql\n")

	got, _, err := Find(root, "acme/a")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(root, "test", "A", "A.qlref")}; !reflect.DeepEqual(got.Tests, want) {
		t.Errorf("tested by %v, want %v", got.Tests, want)
	}
}

// Two files declaring one id leave nothing to say which of them a result is about.
func TestTwoQueriesDeclaringOneIDAreRefused(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/same", "B": "acme/same"})

	if _, _, err := Find(root, "acme/same"); err == nil {
		t.Error("an id two files declare resolved to one of them")
	}
}

func TestAnIDNoQueryDeclaresIsNotFound(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})

	if _, found, err := Find(root, "acme/elsewhere"); err != nil || found {
		t.Errorf("found=%v err=%v for an id nothing declares", found, err)
	}
}

// A run writes its output and keeps its database beside the test, and neither is part of what
// the test says.
func TestTheTestDigestIgnoresWhatARunLeavesBehind(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	tests := []string{filepath.Join(root, "test", "A", "A.qlref")}
	before, err := DigestTests(root, tests)
	if err != nil {
		t.Fatal(err)
	}

	write(t, root, "test/A/A.actual", "| something else |\n")
	write(t, root, "test/A/A.testproj/codeql-database.yml", finishedLine+"\n")
	after, err := DigestTests(root, tests)
	if err != nil {
		t.Fatal(err)
	}
	if before == "" || before != after {
		t.Errorf("digest %q became %q after a run", before, after)
	}
}

// A pass recorded before the .expected or the test code changed describes tests that no longer
// exist, and the digest is how that reads as stale.
func TestTheTestDigestMovesWhenTheTestsChange(t *testing.T) {
	for _, rel := range []string{"test/A/A.expected", "test/A/.github/workflows/ci.yml"} {
		root := workspace(t, map[string]string{"A": "acme/a"})
		tests := []string{filepath.Join(root, "test", "A", "A.qlref")}
		before, err := DigestTests(root, tests)
		if err != nil {
			t.Fatal(err)
		}

		write(t, root, rel, "changed\n")
		after, err := DigestTests(root, tests)
		if err != nil {
			t.Fatal(err)
		}
		if before == after {
			t.Errorf("editing %s left the digest at %q", rel, before)
		}
	}
}
