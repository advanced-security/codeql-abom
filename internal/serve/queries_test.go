package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type catalogAnswer struct {
	Queries []store.CatalogQuery `json:"queries"`
	Stages  []string             `json:"stages"`
}

func readCatalog(t *testing.T, s *Server) catalogAnswer {
	t.Helper()
	w := httptest.NewRecorder()
	s.queries(w, httptest.NewRequest("GET", "/api/queries", nil))
	if w.Code != 200 {
		t.Fatalf("/api/queries answered %d: %s", w.Code, w.Body.String())
	}
	var got catalogAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding the catalog: %v", err)
	}
	return got
}

func advance(t *testing.T, s *Server, body string) int {
	t.Helper()
	w := httptest.NewRecorder()
	s.advanceQuery(w, httptest.NewRequest("POST", "/api/queries/stage", bytes.NewBufferString(body)))
	return w.Code
}

// A page cannot offer a lifecycle it has to hard-code. The stages travel with the catalog so the
// interface and the store cannot come to disagree about what the lifecycle is.
func TestTheCatalogCarriesTheLifecycleItIsStagedAgainst(t *testing.T) {
	s, _ := newServer(t)
	got := readCatalog(t, s)
	if len(got.Stages) == 0 {
		t.Error("the catalog names no stages, so a reader would have to invent the lifecycle")
	}
	if got.Queries == nil {
		t.Error("queries marshaled as null rather than an empty list, which the client throws on")
	}
}

// The refusal belongs at the boundary: a version in a state nothing downstream reads is cheapest
// to catch before it is stored.
func TestAdvancingToAStageOutsideTheLifecycleIsRefused(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s,
		`{"queryId":"abom/edges","queryVersion":"v1","stage":"shipped","by":"operator"}`); code != http.StatusBadRequest {
		t.Errorf("a stage outside the lifecycle answered %d, want 400", code)
	}
}

// Advancement is an act somebody signs, so one with nobody named is refused rather than recorded
// against an anonymous author.
func TestAdvancingWithNobodyNamedIsRefused(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s,
		`{"queryId":"abom/edges","queryVersion":"v1","stage":"test","by":""}`); code != http.StatusBadRequest {
		t.Errorf("an unsigned advance answered %d, want 400", code)
	}
}

// A malformed body is an error rather than a silently ignored write, or a caller would believe it
// had staged something it had not.
func TestAMalformedAdvanceIsAnErrorRatherThanANoOp(t *testing.T) {
	s, _ := newServer(t)
	if code := advance(t, s, "not json"); code != http.StatusBadRequest {
		t.Errorf("a malformed advance answered %d, want 400", code)
	}
}

// fakeTester answers like `codeql test run`: the run fails if any test it was handed fails, at the
// earliest stage any of them stopped.
type fakeTester struct {
	failing map[string]string // test file name -> the stage it stops at
	ran     [][]string
	during  func()
}

func (f *fakeTester) RunTests(_ context.Context, tests []string) (runner.TestResult, error) {
	f.ran = append(f.ran, tests)
	if f.during != nil {
		f.during()
	}
	out := runner.TestResult{}
	for _, test := range tests {
		out.Total++
		if stage, failing := f.failing[filepath.Base(test)]; failing {
			out.Failed++
			out.FailureStage = stage
		}
	}
	out.Passed = out.Failed == 0
	out.Detail = fmt.Sprintf("%d of %d passed", out.Total-out.Failed, out.Total)
	return out, nil
}

// queryWorkspace scaffolds an operator's pack holding one query per name, each tested by its own
// directory unless it is listed as untested.
func queryWorkspace(t *testing.T, names []string, untested ...string) string {
	t.Helper()
	root := t.TempDir()
	if _, err := customql.Scaffold(root, "2.27.1"); err != nil {
		t.Fatal(err)
	}
	put := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		put("src/"+name+".ql", "/**\n * @kind problem\n * @id acme/"+strings.ToLower(name)+
			"\n */\n\nimport actions\n\nfrom Job job\nselect job, \"a job\"\n")
		if slices.Contains(untested, name) {
			continue
		}
		put("test/"+name+"/"+name+".qlref", "src/"+name+".ql\n")
		put("test/"+name+"/"+name+".expected", "| a job |\n")
		put("test/"+name+"/.github/workflows/ci.yml", "on: push\n")
	}
	return root
}

func versionOf(t *testing.T, root, name string) string {
	t.Helper()
	version, err := runner.QueryVersion(filepath.Join(root, "src", name+".ql"))
	if err != nil {
		t.Fatal(err)
	}
	return version
}

func runQueryTest(t *testing.T, s *Server, id, version string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"queryId": id, "queryVersion": version})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/queries/test", bytes.NewReader(body))
	r.Host = "127.0.0.1:8080"
	r.Header.Set(controlHeader, "1")
	w := httptest.NewRecorder()
	s.testQuery(w, r)
	return w
}

