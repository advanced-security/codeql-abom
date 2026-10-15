package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/audit"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestReproductionLabelsRecordedEmptyPolicyBoundaries(t *testing.T) {
	var body bytes.Buffer
	g := &auditGuide{w: &body}
	g.policy(&audit.Policy{TimeoutMs: 600000, CreditsKnown: true})
	for _, wanted := range []string{
		"No AI credit cap (Copilot plan limits still apply)", "No editable files in this policy.",
		"No query hold recorded in this policy.", "| Denied scope | <code>No entries recorded</code> |",
	} {
		if !strings.Contains(rendered(body.String()), wanted) {
			t.Errorf("recorded empty policy boundary missing %q", wanted)
		}
	}
	// The cap is Copilot's on one session; the product's own name explains nothing about it.
	if strings.Contains(body.String(), "ABOM") {
		t.Errorf("the guide names ABOM in a policy it did not set:\n%s", body.String())
	}
}

// rendered is the guide as a Markdown viewer shows its text: every backslash escape resolved.
func rendered(markdown string) string {
	return regexp.MustCompile("\\\\([!-/:-@\\[-`{-~])").ReplaceAllString(markdown, "$1")
}

// What an agent writes reaches the downloaded guide verbatim. Opened in a Markdown viewer it drew
// the agent's links as recorded evidence and loaded its images, whose address carries out whatever
// the agent put in it; the guide's own links are the only ones it may hold.
func TestRecordedTextCannotFormLinksOrImagesInTheGuide(t *testing.T) {
	s, st, _, request := authoringServer(t)
	session, err := st.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	said := strings.Join([]string{
		"![status](https://attacker.example/x/leak.png)",
		"[Open the passing test log](https://attacker.example/login)",
		`\[Approve\](https://attacker.example/p)`,
		"<img src=https://attacker.example/i.png>",
		"<https://attacker.example/auto>",
		"www.attacker.example/www",
		"*bold* _under_ `tick` $math$ | pipe",
	}, " ")
	detail, err := json.Marshal(map[string]any{"message": said, "[key](https://attacker.example/k)": said})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z",
		Hook: "message", Tool: said, Subject: said, Reason: said, Decision: "refused", Detail: string(detail),
		Capture: store.AuditCapture{Version: 1, Inputs: map[string]string{said: said},
			Test: &store.QueryTest{QueryID: said, QueryVersion: said, Total: 1, Failed: 1, FailureStage: "RESULT", Detail: said}},
	}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-sessions/%d/reproduction?download=1", session), nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "<!-- audit-complete:") {
		t.Fatalf("guide: %d\n%s", w.Code, body)
	}
	for _, live := range []string{"https://attacker.example", "www.attacker.example", "![", "*bold*", "_under_", "`tick`", "$math$"} {
		if strings.Contains(body, live) {
			t.Errorf("the guide holds %q as Markdown, not text", live)
		}
	}
	// `\<` is a literal angle bracket; only one no backslash escapes opens a tag or an autolink.
	if tag := regexp.MustCompile(`(^|[^\\])<(img|https)`).FindString(body); tag != "" {
		t.Errorf("the guide holds %q as HTML or an autolink, not text", tag)
	}
	for _, at := range regexp.MustCompile(`\]\(`).FindAllStringIndex(body, -1) {
		if rest := body[at[1]:]; !strings.HasPrefix(rest, "/codeql?") && !strings.HasPrefix(rest, "/api/") {
			t.Errorf("a link the guide did not write: %q", body[max(0, at[0]-40):min(len(body), at[1]+40)])
		}
	}
	// Escaped, not dropped: a reader still sees exactly what was recorded.
	if text := rendered(body); !strings.Contains(text, "https://attacker.example/x/leak.png") || !strings.Contains(text, "| pipe") {
		t.Errorf("the guide no longer shows what was recorded:\n%s", text)
	}
}

