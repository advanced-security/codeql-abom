package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// fakeComparer records what it was asked to compare and reads nothing, marking every member as
// compared with nothing found, so a comparison finishes the way one that read GitHub would.
type fakeComparer struct {
	st    *store.Store
	asked chan int64
}

func (f *fakeComparer) Compare(_ context.Context, id int64) error {
	defer func() { f.asked <- id }()
	pending, err := f.st.PendingComparisonMembers(id)
	if err != nil {
		return err
	}
	for _, m := range pending {
		if err := f.st.RecordComparisonMember(id, store.ComparisonRecord{
			DatabaseIdentity: m, Status: store.ComparisonCompared, Staleness: store.StalenessCurrent,
		}); err != nil {
			return err
		}
	}
	return f.st.FinishComparison(id)
}

// finishedHeldRun asks for a held run over the API and records it finished with one result, under
// the CodeQL named.
func finishedHeldRun(t *testing.T, s *Server, st *store.Store, query, cli string) store.AnalysisRun {
	t.Helper()
	seedOneFinding(t, st)
	run, code := start(t, s, query, oneHeld)
	if code != http.StatusOK || len(run.Members) == 0 {
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
	if err := st.SetAnalysisRunCodeQL(run.RowID, cli); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAnalysisRunState(run.RowID, store.RunComplete); err != nil {
		t.Fatal(err)
	}
	return run
}

func compareOver(t *testing.T, s *Server, run int64) (store.Comparison, *strings.Builder, int) {
	t.Helper()
	w := control(t, s, "/api/runs/"+strconv.FormatInt(run, 10)+"/comparisons", `{"by":"operator"}`)
	var c store.Comparison
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
			t.Fatalf("decoding the comparison: %v", err)
		}
	}
	body := &strings.Builder{}
	body.Write(w.Body.Bytes())
	return c, body, w.Code
}

// Comparing spends the operator's GitHub budget and records what it read, so it is guarded like any
// other write, records who asked, and is carried out rather than only written down.
func TestComparingARunIsGuardedAndCarriedOut(t *testing.T) {
	s, st := newServer(t)
	fake := &fakeComparer{st: st, asked: make(chan int64, 1)}
	s.Comparer = fake
	run := finishedHeldRun(t, s, st, "rules=actions%2F*", "2.27.1")

	rec := get(t, s, "/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/comparisons")
	if rec.Code != http.StatusOK {
		t.Fatalf("listing comparisons answered %d", rec.Code)
	}
	unguarded := httptestPost(t, s, "/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/comparisons", `{"by":"operator"}`)
	if unguarded != http.StatusForbidden {
		t.Errorf("a comparison without the control header answered %d, want 403", unguarded)
	}

	c, body, code := compareOver(t, s, run.RowID)
	if code != http.StatusOK {
		t.Fatalf("comparing answered %d: %s", code, body)
	}
	if c.RunID != run.RowID || c.RequestedBy != "operator" || c.Condition == "" {
		t.Errorf("the comparison reads %+v", c)
	}
	if got := <-fake.asked; got != c.RowID {
		t.Errorf("the comparer was asked for %d, want %d", got, c.RowID)
	}
	read := decode[store.Comparison](t, get(t, s, "/api/comparisons/"+strconv.FormatInt(c.RowID, 10)))
	if read.State != store.ComparisonComplete || read.Compared != 1 {
		t.Errorf("read back, the comparison is %q with %d compared", read.State, read.Compared)
	}
	listed := decode[struct {
		Comparisons []store.Comparison `json:"comparisons"`
	}](t, get(t, s, "/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/comparisons"))
	if len(listed.Comparisons) != 1 || listed.Comparisons[0].RowID != c.RowID {
		t.Errorf("the run lists %+v", listed.Comparisons)
	}
	results := decode[struct {
		Repository string                 `json:"repository"`
		Results    []store.ComparedResult `json:"results"`
	}](t, get(t, s, "/api/comparisons/"+strconv.FormatInt(c.RowID, 10)+"/results?repository="+run.Members[0].Repository))
	if results.Repository != run.Members[0].Repository || results.Results == nil {
		t.Errorf("the results read %+v", results)
	}

	for target, want := range map[string]int{
		"/api/comparisons/999":                        http.StatusNotFound,
		"/api/comparisons/x":                          http.StatusBadRequest,
		"/api/comparisons/1/results":                  http.StatusBadRequest,
		"/api/comparisons/1/results?repository=a&q=b": http.StatusBadRequest,
		"/api/runs/999/comparisons":                   http.StatusNotFound,
	} {
		if got := get(t, s, target).Code; got != want {
			t.Errorf("%s answered %d, want %d", target, got, want)
		}
	}
}

