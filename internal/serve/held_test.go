package serve

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	oneHeld = `{"queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],` +
		`"by":"operator","held":true}`
	oneStored = `{"queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],` +
		`"by":"operator","held":false}`
	oneDraft = `{"queries":[{"queryId":"acme/draft","queryVersion":"d1"}],"by":"operator"}`
)

// publishUnpinnedTag makes actions/unpinned-tag@u1 the version an upstream pack published.
func publishUnpinnedTag(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.RecordSuiteContents("codeql/actions-queries:codeql-suites/actions-code-scanning.qls",
		[]store.SuiteQuery{{Pack: "codeql/actions-queries", QueryID: "actions/unpinned-tag", QueryVersion: "u1"}},
	); err != nil {
		t.Fatalf("RecordSuiteContents: %v", err)
	}
}

func control(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	req.Header.Set(controlHeader, "1")
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// A run that does not say takes the server's suggestion: a published version stores findings and
// a version still being refined holds them. Saying so either way overrides it.
func TestARunLeftUnsaidTakesTheSuggestionAndSayingSoOverridesIt(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	publishUnpinnedTag(t, st)

	for name, c := range map[string]struct {
		body string
		held bool
	}{
		"a published version, left unsaid": {oneQuery, false},
		"a draft, left unsaid":             {oneDraft, true},
		"a published version, held":        {oneHeld, true},
		"a draft, stored":                  {strings.Replace(oneDraft, `"by"`, `"held":false,"by"`, 1), false},
	} {
		run, code := start(t, s, "rules=actions%2F*", c.body)
		if code != http.StatusOK {
			t.Fatalf("%s answered %d", name, code)
		}
		if run.Held != c.held {
			t.Errorf("%s recorded held=%v, want %v", name, run.Held, c.held)
		}
	}
}

// Holding is the run's, not its shape's: refining against a closure database is legitimate.
func TestAHeldRunOverExtendedDatabasesIsRecorded(t *testing.T) {
	s, _ := scopedServer(t)
	run, code := start(t, s, "repositories=octo-org%2Fapp&mode=extended", oneHeld)
	if code != http.StatusOK || !run.Held || len(run.Members) == 0 {
		t.Errorf("a held run over extended databases answered %d held=%v with %d members",
			code, run.Held, len(run.Members))
	}
}

// What a held run kept is listed one member at a time, as a run's results are, and says it is held:
// no finding stands behind any of it yet.
func TestAHeldRunsResultsAreListedAsHeld(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	run, code := start(t, s, "rules=actions%2F*", oneHeld)
	if code != http.StatusOK || len(run.Members) != 1 {
		t.Fatalf("asking for a held run answered %d with %d members", code, len(run.Members))
	}
	m := run.Members[0]
	if err := st.RecordHeldMemberResult(run.RowID, m.DatabaseIdentity, store.MemberResult{
		Status: store.AnalysisSucceeded, Results: 1, Counted: true,
		ByQuery: map[string]int{"actions/unpinned-tag": 1},
	}, []store.Finding{{RuleID: "actions/unpinned-tag",
		FilePath: ".github/workflows/ci.yml", StartLine: 9, Message: "Unpinned 3rd party Action"}}); err != nil {
		t.Fatalf("RecordHeldMemberResult: %v", err)
	}

	got := decode[store.MemberFindings](t, get(t, s,
		"/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/results?repository="+m.Repository))
	if !got.Held || !got.Evaluated || len(got.Findings) != 1 || got.Findings[0].StartLine != 9 ||
		got.Findings[0].HeldResultID == 0 {
		t.Errorf("the held member reads %+v, want its one held result, named", got)
	}
}

// The draft asks the server what it suggests before Run, in the words the run will use.
func TestTheSuggestionIsServedForADraft(t *testing.T) {
	s, st := newServer(t)
	publishUnpinnedTag(t, st)

	got := decode[store.FindingsSuggestion](t, get(t, s,
		"/api/runs/suggestion?query=actions/unpinned-tag@u1&query=acme/draft@d1"))
	if got.Findings || len(got.Queries) != 2 || !got.Queries[0].Promoted || got.Queries[1].Promoted ||
		!strings.Contains(got.Reason, "acme/draft") {
		t.Errorf("the suggestion reads %+v, want findings off naming the draft", got)
	}
	one := decode[store.FindingsSuggestion](t, get(t, s, "/api/runs/suggestion?query=actions/unpinned-tag@u1"))
	if !one.Findings {
		t.Errorf("a published version alone reads %+v, want findings on", one)
	}
	for target, want := range map[string]int{
		"/api/runs/suggestion":                            http.StatusBadRequest,
		"/api/runs/suggestion?query=actions/unpinned-tag": http.StatusBadRequest,
		"/api/runs/suggestion?query=@u1":                  http.StatusBadRequest,
		"/api/runs/suggestion?rules=actions/unpinned-tag": http.StatusBadRequest,
	} {
		if code := get(t, s, target).Code; code != want {
			t.Errorf("%s answered %d, want %d", target, code, want)
		}
	}
}

// Converting is a write every surface then reads, so it is guarded like any other, refused for a
// run that holds nothing, and answers what it did.
func TestConvertingHeldResultsOverTheAPI(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	run, code := start(t, s, "rules=actions%2F*", oneHeld)
	if code != http.StatusOK || len(run.Members) != 1 {
		t.Fatalf("asking for a held run answered %d with %d members", code, len(run.Members))
	}
	m := run.Members[0]
	evaluation, err := st.RecordQueryRun(store.QueryRun{QueryID: "actions/unpinned-tag", QueryVersion: "u1",
		Repository: m.Repository, DBCommit: m.Commit, SourceFilter: m.SourceFilter,
		AnalysisMode: store.AnalysisModeRegular, RecordedBy: store.RecordedByRun, Held: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordHeldMemberResult(run.RowID, m.DatabaseIdentity, store.MemberResult{
		Status: store.AnalysisSucceeded, Results: 1, Counted: true, QueryRunID: evaluation,
	}, []store.Finding{{RuleID: "actions/unpinned-tag", FilePath: ".github/workflows/ci.yml",
		StartLine: 9, Fingerprint: "new"}}); err != nil {
		t.Fatal(err)
	}
	path := "/api/runs/" + strconv.FormatInt(run.RowID, 10) + "/convert"
	body := `{"repository":"` + m.Repository + `","by":"operator"}`

	if code := post(t, s, path).Code; code != http.StatusForbidden {
		t.Errorf("an unguarded conversion answered %d, want 403", code)
	}
	got := decode[store.Conversion](t, control(t, s, path, body))
	if got.Converted != 1 {
		t.Errorf("converting over the API reads %+v, want the 1 result held", got)
	}
	var findings int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM abom_finding WHERE fingerprint = 'new'`).
		Scan(&findings); err != nil || findings != 1 {
		t.Errorf("the conversion left %d findings (%v), want the 1 converted", findings, err)
	}

	stored, code := start(t, s, "rules=actions%2F*", oneStored)
	if code != http.StatusOK {
		t.Fatal(code)
	}
	for target, want := range map[string]int{
		"/api/runs/" + strconv.FormatInt(stored.RowID, 10) + "/convert": http.StatusBadRequest,
		"/api/runs/999/convert": http.StatusNotFound,
		"/api/runs/one/convert": http.StatusBadRequest,
	} {
		if code := control(t, s, target, body).Code; code != want {
			t.Errorf("%s answered %d, want %d", target, code, want)
		}
	}
	if code := control(t, s, path, `{"repository":"`+m.Repository+`"}`).Code; code != http.StatusBadRequest {
		t.Errorf("a conversion nobody signed answered %d, want 400", code)
	}
	if code := control(t, s, path, `not json`).Code; code != http.StatusBadRequest {
		t.Errorf("a conversion with no readable body answered %d, want 400", code)
	}
}
