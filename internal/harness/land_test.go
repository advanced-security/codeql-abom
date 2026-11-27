package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
)

func sha(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func queryText(name, id string) string {
	return "/**\n * @name " + name + "\n * @kind problem\n * @id " + id + "\n */\nimport actions\n\n" +
		"from Job j select j, \"x\"\n"
}

// landPack is a coordinator's pack: two queries, each with its own test and a fixture.
func landPack(t *testing.T) string {
	t.Helper()
	ws := filepath.Join(t.TempDir(), "pack")
	if _, err := customql.Scaffold(ws, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"SelfHostedRunner": "acme/self-hosted-runner", "Other": "acme/other"} {
		put(t, filepath.Join(ws, "src", name+".ql"), queryText(name, id))
		if _, err := customql.ScaffoldTest(ws, name); err != nil {
			t.Fatal(err)
		}
		put(t, filepath.Join(ws, "test", name, ".github", "workflows", "old.yml"), "on: push\n")
	}
	return ws
}

func current(t *testing.T, ws, rel string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(ws, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func edited(t *testing.T, ws, rel, body string) Change {
	return Change{Path: rel, Before: sha(current(t, ws, rel)), After: sha(body), Body: []byte(body)}
}

func created(rel, body string) Change { return Change{Path: rel, After: sha(body), Body: []byte(body)} }

func removed(t *testing.T, ws, rel string) Change {
	return Change{Path: rel, Before: sha(current(t, ws, rel))}
}

const runnerQuery = "src/SelfHostedRunner.ql"

// What a session a worker ran changed lands in the coordinator's pack when every change is one its
// bounds allowed and replaces the file the session read: the query, its help, its .expected, and
// fixtures under its test, written or removed.
func TestASessionsChangesLandWhereItsBoundsAllowed(t *testing.T) {
	ws := landPack(t)
	query := strings.Replace(queryText("SelfHostedRunner", "acme/self-hosted-runner"), `"x"`, `"runs on self-hosted"`, 1)
	tests, err := Land(ws, "acme/self-hosted-runner", []Change{
		edited(t, ws, runnerQuery, query),
		created("src/SelfHostedRunner.md", "# Self-hosted runner\n"),
		created("test/SelfHostedRunner/.github/workflows/new.yml", "on: pull_request\n"),
		removed(t, ws, "test/SelfHostedRunner/.github/workflows/old.yml"),
		created("test/SelfHostedRunner/SelfHostedRunner.expected", "| x |\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tests != "test/SelfHostedRunner" {
		t.Errorf("the session's test is %q", tests)
	}
	if current(t, ws, runnerQuery) != query || current(t, ws, "src/SelfHostedRunner.md") != "# Self-hosted runner\n" ||
		current(t, ws, "test/SelfHostedRunner/.github/workflows/new.yml") != "on: pull_request\n" ||
		current(t, ws, "test/SelfHostedRunner/SelfHostedRunner.expected") != "| x |\n" {
		t.Error("a change did not land")
	}
	if _, err := os.Stat(filepath.Join(ws, "test", "SelfHostedRunner", ".github", "workflows", "old.yml")); err == nil {
		t.Error("the fixture the session removed is still there")
	}
}

// A worker's identity may sign any change, so a landing holding one the session's bounds did not
// allow lands nothing at all, and says which.
func TestAChangeOutsideTheSessionsBoundsLandsNothing(t *testing.T) {
	for name, bad := range map[string]func(t *testing.T, ws string) Change{
		"another query": func(t *testing.T, ws string) Change {
			return edited(t, ws, "src/Other.ql", queryText("Other", "acme/other")+"// changed\n")
		},
		"another query's fixture": func(t *testing.T, ws string) Change {
			return created("test/Other/.github/workflows/x.yml", "on: push\n")
		},
		"the test's .qlref": func(t *testing.T, ws string) Change {
			return edited(t, ws, "test/SelfHostedRunner/SelfHostedRunner.qlref", "query: src/Other.ql\n")
		},
		"a manifest under the test": func(t *testing.T, ws string) Change {
			return created("test/SelfHostedRunner/qlpack.yml", "name: x\n")
		},
		"a fixture that is not YAML": func(t *testing.T, ws string) Change {
			return created("test/SelfHostedRunner/notes.txt", "x\n")
		},
		"the pack's manifest": func(t *testing.T, ws string) Change {
			return edited(t, ws, "qlpack.yml", current(t, ws, "qlpack.yml")+"# x\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			ws := landPack(t)
			before := current(t, ws, runnerQuery)
			change := bad(t, ws)
			_, err := Land(ws, "acme/self-hosted-runner", []Change{
				edited(t, ws, runnerQuery, before+"// changed\n"), change})
			if err == nil || !strings.Contains(err.Error(), change.Path) {
				t.Errorf("a landing changing %s answered %v; want it refused naming %s", name, err, change.Path)
			}
			if current(t, ws, runnerQuery) != before {
				t.Error("the query landed beside a change that did not")
			}
		})
	}
}

// Nothing lands over a file that changed since the session read it: the landing is refused naming
// the file, and the rest of it is not written either.
func TestNothingLandsOverAFileThatChangedSinceTheSessionReadIt(t *testing.T) {
	ws := landPack(t)
	query := edited(t, ws, runnerQuery, queryText("SelfHostedRunner", "acme/self-hosted-runner")+"// mine\n")
	put(t, filepath.Join(ws, "src", "SelfHostedRunner.ql"), queryText("SelfHostedRunner", "acme/self-hosted-runner")+"// theirs\n")
	_, err := Land(ws, "acme/self-hosted-runner", []Change{query,
		created("test/SelfHostedRunner/.github/workflows/new.yml", "on: pull_request\n")})
	if err == nil || !strings.Contains(err.Error(), runnerQuery) {
		t.Errorf("landing over a moved file answered %v; want it refused naming %s", err, runnerQuery)
	}
	if _, err := os.Stat(filepath.Join(ws, "test", "SelfHostedRunner", ".github", "workflows", "new.yml")); err == nil {
		t.Error("the rest of a refused landing was written")
	}
}

// A query a session created lands with the test its scaffold lays out, exactly as laid out: a test
// that names another query is not one the scaffold wrote.
func TestACreatedQueryLandsWithTheTestItsScaffoldLaysOut(t *testing.T) {
	scaffold := customql.TestScaffold("NewPin")
	create := func(qlref string) []Change {
		return []Change{
			created("src/NewPin.ql", queryText("NewPin", "acme/new-pin")),
			created("src/NewPin.md", "# New pin\n"),
			created("test/NewPin/NewPin.qlref", qlref),
			created("test/NewPin/.github/workflows/a.yml", "on: push\n"),
			created("test/NewPin/NewPin.expected", "| x |\n"),
		}
	}
	ws := landPack(t)
	if tests, err := Land(ws, "acme/new-pin", create(scaffold["test/NewPin/NewPin.qlref"])); err != nil ||
		tests != "test/NewPin" {
		t.Fatalf("a created query landed as %q, %v", tests, err)
	}
	if current(t, ws, "test/NewPin/NewPin.expected") != "| x |\n" {
		t.Error("the created query's test did not land")
	}
	ws = landPack(t)
	if _, err := Land(ws, "acme/new-pin", create("query: src/NewPin.ql\n")); err == nil ||
		!strings.Contains(err.Error(), "NewPin.qlref") {
		t.Errorf("a .qlref the scaffold did not write answered %v", err)
	}
}

// A session lands only what it could write once bound to its request's query: one that changed the
// pack without deciding that, or left the query declaring another id, lands nothing.
func TestASessionNotBoundToItsQueryLandsNothing(t *testing.T) {
	ws := landPack(t)
	fixture := created("test/SelfHostedRunner/.github/workflows/new.yml", "on: pull_request\n")
	if _, err := Land(ws, "", []Change{fixture}); err == nil {
		t.Error("a session that decided no query landed a change")
	}
	renamed := edited(t, ws, runnerQuery, queryText("SelfHostedRunner", "acme/renamed"))
	if _, err := Land(ws, "acme/self-hosted-runner", []Change{renamed, fixture}); err == nil {
		t.Error("a session that left its query declaring another id landed")
	}
	if _, err := os.Stat(filepath.Join(ws, "test", "SelfHostedRunner", ".github", "workflows", "new.yml")); err == nil {
		t.Error("a refused landing was written")
	}
}

// A session that changed nothing lands nothing, whatever it decided.
func TestASessionThatChangedNothingLandsNothing(t *testing.T) {
	ws := landPack(t)
	if tests, err := Land(ws, "", nil); err != nil || tests != "" {
		t.Errorf("an empty landing answered %q, %v", tests, err)
	}
}

// A change is landed only as what it says it is: a plain path inside the pack reached through no
// link, bytes that are what the session left, over a file that is still there if the session read
// one. Anything else lands nothing, and writes nothing on the way to refusing it.
func TestALandingThatCannotBeWhatTheSessionDidIsRefused(t *testing.T) {
	for name, bad := range map[string]func(t *testing.T, ws string) Change{
		"a path through a link": func(t *testing.T, ws string) Change {
			outside := t.TempDir()
			workflows := filepath.Join(ws, "test", "SelfHostedRunner", ".github", "workflows")
			if err := os.RemoveAll(workflows); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, workflows); err != nil {
				t.Fatal(err)
			}
			return created("test/SelfHostedRunner/.github/workflows/a.yml", "on: push\n")
		},
		"a path outside the pack": func(t *testing.T, ws string) Change {
			return created("../escape.yml", "on: push\n")
		},
		"bytes that are not what the session left": func(t *testing.T, ws string) Change {
			c := created("test/SelfHostedRunner/.github/workflows/a.yml", "on: push\n")
			c.Body = []byte("on: workflow_dispatch\n")
			return c
		},
		"a file the session read that is gone": func(t *testing.T, ws string) Change {
			c := edited(t, ws, "test/SelfHostedRunner/.github/workflows/old.yml", "on: pull_request\n")
			if err := os.Remove(filepath.Join(ws, "test", "SelfHostedRunner", ".github", "workflows", "old.yml")); err != nil {
				t.Fatal(err)
			}
			return c
		},
	} {
		t.Run(name, func(t *testing.T) {
			ws := landPack(t)
			before := current(t, ws, runnerQuery)
			change := bad(t, ws)
			if _, err := Land(ws, "acme/self-hosted-runner", []Change{
				edited(t, ws, runnerQuery, before+"// changed\n"), change}); err == nil {
				t.Errorf("a landing with %s was kept", name)
			}
			if current(t, ws, runnerQuery) != before {
				t.Error("the query landed beside a change that did not")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(ws), "escape.yml")); err == nil {
				t.Error("a refused landing wrote outside the pack")
			}
		})
	}
}
