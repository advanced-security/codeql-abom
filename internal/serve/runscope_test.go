package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// scopedServer holds an owned repository with a finding, an owned one nothing has fired in, and
// one in no declared account, each analyzed once.
func scopedServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st := newServer(t)
	seedOneFinding(t, st)
	declareOcto(t, st)
	outside(t, st, "octo-org/quiet")
	outside(t, st, "stranger/thing")
	for _, repo := range []string{"octo-org/app", "octo-org/quiet", "stranger/thing"} {
		if _, err := st.RecordQueryRun(store.QueryRun{
			AnalysisMode: store.AnalysisModeRegular, QueryID: "abom/edges",
			Repository: repo, DBCommit: "sha-" + repo, SourceFilter: "f0",
		}); err != nil {
			t.Fatalf("RecordQueryRun(%s): %v", repo, err)
		}
	}
	return srv, st
}

func membersOf(run store.AnalysisRun) []string {
	out := []string{}
	for _, m := range run.Members {
		out = append(out, m.Repository)
	}
	slices.Sort(out)
	return out
}

func resolvedNames(set store.DatabaseSet) []string {
	out := []string{}
	for _, d := range set.Databases {
		out = append(out, d.Repository)
	}
	for _, g := range set.Gaps {
		out = append(out, g.Repository)
	}
	slices.Sort(out)
	return out
}

// startRefusal asks for a run and answers the status and the refusal text.
func startRefusal(t *testing.T, s *Server, query string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.startRun(w, httptest.NewRequest("POST", "/api/runs?"+query, bytes.NewBufferString(oneQuery)))
	return w.Code, w.Body.String()
}

// A named scope's run covers exactly what the repositories unit lists for it, so the run's set is
// a tab a reader can open and count — including a repository nothing has ever fired in.
func TestARunOverANamedScopeCoversExactlyWhatTheRepositoriesUnitLists(t *testing.T) {
	srv, _ := scopedServer(t)

	page := decode[store.RepositoriesPage](t, get(t, srv, "/api/repositories?view=owned&top=1000"))
	listed := []string{}
	for _, r := range page.Items {
		listed = append(listed, r.FullName)
	}
	slices.Sort(listed)
	if !slices.Contains(listed, "octo-org/quiet") || slices.Contains(listed, "stranger/thing") {
		t.Fatalf("the unit lists %v; the fixture must list a clean owned repository and no other",
			listed)
	}

	run, code := start(t, srv, "view=owned", oneQuery)
	if code != http.StatusOK {
		t.Fatalf("a run over the named scope answered %d", code)
	}
	if got := membersOf(run); !slices.Equal(got, listed) {
		t.Errorf("the run covers %v, want exactly the %v the repositories unit lists", got, listed)
	}
}

// A scope carrying a term that chooses no repository is refused with the term named, and the
// preview a reader sees before pressing Run refuses it the same way.
func TestAScopeARunCannotCoverIsRefusedNamingItsTerms(t *testing.T) {
	srv, _ := scopedServer(t)

	for view, terms := range map[string][]string{
		"owned-findings":        {"flags"},
		"supply-chain-findings": {"flags"},
		"unpinned":              {"ref"},
		"coverage-gaps":         {"flags", "ref"},
	} {
		code, body := startRefusal(t, srv, "view="+view)
		if code != http.StatusBadRequest {
			t.Errorf("%s answered %d starting a run, want 400", view, code)
		}
		for _, term := range terms {
			if !strings.Contains(body, term) {
				t.Errorf("%s was refused without naming %q: %s", view, term, body)
			}
		}
		if _, previewed := resolveDatabases(t, srv, "view="+view); previewed != http.StatusBadRequest {
			t.Errorf("%s answered %d previewing, want the run's refusal", view, previewed)
		}
	}
}

// Every term narrows: a findings term keeps the listed repositories it was found in.
func TestAFindingsTermNarrowsARunOverAScope(t *testing.T) {
	srv, _ := scopedServer(t)

	run, code := start(t, srv, "scope=owned&rules=actions%2Funpinned-tag", oneQuery)
	if code != http.StatusOK {
		t.Fatalf("a run over an owned rule answered %d", code)
	}
	if got := membersOf(run); !slices.Equal(got, []string{"octo-org/app"}) {
		t.Errorf("the run covers %v, want only octo-org/app, owned and fired in", got)
	}
}

