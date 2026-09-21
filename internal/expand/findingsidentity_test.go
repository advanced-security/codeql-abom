package expand

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// queryInPack writes a query into a real pack directory, since the identity is read off the
// manifest beside the query and hashed from the files around it.
func queryInPack(t *testing.T, root, pack, version, query, body string) string {
	t.Helper()
	dir := filepath.Join(root, filepath.Base(pack))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	manifest := "name: " + pack + "\nversion: " + version + "\n"
	if err := os.WriteFile(filepath.Join(dir, "qlpack.yml"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	path := filepath.Join(dir, query)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write query: %v", err)
	}
	return path
}

// findingsPlan resolves a one-pack findings suite from a temporary pack, returning the plan and
// the query's path so a test can edit it and re-plan.
func findingsPlan(t *testing.T, root, body string) (tierPlan, string) {
	t.Helper()
	path := queryInPack(t, root, "codeql/actions-queries", "0.6.34", "Unpinned.ql", body)

	e := &Expander{Resolver: &fakeResolver{
		queries: []string{path},
		meta:    map[string]runner.Metadata{path: {ID: "actions/unpinned-tag", Kind: "problem"}},
	}}
	planned, err := e.plan(context.Background(), Options{Suite: "abom-findings.qls"})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return planned, path
}

// `database analyze` runs the whole findings suite as one invocation, so no single query owns the
// result and the lane would otherwise have no pack identity at all. Without one, upgrading
// codeql/actions-queries leaves the run key unchanged and a skip declines to re-analyze against
// rules that did change.
func TestTheFindingsLaneIsIdentifiedByThePacksItDrawsRulesFrom(t *testing.T) {
	planned, _ := findingsPlan(t, t.TempDir(), "select 1")

	run := planned.identifyFindings(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, Repository: "octo/app"})
	if run.Pack != "codeql/actions-queries" || run.PackVersion != "0.6.34" {
		t.Errorf("findings run attributed to %s@%s, want codeql/actions-queries@0.6.34",
			run.Pack, run.PackVersion)
	}
	if run.PackDigest == "" {
		t.Error("findings run carries no pack digest, so a pack edit could not change its key")
	}
}

func TestEditingAFindingsQueryChangesWhatTheLaneIsIdentifiedAs(t *testing.T) {
	root := t.TempDir()
	before, path := findingsPlan(t, root, "select 1")

	if err := os.WriteFile(path, []byte("select 2"), 0o644); err != nil {
		t.Fatalf("edit query: %v", err)
	}
	after, _ := findingsPlan(t, root, "select 2")

	b := before.identifyFindings(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, Repository: "octo/app"})
	a := after.identifyFindings(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, Repository: "octo/app"})
	if b.PackDigest == a.PackDigest {
		t.Errorf("editing a rule left the digest at %s, so the run key would not move",
			a.PackDigest)
	}
}

// Two packs cannot both name the run, so the set stands in for it. The digest still has to cover
// every one of them, or a change in the second pack would go unnoticed.
func TestAFindingsLaneSpanningPacksIsIdentifiedByAllOfThem(t *testing.T) {
	root := t.TempDir()
	upstream := queryInPack(t, root, "codeql/actions-queries", "0.6.34", "Unpinned.ql", "select 1")
	ours := queryInPack(t, root, "advanced-security/abom-rules", "2.26.4", "Ours.ql", "select 2")

	plan := func() tierPlan {
		e := &Expander{Resolver: &fakeResolver{
			queries: []string{upstream, ours},
			meta: map[string]runner.Metadata{
				upstream: {ID: "actions/unpinned-tag", Kind: "problem"},
				ours:     {ID: "abom/ours", Kind: "problem"},
			},
		}}
		planned, err := e.plan(context.Background(), Options{Suite: "abom-findings.qls"})
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		return planned
	}

	before := plan().identifyFindings(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, Repository: "octo/app"})
	if before.Pack != "codeql/actions-queries advanced-security/abom-rules" {
		t.Errorf("pack recorded as %q, want both packs named", before.Pack)
	}

	if err := os.WriteFile(ours, []byte("select 3"), 0o644); err != nil {
		t.Fatalf("edit query: %v", err)
	}
	after := plan().identifyFindings(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, Repository: "octo/app"})
	if before.PackDigest == after.PackDigest {
		t.Error("editing the second pack left the combined digest unchanged")
	}
}