// A server that cannot read code scanning alerts says so rather than recording a comparison that
// would sit unread for ever.
func TestAServerThatCannotReadAlertsSaysSoAndRecordsNothing(t *testing.T) {
	s, st := newServer(t)
	run := finishedHeldRun(t, s, st, "rules=actions%2F*", "2.27.1")
	_, body, code := compareOver(t, s, run.RowID)
	if code != http.StatusServiceUnavailable || !strings.Contains(body.String(), "code scanning") {
		t.Errorf("comparing without a reader answered %d: %s", code, body)
	}
	listed := decode[struct {
		Comparisons []store.Comparison `json:"comparisons"`
	}](t, get(t, s, "/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/comparisons"))
	if len(listed.Comparisons) != 0 {
		t.Errorf("a refused comparison was recorded: %+v", listed.Comparisons)
	}
}

// Asked of a run over extended databases, a comparison is refused where it is asked for, naming
// the shape; the run itself stays legitimate.
func TestComparingAnExtendedRunIsRefusedNamingTheShape(t *testing.T) {
	s, st := scopedServer(t)
	s.Comparer = &fakeComparer{st: st, asked: make(chan int64, 1)}
	run, code := start(t, s, "repositories=octo-org%2Fapp&mode=extended", oneHeld)
	if code != http.StatusOK {
		t.Fatalf("an extended held run answered %d", code)
	}
	if err := st.SetAnalysisRunState(run.RowID, store.RunComplete); err != nil {
		t.Fatal(err)
	}
	_, body, code := compareOver(t, s, run.RowID)
	if code != http.StatusBadRequest || !strings.Contains(body.String(), store.AnalysisModeExtended) {
		t.Errorf("comparing an extended run answered %d: %s", code, body)
	}
}

// review is refused over the API with the store's own words, and the catalog states them on the
// row before anybody presses.
func TestReviewIsRefusedUntilCompared(t *testing.T) {
	s, st := newServer(t)
	s.Tester = &fakeTester{}
	run := finishedHeldRun(t, s, st, "rules=actions%2F*", "2.27.1")
	publishUnpinnedTag(t, st)

	refused := advanceTo(t, s, "actions/unpinned-tag", "u1", store.StageReview)
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "compared") {
		t.Fatalf("advancing an uncompared version answered %d: %s", refused.Code, refused.Body)
	}
	row := catalogRowOf(t, s, "actions/unpinned-tag", "u1")
	if !strings.Contains(row.ReviewRefusal, "compared") {
		t.Errorf("the catalog row says %q before anybody presses", row.ReviewRefusal)
	}

	fake := &fakeComparer{st: st, asked: make(chan int64, 1)}
	s.Comparer = fake
	if _, body, code := compareOver(t, s, run.RowID); code != http.StatusOK {
		t.Fatalf("comparing answered %d: %s", code, body)
	}
	<-fake.asked
	if row := catalogRowOf(t, s, "actions/unpinned-tag", "u1"); row.ReviewRefusal != "" {
		t.Errorf("a compared version's row still says %q", row.ReviewRefusal)
	}
	if w := advanceTo(t, s, "actions/unpinned-tag", "u1", store.StageReview); w.Code != http.StatusOK {
		t.Errorf("advancing a compared version answered %d: %s", w.Code, w.Body)
	}
}

// httptestPost posts as a browser page elsewhere would, without the control header.
func httptestPost(t *testing.T, s *Server, path, body string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	s.Handler().ServeHTTP(rec, req)
	return rec.Code
}

func catalogRowOf(t *testing.T, s *Server, id, version string) catalogRow {
	t.Helper()
	answer := decode[struct {
		Queries []catalogRow `json:"queries"`
	}](t, get(t, s, "/api/queries"))
	for _, row := range answer.Queries {
		if row.QueryID == id && row.QueryVersion == version {
			return row
		}
	}
	t.Fatalf("the catalog holds no %s@%s", id, version)
	return catalogRow{}
}

// A run analyzed again while its comparison reads would mix two runs' results under one CodeQL, so
// neither starts while the other is under way on this server.
func TestARunIsNotAnalyzedAgainWhileItIsComparedNorComparedWhileItRuns(t *testing.T) {
	s, st := newServer(t)
	s.Comparer = &fakeComparer{st: st, asked: make(chan int64, 1)}
	s.Executor = blockedExecutor{}
	run := finishedHeldRun(t, s, st, "rules=actions%2F*", "2.27.1")

	s.holdRun(run.RowID, func() {})
	if _, body, code := compareOver(t, s, run.RowID); code != http.StatusConflict {
		t.Errorf("comparing a run under way answered %d: %s", code, body)
	}
	s.releaseRun(run.RowID)

	if !s.holdComparison(run.RowID) {
		t.Fatal("could not hold the comparison")
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/runs/"+strconv.FormatInt(run.RowID, 10)+"/start", nil)
	req.SetPathValue("id", strconv.FormatInt(run.RowID, 10))
	s.executeRun(w, req)
	if w.Code != http.StatusConflict {
		t.Errorf("starting a run under comparison answered %d: %s", w.Code, w.Body)
	}
	s.releaseComparison(run.RowID)
}

type blockedExecutor struct{}

func (blockedExecutor) Run(context.Context, int64) error { return nil }
