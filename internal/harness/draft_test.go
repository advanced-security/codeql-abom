package harness

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// sourceDatabase writes what a CodeQL database keeps of the source it was built from.
func sourceDatabase(t *testing.T, files map[string]string) string {
	t.Helper()
	db := t.TempDir()
	prefix := "/src/checkout"
	if err := os.WriteFile(filepath.Join(db, "codeql-database.yml"),
		[]byte("---\nsourceLocationPrefix: "+prefix+"\nprimaryLanguage: actions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(db, "src.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for rel, body := range files {
		w, err := zw.Create(strings.TrimPrefix(prefix, "/") + "/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

// analysisRun records a run of acme/pin over repositories whose databases are held at the paths given.
func (b bench) analysisRun(t *testing.T, databases map[string]string) int64 {
	t.Helper()
	held := []store.ScopedDatabase{}
	for repo, db := range databases {
		held = append(held, store.ScopedDatabase{DatabaseIdentity: store.DatabaseIdentity{
			Repository: repo, Commit: "c1", Mode: store.AnalysisModeRegular,
		}, Status: store.DatabaseRetained, Path: db})
	}
	id, err := b.store.CreateAnalysisRun(store.AnalysisRequest{RequestedBy: "operator",
		Queries: []store.AnalysisQuery{{QueryID: "acme/pin", QueryVersion: "v1"}}, Databases: held})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (b bench) drafted(t *testing.T, run int64, picked []Picked) store.DraftSession {
	t.Helper()
	_, done, err := b.sessions.draft(run, "acme/pin", picked, "operator")
	if err != nil {
		t.Fatal(err)
	}
	<-done
	drafts, err := b.store.DraftSessions(run)
	if err != nil || len(drafts) == 0 {
		t.Fatalf("drafts %v %v", drafts, err)
	}
	return drafts[0]
}

const pinnedWorkflow = "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n" +
	"      - uses: actions/checkout@692973e3d937129bcbf40652eb9f2f61becf3332\n"

// A drafting session reads only what ABOM hands it: the picked results with their code, and the
// files of the repositories picked. It has no files, no shell and no ql-mcp, and files nothing.
func TestADraftingSessionSeesOnlyWhatABOMHandsIt(t *testing.T) {
	var sealed bool
	var results, source string
	var refused []string
	b := newBench(t, stand(func(_ context.Context, s Setup) (string, error) {
		sealed = s.Sealed
		res, err := call(t, tool(t, s, ToolResults), map[string]any{})
		if err != nil {
			return "", err
		}
		results = res.TextResultForLLM
		res, err = call(t, tool(t, s, ToolSource), map[string]any{"repository": "octo/quiet",
			"path": ".github/workflows/ci.yml"})
		if err != nil {
			return "", err
		}
		source = res.TextResultForLLM
		for _, args := range []map[string]any{
			{"repository": "octo/unpicked", "path": ".github/workflows/ci.yml"},
			{"repository": "octo/quiet", "path": "../../etc/passwd"},
			{"repository": "octo/quiet", "path": "/etc/passwd"},
		} {
			if why := refusal(t, tool(t, s, ToolSource), args); why != "" {
				refused = append(refused, why)
			}
		}
		if _, err := call(t, tool(t, s, ToolDraft), map[string]any{
			"description":  "report actions/checkout pinned to a tag rather than a full SHA",
			"compliant":    pinnedWorkflow,
			"nonCompliant": strings.Replace(pinnedWorkflow, "692973e3d937129bcbf40652eb9f2f61becf3332", "v4", 1),
			"rationale":    "the picked finding is a full SHA pin",
		}); err != nil {
			return "", err
		}
		return "Drafted a request from the false alarm.", nil
	}))
	app := sourceDatabase(t, map[string]string{".github/workflows/ci.yml": pinnedWorkflow})
	quiet := sourceDatabase(t, map[string]string{".github/workflows/ci.yml": "on: push\n",
		"README.md": "not a workflow"})
	other := sourceDatabase(t, map[string]string{".github/workflows/ci.yml": "secret: nobody picked me\n"})
	run := b.analysisRun(t, map[string]string{"octo/app": app, "octo/quiet": quiet, "octo/unpicked": other})

	got := b.drafted(t, run, []Picked{
		{Repository: "octo/app", Kind: store.RequestFalseAlarm, FilePath: ".github/workflows/ci.yml",
			StartLine: 6, Message: "actions/checkout is pinned to a tag"},
		{Repository: "octo/quiet", Kind: store.RequestMissed},
	})

	if !sealed {
		t.Error("the drafting session was offered files, a shell or ql-mcp")
	}
	for _, want := range []string{"false alarm", "octo/app", "actions/checkout@692973e", "COMPLIANT",
		"missed detection", "octo/quiet", ".github/workflows/ci.yml"} {
		if !strings.Contains(results, want) {
			t.Errorf("the results do not say %q:\n%s", want, results)
		}
	}
	if strings.Contains(results, "README.md") {
		t.Errorf("the results list a file that is neither a workflow nor an action:\n%s", results)
	}
	if !strings.Contains(source, "on: push") {
		t.Errorf("reading a picked repository's workflow gave %q", source)
	}
	if len(refused) != 3 {
		t.Errorf("refused %d of 3 reads outside the picked repositories' source: %v", len(refused), refused)
	}
	if got.State != store.SessionFinished || got.Draft == nil ||
		!strings.Contains(got.Draft.NonCompliant, "actions/checkout@v4") {
		t.Errorf("drafted %+v", got)
	}
	if requests, err := b.store.QueryRequests(); err != nil || len(requests) != 0 {
		t.Errorf("drafting filed %+v %v", requests, err)
	}
	if _, err := os.Stat(filepath.Join(b.sessions.WorkDir, "draft-1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the drafting session's directory outlived it: %v", err)
	}
}

// A draft is written from picks the run showed, by an agent somebody configured.
func TestADraftIsRefusedWithoutAnAgentOrAPick(t *testing.T) {
	b := newBench(t, nil)
	run := b.analysisRun(t, map[string]string{"octo/app": ""})
	if _, err := b.sessions.Draft(run, "acme/pin", []Picked{{Repository: "octo/app",
		Kind: store.RequestMissed}}, "operator"); !errors.Is(err, ErrNoAgent) {
		t.Errorf("a draft with no agent: %v, want ErrNoAgent", err)
	}
	b.sessions.Agent = stand(func(context.Context, Setup) (string, error) { return "done", nil })
	if _, err := b.sessions.Draft(run, "acme/pin", nil, "operator"); err == nil {
		t.Error("a draft from nothing picked was started")
	}
	if _, err := b.sessions.Draft(run, "acme/pin", []Picked{{Repository: "octo/elsewhere",
		Kind: store.RequestMissed}}, "operator"); err == nil {
		t.Error("a draft from a repository the run does not hold was started")
	}
}
