package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/publish"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// pullRequests stands in for the publisher App, recording what it was asked to write.
type pullRequests struct {
	refuse  error
	reused  bool
	opened  []publish.Export
	written []string
}

func (p *pullRequests) Target(context.Context) (publish.Target, error) {
	return publish.Target{Owner: "acme", Repo: "queries"}, p.refuse
}

func (p *pullRequests) Open(_ context.Context, _ publish.Target, e publish.Export) (string, bool, error) {
	p.opened = append(p.opened, e)
	for _, f := range e.Files {
		p.written = append(p.written, f.Path)
	}
	return "https://github.com/acme/queries/pull/7", p.reused, nil
}

// reviewed is the bundle digest the components show now, which is what you review before exporting.
func reviewed(t *testing.T, s *Server, query string) string {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/queries/components?query="+query, nil))
	var m struct {
		BundleSHA256 string `json:"bundleSha256"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.BundleSHA256 == "" {
		t.Fatalf("reading %s's components: %d %s", query, w.Code, w.Body.String())
	}
	return m.BundleSHA256
}

func exportRequest(query, bundle string) *http.Request {
	r := localRequest(http.MethodPost, "/api/queries/export?query="+query+"&bundle="+bundle, nil)
	r.Header.Set(controlHeader, "1")
	return r
}

// An export opens one pull request holding everything that makes the query run and be tested, never
// a test's own output, and a record of the decision and the digests it was made over; the
// decision is signed before anything is written.
func TestAnExportOpensAPullRequestOfTheQueryAndItsDecision(t *testing.T) {
	s, st, dir := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	if err := os.WriteFile(filepath.Join(s.QueryDir, "test", "A", "A.actual"), []byte("output"), 0o644); err != nil {
		t.Fatal(err)
	}
	pr := &pullRequests{}
	s.Publisher = pr

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", reviewed(t, s, "acme/a")))
	if w.Code != http.StatusOK {
		t.Fatalf("export answered %d: %s", w.Code, w.Body.String())
	}
	var answer struct {
		URL, Target, Decision, Bundle string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || answer.URL != "https://github.com/acme/queries/pull/7" ||
		answer.Target != "acme/queries" || answer.Decision == "" || answer.Bundle == "" {
		t.Fatalf("answer = %+v, %v", answer, err)
	}
	if !slices.Contains(pr.written, "src/A.ql") || !slices.Contains(pr.written, "test/A/A.qlref") {
		t.Errorf("wrote %v; want the query and its test", pr.written)
	}
	if slices.ContainsFunc(pr.written, func(p string) bool { return strings.HasSuffix(p, ".actual") }) {
		t.Errorf("wrote %v; a test's output is not part of the query", pr.written)
	}
	if !slices.ContainsFunc(pr.written, func(p string) bool { return strings.HasPrefix(p, "abom-exports/") }) {
		t.Errorf("wrote %v; want the export's record", pr.written)
	}
	body := pr.opened[0].Body
	if !strings.Contains(body, answer.Decision) || !strings.Contains(body, answer.Bundle) {
		t.Errorf("the pull request does not cite its decision and bundle:\n%s", body)
	}

	d, seq := headDecision(t, st, dir)
	if d.Operation() != authz.OpExportQuery || !d.Allowed() || d.Digest() != answer.Decision ||
		!strings.Contains(string(d.Evidence()), "acme/queries") || !strings.Contains(string(d.Evidence()), answer.Bundle) {
		t.Errorf("logged %s %s: %s; want the export decided over its target and bundle", d.Operation(), d.Digest(), d.Evidence())
	}
	// The route decision that admitted the export is the one before it, and the export cites it.
	before, err := st.Assertions(seq-2, 1)
	if err != nil || len(before) != 1 || !strings.Contains(string(d.Evidence()), `"route":"`+before[0].Digest+`"`) {
		t.Errorf("the export's evidence %s does not cite the route decision before it (%v)", d.Evidence(), err)
	}
}

// A publisher that is not exactly one repository with exactly its permissions refuses the export
// before anything is written, with its reason; one GitHub cannot be asked about is GitHub's failure,
// not a misinstall; and with no publisher there is nothing to export to.
func TestAnExportIsRefusedWhenThePublisherIsNotExactlyOneRepository(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	pr := &pullRequests{refuse: &publish.Misinstalled{Reason: "the publisher App is installed on 2 repositories of acme; it may write to one only"}}
	s.Publisher = pr

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", reviewed(t, s, "acme/a")))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "2 repositories") || len(pr.opened) != 0 {
		t.Errorf("a misinstalled publisher answered %d: %s, opened %d", w.Code, w.Body.String(), len(pr.opened))
	}

	// Not being able to ask GitHub is not a misinstall: it is GitHub's answer, and says so.
	pr.refuse = errors.New("Get \"https://api.github.com/app/installations\": dial tcp: i/o timeout")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", reviewed(t, s, "acme/a")))
	if p := problemOf(t, w); p.Code != problemUpstream || !strings.Contains(p.Upstream, "i/o timeout") || len(pr.opened) != 0 {
		t.Errorf("an unreadable publisher answered %+v, opened %d", p, len(pr.opened))
	}

	s.Publisher = nil
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", reviewed(t, s, "acme/a")))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "app create --publisher") {
		t.Errorf("no publisher answered %d: %s", w.Code, w.Body.String())
	}
}

// A component changed since it was read is not exported as the digest that was decided.
func TestAComponentChangedSinceItWasReadIsNotExported(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	pr := &pullRequests{}
	changes := &changing{pullRequests: pr, path: filepath.Join(s.QueryDir, "src", "A.ql")}
	s.Publisher = changes
	bundle := reviewed(t, s, "acme/a")
	changes.armed = true

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", bundle))
	if w.Code != http.StatusConflict || len(pr.opened) != 0 {
		t.Errorf("a component changed mid-export answered %d: %s, opened %d", w.Code, w.Body.String(), len(pr.opened))
	}
}

// changing rewrites a component once armed, while an export reads the target: after the files
// were reviewed and read, before they are written.
type changing struct {
	*pullRequests
	path  string
	armed bool
}

func (c *changing) Target(ctx context.Context) (publish.Target, error) {
	if c.armed {
		_ = os.WriteFile(c.path, []byte("select 2\n"), 0o644)
	}
	return c.pullRequests.Target(ctx)
}

// The components say whether this server can export them, so a reader is offered the action only
// where a publisher is configured; whether GitHub agrees is asked when it is taken.
func TestTheComponentsSayWhetherTheyCanBeExported(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	misinstalled := &publish.Misinstalled{Reason: "the publisher App is installed on 2 repositories of acme; it may write to one only"}
	for name, c := range map[string]struct {
		publisher          Publisher
		exportable         bool
		target, blockedHas string
	}{
		"no publisher":       {nil, false, "", ""},
		"one repository":     {&pullRequests{}, true, "acme/queries", ""},
		"installed too wide": {&pullRequests{refuse: misinstalled}, true, "", "2 repositories"},
		// GitHub not answering now is said when the export is pressed, not assumed here.
		"github unreachable": {&pullRequests{refuse: errors.New("dial tcp: i/o timeout")}, true, "", ""},
	} {
		s.Publisher = c.publisher
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, localRequest(http.MethodGet, "/api/queries/components?query=acme/a", nil))
		var got struct {
			Exportable    *bool  `json:"exportable"`
			ExportTarget  string `json:"exportTarget"`
			ExportBlocked string `json:"exportBlocked"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Exportable == nil || *got.Exportable != c.exportable ||
			got.ExportTarget != c.target || (c.blockedHas == "") != (got.ExportBlocked == "") ||
			!strings.Contains(got.ExportBlocked, c.blockedHas) {
			t.Errorf("%s: components = %d %s", name, w.Code, w.Body.String())
		}
	}
}

