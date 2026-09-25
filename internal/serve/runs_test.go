package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func start(t *testing.T, s *Server, query, body string) (store.AnalysisRun, int) {
	t.Helper()
	w := httptest.NewRecorder()
	s.startRun(w, httptest.NewRequest("POST", "/api/runs?"+query, bytes.NewBufferString(body)))
	var run store.AnalysisRun
	if w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &run); err != nil {
			t.Fatalf("decoding the run: %v", err)
		}
	}
	return run, w.Code
}

const oneQuery = `{"queries":[{"queryId":"actions/unpinned-tag","queryVersion":"u1"}],"by":"operator"}`

// A term that chooses no repository is refused by name rather than dropped: a run that quietly
// ignored one would cover more than the reader asked for.
func TestStartingARunRefusesATermThatChoosesNoRepository(t *testing.T) {
	s, _ := newServer(t)

	for term, query := range map[string]string{
		"kinds": "kinds=workflow", "origins": "origins=seed", "minReach": "minReach=5",
		"ref": "ref=tag", "flags": "flags=findings",
	} {
		code, body := startRefusal(t, s, "rules=abom%2Fone&"+query)
		if code != http.StatusBadRequest {
			t.Errorf("%s answered %d starting a run, want 400", query, code)
		}
		if !bytes.Contains([]byte(body), []byte(term)) {
			t.Errorf("%s was refused without naming %q: %s", query, term, body)
		}
	}
}

// A run naming no query would analyze nothing while looking like work.
func TestARunNamingNoQueryIsRefused(t *testing.T) {
	s, _ := newServer(t)
	if _, code := start(t, s, "rules=actions%2F*", `{"queries":[],"by":"operator"}`); code != http.StatusBadRequest {
		t.Errorf("a run naming no query answered %d, want 400", code)
	}
}

// Asking for an expensive thing is an act somebody signs.
func TestARunWithNobodyNamedIsRefused(t *testing.T) {
	s, _ := newServer(t)
	body := `{"queries":[{"queryId":"actions/unpinned-tag"}],"by":""}`
	if _, code := start(t, s, "rules=actions%2F*", body); code != http.StatusBadRequest {
		t.Errorf("an unsigned run answered %d, want 400", code)
	}
}

// A scope nothing can answer is refused with the reason the resolution already worked out, rather
// than recorded as a run over nothing that will report a clean result.
func TestARunOverAScopeNothingCanAnswerIsRefusedWithTheReason(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.startRun(w, httptest.NewRequest("POST",
		"/api/runs?rules=actions%2F*&mode=extended", bytes.NewBufferString(oneQuery)))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("a run over an unanswerable scope answered %d, want 400", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("extended")) {
		t.Errorf("the refusal is %q, which does not say which shape cannot answer", w.Body.String())
	}
}

// A run that is not there is the reader naming one that does not exist. 5xx would say ABOM is
// broken and leave the page unable to name what would fix it.
func TestAnUnknownRunIsNotFoundRatherThanAFault(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/runs/999", nil)
	req.SetPathValue("id", "999")
	s.run(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("an unknown run answered %d, want 404", w.Code)
	}
}