// A run records the question a name resolved to. Keeping another scope under the same name must not
// change what an old run says it asked.
func TestARunRecordsTheQuestionANameResolvedToRatherThanTheName(t *testing.T) {
	srv, st := scopedServer(t)
	if err := st.SaveView(store.SavedView{
		Name: "mine", Predicate: "scope=owned", CreatedBy: store.DeclaredByInterface,
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	run, code := start(t, srv, "view=mine", oneQuery)
	if code != http.StatusOK {
		t.Fatalf("a run over a kept scope answered %d", code)
	}
	if err := st.SaveView(store.SavedView{
		Name: "mine", Predicate: "q=quiet", CreatedBy: store.DeclaredByInterface,
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}
	again, err := st.AnalysisRun(run.RowID)
	if err != nil {
		t.Fatalf("AnalysisRun: %v", err)
	}
	if again.Predicate != "scope=owned" {
		t.Errorf("the run records %q, want the question the name resolved to", again.Predicate)
	}
	if again.ScopeName != "mine" {
		t.Errorf("the run records it was chosen as %q, want the name it was chosen by", again.ScopeName)
	}
}

// A run chosen by name says so, beside the question; one chosen any other way names no scope,
// rather than a name somebody might take for how it was chosen.
func TestARunNamesTheScopeItWasChosenByAndOnlyThatOne(t *testing.T) {
	srv, _ := scopedServer(t)

	named, code := start(t, srv, "view=owned", oneQuery)
	if code != http.StatusOK {
		t.Fatalf("a run over a built-in answered %d", code)
	}
	listed := decode[struct {
		Runs []store.AnalysisRun `json:"runs"`
	}](t, get(t, srv, "/api/runs"))
	if len(listed.Runs) != 1 || listed.Runs[0].ScopeName != "owned" || named.ScopeName != "owned" {
		t.Errorf("a run chosen as owned lists as %+v", listed.Runs)
	}

	for _, query := range []string{"scope=owned", "repositories=octo-org%2Fquiet"} {
		run, code := start(t, srv, query, oneQuery)
		if code != http.StatusOK {
			t.Fatalf("running %s answered %d", query, code)
		}
		if run.ScopeName != "" {
			t.Errorf("%s names the scope %q it was never chosen by", query, run.ScopeName)
		}
	}
}

// What a reader is shown before pressing Run is what the run covers, compared rather than assumed.
func TestThePreviewResolvesExactlyWhatTheRunCovers(t *testing.T) {
	srv, _ := scopedServer(t)

	for _, query := range []string{
		"view=owned", "q=octo-org", "scope=owned&rules=actions%2Funpinned-tag",
		"repositories=octo-org%2Fquiet,stranger%2Fthing", "rules=actions%2F*",
	} {
		set, code := resolveDatabases(t, srv, query)
		if code != http.StatusOK {
			t.Fatalf("previewing %s answered %d", query, code)
		}
		run, code := start(t, srv, query, oneQuery)
		if code != http.StatusOK {
			t.Fatalf("running %s answered %d", query, code)
		}
		if got, want := membersOf(run), resolvedNames(set); !slices.Equal(got, want) {
			t.Errorf("%s: the run covers %v and the preview showed %v", query, got, want)
		}
	}
}

// A scope listing nothing is refused rather than run over everything. Without a declaration the
// owned scope lists nothing, and the rule beside it must not widen that to wherever it fired.
func TestAScopeListingNothingIsRefusedRatherThanRunOverEverything(t *testing.T) {
	srv, st := newServer(t)
	seedOneFinding(t, st)

	for _, query := range []string{"scope=owned", "scope=owned&rules=actions%2F*"} {
		code, body := startRefusal(t, srv, query)
		if code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want a refusal: nothing is declared", query, code)
		}
		if !strings.Contains(body, "lists no repository") {
			t.Errorf("%s was refused without the reason: %s", query, body)
		}
	}
	if list := decode[struct {
		Runs []store.AnalysisRun `json:"runs"`
	}](t, get(t, srv, "/api/runs")); len(list.Runs) != 0 {
		t.Errorf("a refused scope still recorded %d run(s)", len(list.Runs))
	}
}