// An export sends the bundle you reviewed or nothing: one whose files changed since you read its
// components would go out under your decision with content you never saw.
func TestAnExportOfAnythingButTheReviewedBundleIsRefused(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	pr := &pullRequests{}
	s.Publisher = pr
	seen := reviewed(t, s, "acme/a")
	query := filepath.Join(s.QueryDir, "src", "A.ql")
	body, err := os.ReadFile(query)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(query, append(body, "// changed after review\n"...), 0o644); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", seen))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "since you reviewed") || len(pr.opened) != 0 {
		t.Errorf("exporting a changed bundle answered %d: %s, opened %d", w.Code, w.Body.String(), len(pr.opened))
	}
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", ""))
	if w.Code != http.StatusBadRequest || len(pr.opened) != 0 {
		t.Errorf("exporting without naming the reviewed bundle answered %d: %s", w.Code, w.Body.String())
	}
}

// Exporting a bundle whose pull request is already open answers that pull request and says so.
func TestAnExportSaysWhenItsPullRequestWasAlreadyOpen(t *testing.T) {
	s, _, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	s.Publisher = &pullRequests{reused: true}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", reviewed(t, s, "acme/a")))
	var answer struct{ Reused bool }
	if err := json.Unmarshal(w.Body.Bytes(), &answer); err != nil || !answer.Reused {
		t.Errorf("an export answered by its open pull request = %s", w.Body.String())
	}
}

// unreadTests is a store whose test record reads as many times as reads says, and then cannot be read.
type unreadTests struct {
	store.Backend
	reads *int
}

func (u unreadTests) QueryTestsOf(query string) ([]store.QueryTest, error) {
	if *u.reads--; *u.reads < 0 {
		return nil, errors.New("database is locked")
	}
	return u.Backend.QueryTestsOf(query)
}

// A pull request states what is known of the query's tests: a record that could not be read is not
// the same as tests that were never run, and saying the second would be a claim nobody checked.
func TestAnExportSaysWhenItsTestRecordCouldNotBeRead(t *testing.T) {
	s, st, _ := controlServer(t, "")
	s.QueryDir = queryWorkspace(t, []string{"A"})
	pr := &pullRequests{}
	s.Publisher = pr
	seen := reviewed(t, s, "acme/a")
	reads := 0
	s.Store = unreadTests{Backend: st, reads: &reads}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, exportRequest("acme/a", seen))
	if w.Code != http.StatusOK || len(pr.opened) != 1 {
		t.Fatalf("export answered %d: %s", w.Code, w.Body.String())
	}
	body := pr.opened[0].Body
	if !strings.Contains(body, "could not be read") || strings.Contains(body, "not run at this version") {
		t.Errorf("the pull request reports an unread test record as:\n%s", body)
	}
}
