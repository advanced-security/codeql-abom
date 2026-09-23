package expand

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// edgesPlan resolves a one-pack BQRS suite, returning the plan and the pack directory so a test
// can edit a library beside the query and re-plan.
func edgesPlan(t *testing.T, root string, files map[string]string) (tierPlan, string) {
	t.Helper()
	path := queryInPack(t, root, "advanced-security/abom-actions-graph-extraction-src", "2.27.0",
		"AbomEdges.ql", files["AbomEdges.ql"])
	dir := filepath.Dir(path)
	for name, body := range files {
		if name == "AbomEdges.ql" {
			continue
		}
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	e := &Expander{Resolver: &fakeResolver{
		queries: []string{path},
		meta:    map[string]runner.Metadata{path: {ID: "abom/edges", Tags: ingest.TagEdges}},
	}}
	planned, err := e.plan(context.Background(), Options{Suite: "abom-dependency-graph.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return planned, dir
}

func edgesRun(p tierPlan) store.QueryRun {
	return p.queryRun("abom/edges", revision{
		repository: "octo/app", commit: "sha-one", filter: "f0",
	})
}

// `query_version` has been a declared column nothing writes since the ledger was built, so a run
// could name the pack that supplied a query but never the query itself.
func TestARecordedRunNamesTheVersionOfTheQueryThatProducedIt(t *testing.T) {
	planned, _ := edgesPlan(t, t.TempDir(), map[string]string{
		"AbomEdges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":      "class A extends int {}\n",
	})
	if v := edgesRun(planned).QueryVersion; v == "" {
		t.Error("a planned query produced a run with no query version")
	}
}

// The payoff. A library edit leaves the query text untouched and changes what it means, so a run
// key blind to it would report the analysis already done and decline to look again.
func TestEditingALibraryMovesTheRunKeyOfTheQueryThatImportsIt(t *testing.T) {
	root := t.TempDir()
	planned, dir := edgesPlan(t, root, map[string]string{
		"AbomEdges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":      "class A extends int {}\n",
	})
	before := edgesRun(planned)

	if err := os.WriteFile(filepath.Join(dir, "Lib.qll"),
		[]byte("class A extends int { A() { this > 0 } }\n"), 0o644); err != nil {
		t.Fatalf("editing the library: %v", err)
	}
	after, _ := edgesPlan(t, root, map[string]string{
		"AbomEdges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":      "class A extends int { A() { this > 0 } }\n",
	})

	if v := edgesRun(after).QueryVersion; v == before.QueryVersion {
		t.Error("an imported library changed and the query version did not move, so a skip would " +
			"decline to re-analyze against a query that now means something else")
	}
}

// A version that did not move when nothing moved would re-analyze the whole corpus on every
// round, which is the opposite failure and just as expensive.
func TestAnUnchangedQueryKeepsItsVersionAcrossRounds(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"AbomEdges.ql": "import Lib\nfrom int i select i\n",
		"Lib.qll":      "class A extends int {}\n",
	}
	first, _ := edgesPlan(t, root, files)
	second, _ := edgesPlan(t, root, files)
	if edgesRun(first).QueryVersion != edgesRun(second).QueryVersion {
		t.Error("planning the same query twice produced two versions")
	}
}

// `database analyze` runs the findings lane as one invocation, so no single query owns the
// result -- but the lane still has to move when any query in it does, for the same reason its
// pack digest does.
func TestTheFindingsLaneIsVersionedByEveryQueryItRuns(t *testing.T) {
	root := t.TempDir()
	planned, path := findingsPlan(t, root, "select 1")
	before := planned.findingsRun(revision{repository: "octo/app", commit: "sha-one"})
	if before.QueryVersion == "" {
		t.Fatal("the findings lane produced a run with no query version")
	}

	if err := os.WriteFile(path, []byte("select 2"), 0o644); err != nil {
		t.Fatalf("editing the query: %v", err)
	}
	edited, _ := findingsPlan(t, root, "select 2")
	after := edited.findingsRun(revision{repository: "octo/app", commit: "sha-one"})
	if after.QueryVersion == before.QueryVersion {
		t.Error("a query in the findings lane changed and the lane's version did not move")
	}
}
