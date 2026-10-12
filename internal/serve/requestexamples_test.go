package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	branchSHA = "1111111111111111111111111111111111111111"
	movedSHA  = "2222222222222222222222222222222222222222"
)

// fakeFiles answers refs from a map and files from another, counting what it was asked.
type fakeFiles struct {
	refs      map[string]string
	files     map[string]catalog.CommitFile
	refCalls  [][]catalog.RefQuestion
	fileCalls []string
}

func (f *fakeFiles) ResolveRefs(_ context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error) {
	f.refCalls = append(f.refCalls, qs)
	out := catalog.RefBatch{Answers: make([]catalog.RefAnswer, len(qs))}
	for i, q := range qs {
		if sha, ok := f.refs[q.Owner+"/"+q.Repo+"@"+q.Ref]; ok {
			out.Answers[i] = catalog.RefAnswer{Commit: sha}
		} else {
			out.Answers[i] = catalog.RefAnswer{Unread: "no such ref", Missing: true}
		}
	}
	return out, nil
}

func (f *fakeFiles) CommitFiles(_ context.Context, repository, commit string, paths []string) (catalog.FileBatch, error) {
	out := catalog.FileBatch{Files: map[string]catalog.CommitFile{}}
	for _, p := range paths {
		key := repository + "@" + commit + ":" + p
		f.fileCalls = append(f.fileCalls, key)
		file, ok := f.files[key]
		if !ok {
			file = catalog.CommitFile{Unread: "GitHub holds no file at " + p}
		}
		out.Files[p] = file
	}
	return out, nil
}

const ciLink = "https://github.com/octo/app/blob/feature/x/.github/workflows/ci.yml#L2-L3"

func filesServer(t *testing.T) (*Server, *fakeFiles) {
	t.Helper()
	s, _ := newServer(t)
	f := &fakeFiles{
		refs: map[string]string{"octo/app@feature/x": branchSHA},
		files: map[string]catalog.CommitFile{
			"octo/app@" + branchSHA + ":.github/workflows/ci.yml": {Text: "on: push\njobs:\n  a: {}\nend\n"},
			"octo/app@" + movedSHA + ":.github/workflows/ci.yml":  {Text: "moved\nmoved\nmoved\n"},
		},
	}
	s.Files = f
	return s, f
}

func linkedBody(t *testing.T, examples ...store.RequestExample) string {
	t.Helper()
	body, err := json.Marshal(store.QueryRequest{
		Description: "d", RequestedBy: "operator",
		Examples: append([]store.RequestExample{{Side: store.ExampleCompliant, Text: "runs-on: x"}},
			examples...),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func postRequest(s *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.saveQueryRequest(w, httptest.NewRequest("POST", "/api/query-requests", bytes.NewBufferString(body)))
	return w
}

// A linked example is read by the server at the commit its ref names, whatever text the client
// sent, and a ref holding a slash is found by asking every split in one question.
func TestALinkedExampleIsReadAtTheCommitItsRefNamesNow(t *testing.T) {
	s, f := filesServer(t)
	w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: ciLink,
		Text: "forged by the client"}))
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	got := readRequests(t, s).Requests[0].Examples[1]
	want := store.RequestExample{Side: store.ExampleNonCompliant, URL: ciLink, Text: "jobs:\n  a: {}\n",
		Repository: "octo/app", Path: ".github/workflows/ci.yml", Ref: "feature/x", Commit: branchSHA,
		StartLine: 2, EndLine: 3}
	if got != want {
		t.Errorf("kept %+v\nwant %+v", got, want)
	}
	if len(f.refCalls) != 1 || len(f.refCalls[0]) != 4 {
		t.Errorf("asked %v, want one question holding every split of the ref and path", f.refCalls)
	}
}

// ABOM never fetches an arbitrary address: only a GitHub blob link is read, and anything else is
// refused before GitHub is asked.
func TestOnlyAGitHubBlobLinkIsRead(t *testing.T) {
	s, f := filesServer(t)
	for _, link := range []string{
		"https://evil.example/octo/app/blob/main/ci.yml#L1",
		"http://github.com/octo/app/blob/main/ci.yml#L1",
		"https://github.com/octo/app/tree/main/ci.yml#L1",
		"https://github.com/octo/app/blob/main/ci.yml#readme",
		"https://github.com/octo/app/blob/main/ci.yml#L5-L2",
		"https://github.com/octo/app/blob/main/../ci.yml#L1",
		"https://github.com/octo/app/blob/main#L1",
		"https://user@github.com/octo/app/blob/main/ci.yml#L1",
	} {
		w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: link}))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s answered %d, want 400: %s", link, w.Code, w.Body.String())
		}
	}
	if len(f.refCalls) != 0 || len(f.fileCalls) != 0 {
		t.Errorf("refused links still asked GitHub: %v %v", f.refCalls, f.fileCalls)
	}
}