// The list is what a reader chooses from, so it answers before anything has been asked for.
func TestTheRunListAnswersBeforeAnythingHasBeenAskedFor(t *testing.T) {
	s, _ := newServer(t)
	w := httptest.NewRecorder()
	s.runs(w, httptest.NewRequest("GET", "/api/runs", nil))
	if w.Code != 200 {
		t.Fatalf("the run list answered %d", w.Code)
	}
	var got struct {
		Runs []store.AnalysisRun `json:"runs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the run list: %v", err)
	}
	if got.Runs == nil {
		t.Error("runs marshaled as null rather than an empty list, which the client throws on")
	}
}

// fakeExecutor records that it was asked to carry a run out.
type fakeExecutor struct {
	mu    sync.Mutex
	asked []int64
}

func (f *fakeExecutor) Run(_ context.Context, runID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, runID)
	return nil
}

func (f *fakeExecutor) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.asked)
}

func startExecution(t *testing.T, s *Server, id string) int {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/runs/"+id+"/start", nil)
	req.SetPathValue("id", id)
	s.executeRun(w, req)
	return w.Code
}

// A run over a thousand repositories is minutes of CodeQL. Without a way to stop it a reader
// waits it out or kills the server, and killing the server leaves the run recorded as running.
func TestARunUnderWayCanBeStopped(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	executor := &blockingExecutor{started: make(chan struct{}), release: make(chan struct{})}
	s.Executor = executor

	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}
	id := strconv.FormatInt(run.RowID, 10)
	if got := startExecution(t, s, id); got != 200 {
		t.Fatalf("starting the run answered %d", got)
	}
	<-executor.started

	if got := cancelExecution(t, s, id, true); got != 200 {
		t.Fatalf("canceling answered %d, want 200", got)
	}

	close(executor.release)
	if err := <-executor.saw; err == nil {
		t.Error("the executor was not told to stop")
	}
}

// Only runs this process started. One left `running` by a server that died is not one this
// server can stop, and reporting a cancellation that never happened is worse than refusing.
func TestARunThisServerIsNotCarryingOutCannotBeStopped(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	s.Executor = &fakeExecutor{}

	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}

	got := cancelExecution(t, s, strconv.FormatInt(run.RowID, 10), true)
	if got != http.StatusConflict {
		t.Errorf("canceling a run nothing is carrying out answered %d, want 409", got)
	}
}

// Stopping a run undoes work somebody paid for, so it is guarded like the control that starts one.
func TestStoppingARunIsGuarded(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	s.Executor = &fakeExecutor{}

	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}

	got := cancelExecution(t, s, strconv.FormatInt(run.RowID, 10), false)
	if got != http.StatusForbidden {
		t.Errorf("an unguarded cancel answered %d, want 403", got)
	}
}

func cancelExecution(t *testing.T, s *Server, id string, guarded bool) int {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/runs/"+id+"/cancel", nil)
	if guarded {
		req.Header.Set("X-Abom-Control", "1")
	}
	// Control endpoints answer only over loopback, and httptest addresses the request to
	// example.com.
	req.Host = "127.0.0.1"
	req.SetPathValue("id", id)
	s.cancelRun(w, req)
	return w.Code
}

// blockingExecutor stays in Run until released, so a test can cancel something that is genuinely
// in flight rather than racing a call that has already returned.
type blockingExecutor struct {
	started chan struct{}
	release chan struct{}
	saw     chan error
	once    sync.Once
}

func (b *blockingExecutor) Run(ctx context.Context, _ int64) error {
	b.once.Do(func() { b.saw = make(chan error, 1) })
	close(b.started)
	<-b.release
	err := ctx.Err()
	b.saw <- err
	return err
}

// seedOneFinding gives the fixture a repository carrying a rule, so a scope resolves to something
// and a run can be recorded at all.
func seedOneFinding(t *testing.T, st *store.Store) {
	t.Helper()
	repoID, err := st.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	w := store.NewWriter(st, 0)
	if err := w.AddNode(store.Node{
		Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: repoID, Path: ".github/workflows/ci.yml",
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := st.AddFindings([]store.Finding{
		{RuleID: "actions/unpinned-tag", RepositoryID: repoID,
			FilePath: ".github/workflows/ci.yml", StartLine: 3},
	}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}
	if _, err := st.LinkFindingsToNodes(); err != nil {
		t.Fatalf("LinkFindingsToNodes: %v", err)
	}
}

// A server built without a runner can honestly record a run and not carry it out. Saying so is a
// precondition the reader has not met, which is a 4xx: reported as a fault it would say ABOM is
// broken and leave the page unable to name what would fix it.
func TestAServerWithNoRunnerSaysSoRatherThanFailing(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}

	if got := startExecution(t, s, strconv.FormatInt(run.RowID, 10)); got != http.StatusBadRequest {
		t.Errorf("starting a run with no runner answered %d, want 400", got)
	}
}

// Starting hands the work off and answers, because a run over a corpus outlives any request a
// browser is willing to hold open.
func TestStartingARunHandsItToTheExecutorAndAnswers(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	executor := &fakeExecutor{}
	s.Executor = executor

	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}
	if got := startExecution(t, s, strconv.FormatInt(run.RowID, 10)); got != 200 {
		t.Fatalf("starting the run answered %d", got)
	}

	// The handler detaches the work, so the assertion waits for it rather than racing it.
	for range 100 {
		if executor.count() > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("the run was never handed to the executor")
}

// Starting one that does not exist is the reader naming a run that is not there.
func TestStartingARunThatDoesNotExistIsNotFound(t *testing.T) {
	s, _ := newServer(t)
	s.Executor = &fakeExecutor{}
	if got := startExecution(t, s, "999"); got != http.StatusNotFound {
		t.Errorf("starting an unknown run answered %d, want 404", got)
	}
}

// Results are read one member at a time, so a reader can pick the ones to push back on. A member
// nothing ran against says so, and a term the listing does not apply is refused by name rather
// than answered with everything.
func TestARunsResultsAreReadOneMemberAtATime(t *testing.T) {
	s, st := newServer(t)
	seedOneFinding(t, st)
	run, code := start(t, s, "rules=actions%2F*", oneQuery)
	if code != 200 {
		t.Fatalf("recording a run answered %d", code)
	}
	base := "/api/runs/" + strconv.FormatInt(run.RowID, 10) + "/results"

	got := decode[store.MemberFindings](t, get(t, s, base+"?repository=octo-org/app"))
	if got.Evaluated || got.Findings == nil || len(got.Findings) != 0 {
		t.Errorf("a member nothing ran against reads %+v", got)
	}
	for _, c := range []struct {
		target string
		want   int
	}{
		{base, http.StatusBadRequest},
		{base + "?repository=octo-org/elsewhere", http.StatusNotFound},
		{"/api/runs/999/results?repository=octo-org/app", http.StatusNotFound},
		{base + "?repository=octo-org/app&severity=error", http.StatusBadRequest},
		{base + "?repository=octo-org/app&query=actions%2Funpinned-tag", http.StatusOK},
	} {
		if code := get(t, s, c.target).Code; code != c.want {
			t.Errorf("%s answered %d, want %d", c.target, code, c.want)
		}
	}
}

// seedSourcedFinding records one finding the crawl's lane reported through an upstream rule it
// kept a record of, and returns the finding's row.
func seedSourcedFinding(t *testing.T, st *store.Store) int64 {
	t.Helper()
	repoID, err := st.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := store.NewWriter(st, 0)
	if err := w.AddNode(store.Node{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
		RepositoryID: repoID, Path: ".github/workflows/ci.yml"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	lane, err := st.RecordQueryRun(store.QueryRun{QueryID: "abom-findings.qls", Pack: "codeql/actions-queries",
		Repository: "octo-org/app", DBCommit: "c1", AnalysisMode: store.AnalysisModeRegular,
		RecordedBy: store.RecordedByCrawl, Rules: []store.QueryRunRule{
			{RuleID: "actions/unpinned-tag", QueryVersion: "u1", Pack: "codeql/actions-queries"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFindings([]store.Finding{{RuleID: "actions/unpinned-tag", RepositoryID: repoID,
		QueryRunID: lane, FilePath: ".github/workflows/ci.yml", StartLine: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LinkFindingsToNodes(); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := st.DB().QueryRow(`SELECT row_id FROM abom_finding`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A finding is traced over the API to what reported it, and one the store does not hold is not
// found rather than answered empty.
func TestAFindingIsTracedToWhatReportedIt(t *testing.T) {
	s, st := newServer(t)
	id := seedSourcedFinding(t, st)

	got := decode[store.FindingTrace](t, get(t, s, "/api/findings/"+strconv.FormatInt(id, 10)))
	if len(got.Evaluations) != 1 || got.Evaluations[0].Source != store.QuerySourceUpstream ||
		got.Evaluations[0].RuleVersion != "u1" || got.Evaluations[0].RecordedBy != store.RecordedByCrawl {
		t.Errorf("the finding traces to %+v", got.Evaluations)
	}
	for target, want := range map[string]int{
		"/api/findings/999": http.StatusNotFound,
		"/api/findings/one": http.StatusBadRequest,
		"/api/findings/0":   http.StatusBadRequest,
	} {
		if code := get(t, s, target).Code; code != want {
			t.Errorf("%s answered %d, want %d", target, code, want)
		}
	}
}

// The source term reaches the set path with the store's meaning, and a source ABOM does not know
// is refused rather than matching nothing, which would read as "none of those".
func TestTheSetPathNarrowsBySourceOverTheAPI(t *testing.T) {
	s, st := newServer(t)
	seedSourcedFinding(t, st)
	repoID, err := st.UpsertRepository("octo-org/app", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFindings([]store.Finding{{RuleID: "actions/untrusted-checkout", RepositoryID: repoID,
		FilePath: ".github/workflows/ci.yml", StartLine: 5}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LinkFindingsToNodes(); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string][2]int{"source=upstream": {1, 1}, "source=custom": {0, 1}} {
		if got := decode[Match](t, get(t, s, "/api/match?"+query)); got.Findings != want[0] || got.Unsourced != want[1] {
			t.Errorf("%s matched %d findings with %d unsourced, want %v", query, got.Findings, got.Unsourced, want)
		}
	}
	if code := get(t, s, "/api/match?source=user").Code; code != http.StatusBadRequest {
		t.Errorf("an unknown source answered %d, want a refusal", code)
	}
}

// The listing narrows by source with the set path's meaning, which it can do only once its route
// declares the term: an undeclared one is refused before the handler reads it.
func TestTheActionsListingNarrowsBySource(t *testing.T) {
	s, st := newServer(t)
	seedGraph(t, st)
	id := seedSourcedFinding(t, st)
	if _, err := st.DB().Exec(`INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT ?, row_id FROM abom_node WHERE node_key = 'actions/checkout'`, id); err != nil {
		t.Fatal(err)
	}
	for query, want := range map[string]int{"source=upstream": 1, "source=custom": 0} {
		if got := decode[store.InventoryPage](t, get(t, s, "/api/actions?"+query)); got.Total != want {
			t.Errorf("/api/actions?%s listed %d rows, want %d", query, got.Total, want)
		}
	}
	if code := get(t, s, "/api/actions?source=user").Code; code != http.StatusBadRequest {
		t.Errorf("an unknown source answered %d, want a refusal", code)
	}
}
