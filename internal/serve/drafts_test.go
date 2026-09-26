package serve

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// pickedRun is a run of acme/a over octo/app, where it fired, and octo/quiet, where it ran and
// found nothing.
func pickedRun(t *testing.T, st *store.Store) (run, fired int64) {
	t.Helper()
	repo, err := st.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	held := func(name string) store.ScopedDatabase {
		return store.ScopedDatabase{DatabaseIdentity: store.DatabaseIdentity{
			Repository: name, Commit: "c1", Mode: store.AnalysisModeRegular,
		}, Status: store.DatabaseRetained, Path: "/db/" + name}
	}
	run, err = st.CreateAnalysisRun(store.AnalysisRequest{RequestedBy: "operator",
		Queries:   []store.AnalysisQuery{{QueryID: "acme/a", QueryVersion: "v1"}},
		Databases: []store.ScopedDatabase{held("octo/app"), held("octo/quiet")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"octo/app", "octo/quiet"} {
		qr, err := st.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular, QueryID: "acme/a",
			QueryVersion: "v1", Repository: name, DBCommit: "c1"})
		if err != nil {
			t.Fatal(err)
		}
		found := 0
		if name == "octo/app" {
			if _, err := st.AddFindings([]store.Finding{{QueryRunID: qr, RuleID: "acme/a", RepositoryID: repo,
				FilePath: ".github/workflows/ci.yml", StartLine: 6, Message: "pinned to a tag"}}); err != nil {
				t.Fatal(err)
			}
			found = 1
		}
		if err := st.RecordAnalysisMemberResult(run, held(name).DatabaseIdentity, store.MemberResult{
			Status: store.AnalysisSucceeded, Counted: true, Results: found,
			ByQuery: map[string]int{"acme/a": found}, QueryRunID: qr,
		}); err != nil {
			t.Fatal(err)
		}
	}
	listed, err := st.RunFindings(run, "octo/app", "", 10)
	if err != nil || len(listed.Findings) != 1 {
		t.Fatalf("the fixture run lists %+v (err %v)", listed, err)
	}
	return run, listed.Findings[0].FindingRowID
}

// Drafting spends somebody's credits, so it is a guarded control; and it is written only from
// what the run showed, each pick handed on with where it was and what was said of it.
func TestDraftingIsAGuardedControlOverWhatTheRunShowed(t *testing.T) {
	s, st, f, _ := authoringServer(t)
	run, fired := pickedRun(t, st)
	path := fmt.Sprintf("/api/runs/%d/drafts", run)
	picks := fmt.Sprintf(`{"queryId":"acme/a","by":"operator","evidence":[
		{"repository":"octo/app","findingRowId":%d,"kind":"false-alarm"},
		{"repository":"octo/quiet","kind":"missed"}]}`, fired)

	if w := sessionPost(t, s, path, picks, false); w.Code != http.StatusForbidden {
		t.Errorf("a draft from another origin answered %d, want 403", w.Code)
	}
	if w := sessionPost(t, s, path, `{"queryId":"acme/a","by":"operator","evidence":[
		{"repository":"octo/elsewhere","kind":"missed"}]}`, true); w.Code != http.StatusBadRequest {
		t.Errorf("a draft from a pick the run did not show answered %d, want 400", w.Code)
	}
	if len(f.drafted) != 0 {
		t.Fatalf("refused picks reached the agent: %+v", f.drafted)
	}

	if w := sessionPost(t, s, path, picks, true); w.Code != http.StatusOK {
		t.Fatalf("drafting answered %d: %s", w.Code, w.Body.String())
	}
	if len(f.drafted) != 1 || len(f.drafted[0]) != 2 {
		t.Fatalf("drafted %+v", f.drafted)
	}
	alarm, miss := f.drafted[0][0], f.drafted[0][1]
	if alarm.Kind != store.RequestFalseAlarm || alarm.FilePath != ".github/workflows/ci.yml" ||
		alarm.StartLine != 6 || !strings.Contains(alarm.Message, "pinned to a tag") ||
		miss.Kind != store.RequestMissed || miss.Repository != "octo/quiet" {
		t.Errorf("handed the agent %+v and %+v", alarm, miss)
	}
	var listed struct {
		Drafts []store.DraftSession `json:"drafts"`
	}
	if code := sessionGet(t, s, path, &listed); code != http.StatusOK || len(listed.Drafts) != 1 ||
		listed.Drafts[0].QueryID != "acme/a" {
		t.Errorf("the run's drafts answered %d with %+v", code, listed.Drafts)
	}
	if w := sessionPost(t, s, path+"/stop", "", true); w.Code != http.StatusOK {
		t.Errorf("stopping the draft answered %d", w.Code)
	}
}

func TestAServerWithNoAgentSaysSoRatherThanDrafting(t *testing.T) {
	s, st, _, _ := authoringServer(t)
	s.Authoring = nil
	run, _ := pickedRun(t, st)
	w := sessionPost(t, s, fmt.Sprintf("/api/runs/%d/drafts", run),
		`{"queryId":"acme/a","by":"operator","evidence":[{"repository":"octo/quiet","kind":"missed"}]}`, true)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "--copilot-cli") {
		t.Errorf("drafting with no agent answered %d: %s", w.Code, w.Body.String())
	}
}