// A link naming no lines is the whole file, kept as every line it held at the commit it was read at.
func TestALinkNamingNoLinesReadsTheWholeFile(t *testing.T) {
	s, _ := filesServer(t)
	link := strings.TrimSuffix(ciLink, "#L2-L3")
	w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: link}))
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	got := readRequests(t, s).Requests[0].Examples[1]
	if got.Text != "on: push\njobs:\n  a: {}\nend\n" || got.StartLine != 1 || got.EndLine != 4 || got.Commit != branchSHA {
		t.Errorf("kept %+v", got)
	}
}

// A whole file too long to be one example is refused saying how to narrow it.
func TestAWholeFileTooLongForAnExampleAsksForLines(t *testing.T) {
	s, f := filesServer(t)
	f.files["octo/app@"+branchSHA+":big.yml"] = catalog.CommitFile{
		Text: strings.Repeat("x\n", maxExampleLines+1)}
	w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant,
		URL: "https://github.com/octo/app/blob/feature/x/big.yml"}))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "#L10-L20") {
		t.Errorf("answered %d: %s", w.Code, w.Body.String())
	}
}

// An edit keeps a linked example at the commit it was read at: re-resolving the branch would change
// a test's fixture without anybody choosing to.
func TestAnEditRereadsAPinnedExampleAtItsCommitNotItsBranch(t *testing.T) {
	s, f := filesServer(t)
	if w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: ciLink})); w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	kept := readRequests(t, s).Requests[0]
	f.refs["octo/app@feature/x"] = movedSHA
	f.refCalls = nil

	body, _ := json.Marshal(kept)
	edit := httptest.NewRequest("PUT", "/api/query-requests/1", bytes.NewBuffer(body))
	edit.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	s.editQueryRequest(w, edit)
	if w.Code != http.StatusOK {
		t.Fatalf("an edit answered %d: %s", w.Code, w.Body.String())
	}
	if got := readRequests(t, s).Requests[0].Examples[1]; got.Commit != branchSHA || got.Text != "jobs:\n  a: {}\n" {
		t.Errorf("an edit moved the example to %s: %q", got.Commit, got.Text)
	}
	if len(f.refCalls) != 0 {
		t.Errorf("an edit resolved the branch again: %v", f.refCalls)
	}
}

// A link GitHub cannot answer for is refused with GitHub's reason, never kept as an empty example.
func TestALinkedExampleThatCannotBeReadIsRefusedSayingWhy(t *testing.T) {
	s, _ := filesServer(t)
	for link, says := range map[string]string{
		"https://github.com/octo/app/blob/feature/x/.github/workflows/ci.yml#L3-L9": "4 lines",
		"https://github.com/octo/app/blob/feature/x/missing.yml#L1":                 "no file",
		"https://github.com/octo/app/blob/nowhere/ci.yml#L1":                        "no branch, tag or commit",
	} {
		w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: link}))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), says) {
			t.Errorf("%s answered %d, want 400 saying %q: %s", link, w.Code, says, w.Body.String())
		}
	}
	s.Files = nil
	w := postRequest(s, linkedBody(t, store.RequestExample{Side: store.ExampleNonCompliant, URL: ciLink}))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "paste") {
		t.Errorf("with no reader a link answered %d: %s", w.Code, w.Body.String())
	}
}

// The form shows what a link reads as before anything is kept.
func TestALinkedExampleCanBeReadBeforeItIsKept(t *testing.T) {
	s, _ := filesServer(t)
	w := httptest.NewRecorder()
	s.readRequestExample(w, boundaryRequest(http.MethodPost, "/api/query-requests/example",
		`{"side":"non-compliant","url":"`+ciLink+`"}`, true))
	if w.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", w.Code, w.Body.String())
	}
	var got store.RequestExample
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Commit != branchSHA || got.Text != "jobs:\n  a: {}\n" {
		t.Errorf("read %+v", got)
	}
	if n := len(readRequests(t, s).Requests); n != 0 {
		t.Errorf("reading an example kept %d requests", n)
	}
}