func TestReproductionIsAHumanGuideWithEveryMeasuredIteration(t *testing.T) {
	s, st, _, request := authoringServer(t)
	session, err := st.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, passed := range []bool{false, true} {
		test := &store.QueryTest{QueryID: "custom/check", QueryVersion: "version-123", CLIVersion: "2.27.2", Total: 3, Passed: passed, TestDigest: "test-hash", FixturesDigest: "fixture-hash"}
		if !passed {
			test.Failed, test.FailureStage = 1, "RESULT"
		}
		_, err = st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z", Hook: "test-evidence", Tool: "abom_test", Decision: "noted",
			Capture: store.AuditCapture{Version: 1, Test: test, Inputs: map[string]string{"expected": "expected-hash"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for range 503 {
		if _, err := st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z", Hook: "pre-tool-use", Tool: "view", Subject: "test/Check", Decision: "allowed", Capture: store.AuditCapture{Version: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-sessions/%d/reproduction?download=1", session), nil))
	body := w.Body.String()
	for _, wanted := range []string{"## Session and limits", "## Manual reproduction checklist", "## Required inputs", "## Measured test iterations", "## Recorded steps and test iterations",
		"Result comparison failed", "2 of 3 passed", "3 of 3 passed", "CodeQL CLI", "fixture-hash", "expected-hash", "events=505"} {
		if !strings.Contains(rendered(body), wanted) {
			t.Errorf("guide does not contain %q", wanted)
		}
	}
	if strings.Contains(body, "<pre>") || strings.Contains(body, `"policies":`) {
		t.Fatal("the primary guide is still a JSON dump")
	}
}

func TestAuditSearchAndExportIncludeTheWholeFilteredSnapshot(t *testing.T) {
	s, st, _, request := authoringServer(t)
	session, err := st.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 605; i++ {
		_, err := st.RecordAuthoringEvent(store.AuthoringEvent{SessionID: session, At: "2026-10-01T00:00:00Z", Hook: "pre-tool-use", Tool: "view", Decision: "allowed"})
		if err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-events?session=%d", session), nil))
	var page store.AuditPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Total != 605 || len(page.Items) != 100 {
		t.Fatalf("listing: %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-events/export?session=%d&tool=view", session), nil))
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if w.Code != 200 || len(lines) != 607 || !strings.Contains(lines[len(lines)-1], `"complete":true`) {
		t.Fatalf("export: status=%d records=%d", w.Code, len(lines))
	}
	for _, path := range []string{"/api/authoring-events?limit=501", "/api/authoring-events?unknown=x", "/api/authoring-events?from=garbage", "/api/authoring-events?purpose=other"} {
		w = httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localRequest("GET", path, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest("GET", "/api/authoring-sessions/9999/events", nil))
	if w.Code != 404 {
		t.Fatalf("missing session: %d", w.Code)
	}
}

// A route names its session, so a request, run or other session in the query could only narrow it
// silently to nothing; it is refused instead of answering with an empty page that reads as no events.
func TestASessionRouteRefusesTermsThatWouldNarrowItsSession(t *testing.T) {
	s, st, _, request := authoringServer(t)
	session, err := st.StartAuthoringSession(request, "operator", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"events", "reproduction"} {
		for _, term := range []string{"request=999", "run=999", fmt.Sprintf("session=%d", session+1)} {
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-sessions/%d/%s?%s", session, route, term), nil))
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "route") {
				t.Errorf("%s?%s: %d %s", route, term, w.Code, w.Body)
			}
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localRequest("GET", fmt.Sprintf("/api/authoring-sessions/%d/%s?session=%d", session, route, session), nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s naming its own session: %d %s", route, w.Code, w.Body)
		}
	}
}

// The status comes from what the store says the error is, not from its wording: a failure that
// mentions a cursor is still the server's, and a refused filter is the reader's however it is worded.
func TestAuditErrorsAreClassifiedByKindNotWording(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{fmt.Errorf("reading the cursor table: %w", errors.New("disk I/O error")), http.StatusInternalServerError},
		{fmt.Errorf("%w: page size out of range", store.ErrBadAuditFilter), http.StatusBadRequest},
		{fmt.Errorf("looking up: %w", store.ErrNoAuditSession), http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		auditError(w, c.err)
		if w.Code != c.want {
			t.Errorf("%v: %d, want %d", c.err, w.Code, c.want)
		}
	}
}

func TestAuditDownloadsRespectDemoProhibition(t *testing.T) {
	s, _, _, _ := authoringServer(t)
	s.Instance.Mode = ModeDemo
	for _, path := range []string{"/api/authoring-events/export", "/api/authoring-sessions/1/reproduction?download=1"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localRequest("GET", path, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}