// A query is written and tested before anything runs it, so the catalog lists it from the moment
// it is on disk, marked as the version the steps describe.
func TestTheCatalogListsTheWorkingCopyBeforeAnythingHasRun(t *testing.T) {
	s, _ := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	s.Tester, s.QueryDir = &fakeTester{}, root

	var got []store.CatalogQuery
	for _, q := range readCatalog(t, s).Queries {
		if q.QueryID == "acme/a" {
			got = append(got, q)
		}
	}
	if len(got) != 1 || got[0].QueryVersion != versionOf(t, root, "A") || !got[0].Working ||
		got[0].Origin != store.QueryOriginUser {
		t.Errorf("the catalog lists acme/a as %+v, want its working copy once", got)
	}
}

// A mark is served, never computed in the browser, and appears only where its step applies: on
// the working copy of a query the operator owns.
func TestOnlyTheWorkingCopyCarriesItsSteps(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	s.Tester, s.QueryDir = &fakeTester{}, root
	if _, err := st.RecordQueryRun(store.QueryRun{
		AnalysisMode: store.AnalysisModeRegular, QueryID: "actions/unpinned-tag",
		QueryVersion: "u1", Pack: "codeql/actions-queries", Repository: "octo/app",
		DBCommit: "sha-one",
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.queries(w, httptest.NewRequest("GET", "/api/queries", nil))
	var got struct {
		Queries []struct {
			store.CatalogQuery
			Steps []authoring.Step `json:"steps"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
	if len(got.Queries) != 2 {
		t.Fatalf("listed %d queries, want the working copy and the upstream one", len(got.Queries))
	}
	for _, q := range got.Queries {
		switch {
		case q.Steps == nil:
			t.Errorf("%s carries steps: null, which the client throws on", q.QueryID)
		case q.QueryID == "acme/a" && len(q.Steps) != 6:
			t.Errorf("the working copy carries %d steps, want (b) through (g)", len(q.Steps))
		case q.QueryID != "acme/a" && len(q.Steps) != 0:
			t.Errorf("%s carries steps for tests ABOM does not hold", q.QueryID)
		}
	}
}

// One query's failing test must not fail another. Grading every query by the whole test tree
// turned a neighbor's broken fixture into this query's red mark.
func TestAQueryIsGradedOnlyByItsOwnTests(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A", "B"})
	tester := &fakeTester{failing: map[string]string{"B.qlref": "RESULT"}}
	s.Tester, s.QueryDir = tester, root

	for _, name := range []string{"A", "B"} {
		id := "acme/" + strings.ToLower(name)
		if w := runQueryTest(t, s, id, versionOf(t, root, name)); w.Code != http.StatusOK {
			t.Fatalf("testing %s answered %d: %s", id, w.Code, w.Body.String())
		}
	}

	want := [][]string{
		{filepath.Join(root, "test", "A", "A.qlref")},
		{filepath.Join(root, "test", "B", "B.qlref")},
	}
	if !reflect.DeepEqual(tester.ran, want) {
		t.Errorf("ran %v, want each query's own test and nothing else: %v", tester.ran, want)
	}
	a, _, err := st.QueryTestFor("acme/a", versionOf(t, root, "A"))
	if err != nil || !a.Passed {
		t.Errorf("acme/a reads passed=%v (err %v) beside a failing neighbor", a.Passed, err)
	}
	b, _, err := st.QueryTestFor("acme/b", versionOf(t, root, "B"))
	if err != nil || b.Passed {
		t.Errorf("acme/b reads passed=%v (err %v) with its own test failing", b.Passed, err)
	}
}

// What the steps are read from travels with the verdict: where it stopped, and which tests those
// were, so a later edit to them reads as stale.
func TestATestResultKeepsWhereItStoppedAndWhichTestsRan(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	s.Tester, s.QueryDir = &fakeTester{failing: map[string]string{"A.qlref": "COMPILATION"}}, root

	version := versionOf(t, root, "A")
	if w := runQueryTest(t, s, "acme/a", version); w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	digest, err := authoring.DigestTests(root, []string{filepath.Join(root, "test", "A", "A.qlref")})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := st.QueryTestFor("acme/a", version)
	if err != nil {
		t.Fatal(err)
	}
	if got.FailureStage != "COMPILATION" || got.TestDigest != digest {
		t.Errorf("recorded stage %q and digest %q, want COMPILATION and %q", got.FailureStage,
			got.TestDigest, digest)
	}
}

// The version on the page is the one the operator meant to test. Testing whatever is on disk now
// and filing it under an older version would vouch for a query nobody ran.
func TestTestingAVersionThatIsNoLongerOnDiskIsRefused(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	tester := &fakeTester{}
	s.Tester, s.QueryDir = tester, root

	if w := runQueryTest(t, s, "acme/a", "an-older-version"); w.Code != http.StatusConflict {
		t.Errorf("a stale version answered %d, want 409: %s", w.Code, w.Body.String())
	}
	if len(tester.ran) != 0 {
		t.Errorf("the tests ran for a version that is not on disk: %v", tester.ran)
	}
	if _, known, _ := st.QueryTestFor("acme/a", "an-older-version"); known {
		t.Error("a result was recorded against a version nobody tested")
	}
}

// A test run takes seconds, and an agent or an operator can edit in the meantime. The result then
// describes neither the version listed nor the one on disk, so it is filed under neither.
func TestAQueryEditedWhileItsTestsRanIsNotRecorded(t *testing.T) {
	for _, rel := range []string{"src/A.ql", "test/A/A.expected"} {
		s, st := newServer(t)
		root := queryWorkspace(t, []string{"A"})
		version := versionOf(t, root, "A")
		s.Tester, s.QueryDir = &fakeTester{during: func() {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.WriteFile(path, []byte("// edited mid-run\n"), 0o644); err != nil {
				t.Error(err)
			}
		}}, root

		if w := runQueryTest(t, s, "acme/a", version); w.Code != http.StatusConflict {
			t.Errorf("editing %s mid-run answered %d, want 409: %s", rel, w.Code, w.Body.String())
		}
		if _, known, _ := st.QueryTestFor("acme/a", version); known {
			t.Errorf("editing %s mid-run still filed a result under the version listed", rel)
		}
	}
}

// Only the operator's own queries have tests ABOM can run; a shipped one is tested where it ships.
func TestOnlyAQueryInTheWorkspaceCanBeTested(t *testing.T) {
	s, _ := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	tester := &fakeTester{}
	s.Tester, s.QueryDir = tester, root

	if w := runQueryTest(t, s, "actions/unpinned-tag", "v1"); w.Code != http.StatusNotFound {
		t.Errorf("a query outside the workspace answered %d, want 404: %s", w.Code, w.Body.String())
	}
	if len(tester.ran) != 0 {
		t.Errorf("something ran for a query outside the workspace: %v", tester.ran)
	}
}

// Nothing testing a query is recorded as showing nothing, not handed to the CLI, which would run
// every test it could find instead.
func TestAQueryNothingTestsIsRecordedAsShowingNothing(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"}, "A")
	tester := &fakeTester{}
	s.Tester, s.QueryDir = tester, root

	version := versionOf(t, root, "A")
	if w := runQueryTest(t, s, "acme/a", version); w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	if len(tester.ran) != 0 {
		t.Errorf("the CLI was handed %v for a query nothing tests", tester.ran)
	}
	got, known, err := st.QueryTestFor("acme/a", version)
	if err != nil || !known {
		t.Fatalf("nothing recorded: known=%v err=%v", known, err)
	}
	if got.Passed || got.Total != 0 || !strings.Contains(got.Detail, "src/A.ql") {
		t.Errorf("recorded %+v, want a result that is not a pass and names the query", got)
	}
}

func advanceTo(t *testing.T, s *Server, id, version, stage string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"queryId": id, "queryVersion": version, "stage": stage, "by": "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.advanceQuery(w, httptest.NewRequest("POST", "/api/queries/stage", bytes.NewReader(body)))
	return w
}

// extended-test is earned the way test is. A version whose steps have not all passed is refused,
// and the refusal names the first step that has not, which is what the reader does next.
func TestExtendedTestIsRefusedUntilEveryStepPasses(t *testing.T) {
	s, st := newServer(t)
	root := queryWorkspace(t, []string{"A"})
	s.QueryDir = root
	version := versionOf(t, root, "A")

	w := advanceTo(t, s, "acme/a", version, store.StageExtendedTest)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Query help") {
		t.Errorf("with nothing done, answered %d: %s", w.Code, w.Body.String())
	}
	cat := readCatalog(t, s)
	for _, q := range cat.Queries {
		if q.QueryID == "acme/a" && q.Stage != "" {
			t.Errorf("a refused advance left the version at %q", q.Stage)
		}
	}

	// Every other step done, and the AST one still waiting for PrintAST.
	if err := os.WriteFile(filepath.Join(root, "src", "A.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testproj := filepath.Join(root, "test", "A", "A.testproj")
	if err := os.MkdirAll(testproj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testproj, "codeql-database.yml"),
		[]byte("finalised: true\n"), 0o644); err != nil { //nolint:misspell // the CLI's key
		t.Fatal(err)
	}
	digest, err := authoring.DigestTests(root, []string{filepath.Join(root, "test", "A", "A.qlref")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordQueryTest(store.QueryTest{QueryID: "acme/a", QueryVersion: version,
		Passed: true, Total: 1, Detail: "1 of 1 passed", TestDigest: digest}); err != nil {
		t.Fatal(err)
	}
	w = advanceTo(t, s, "acme/a", version, store.StageExtendedTest)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "AST") {
		t.Errorf("with only the AST step waiting, answered %d: %s", w.Code, w.Body.String())
	}
}

// Only the version on disk has steps to show, so no other can have earned them.
func TestExtendedTestIsRefusedForAVersionThatIsNotOnDisk(t *testing.T) {
	s, _ := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})

	if w := advanceTo(t, s, "acme/a", "an-older-version", store.StageExtendedTest); w.Code != http.StatusBadRequest {
		t.Errorf("a version not on disk answered %d: %s", w.Code, w.Body.String())
	}
	if w := advanceTo(t, s, "actions/unpinned-tag", "u1", store.StageExtendedTest); w.Code != http.StatusBadRequest {
		t.Errorf("a query outside the workspace answered %d: %s", w.Code, w.Body.String())
	}
}
