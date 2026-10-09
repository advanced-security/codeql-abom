package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type stepShown struct {
	Step  string `json:"step"`
	Files []struct {
		Path      string `json:"path"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	} `json:"files"`
	Absent string `json:"absent"`
}

func showStep(t *testing.T, s *Server, query, step string) (int, stepShown, string) {
	t.Helper()
	w := httptest.NewRecorder()
	reading(s.queryStep, "query", "step")(w, httptest.NewRequest("GET",
		"/api/queries/step?query="+query+"&step="+step, nil))
	var got stepShown
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding %s: %v", w.Body.String(), err)
		}
	}
	return w.Code, got, w.Body.String()
}

// Each step a reader can open shows the file it was read from, as it is on disk, and a step with
// nothing on disk says what is missing rather than showing an empty file.
func TestAStepShowsWhatItWasReadFrom(t *testing.T) {
	s, st := newServer(t)
	s.QueryDir = queryWorkspace(t, []string{"A"})

	for _, c := range []struct{ step, path, body string }{
		{"expected", "test/A/A.expected", "| a job |\n"},
		{"compiles", "src/A.ql", "@id acme/a"},
	} {
		code, got, raw := showStep(t, s, "acme/a", c.step)
		if code != http.StatusOK || len(got.Files) != 1 || got.Files[0].Path != c.path ||
			!strings.Contains(got.Files[0].Body, c.body) || got.Absent != "" {
			t.Errorf("%s answered %d: %s", c.step, code, raw)
		}
	}

	for _, c := range []struct{ step, says string }{
		{"help", "src/A.md"},
		{"tests", ".actual"},
		{"ast", "printed"},
	} {
		code, got, raw := showStep(t, s, "acme/a", c.step)
		if code != http.StatusOK || len(got.Files) != 0 || !strings.Contains(got.Absent, c.says) {
			t.Errorf("%s with nothing on disk answered %d: %s", c.step, code, raw)
		}
	}

	if err := os.WriteFile(filepath.Join(s.QueryDir, "src", "A.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.QueryDir, "test", "A", "A.actual"), []byte("| other |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordAuthoringAST(store.AuthoringAST{
		PrintedBy: "operator", QueryID: "acme/a", PrintedAt: "2026-10-08T00:00:00Z", Query: "PrintAST",
		Database: "test/A/A.testproj", SourceFiles: ".github/workflows/ci.yml", Body: "Workflow ci",
		CLIVersion: "2.27.1",
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ step, path, body string }{
		{"help", "src/A.md", "# A\n"},
		{"tests", "test/A/A.actual", "| other |\n"},
		{"ast", "test/A/A.testproj", "Workflow ci"},
	} {
		code, got, raw := showStep(t, s, "acme/a", c.step)
		if code != http.StatusOK || len(got.Files) != 1 || !strings.Contains(got.Files[0].Path, c.path) ||
			got.Files[0].Body != c.body {
			t.Errorf("%s answered %d: %s", c.step, code, raw)
		}
	}
}

func TestAStepIsShownOnlyForAQueryOnDiskAndAStepWithAFile(t *testing.T) {
	s, _ := newServer(t)
	if code, _, raw := showStep(t, s, "acme/a", "expected"); code != http.StatusServiceUnavailable {
		t.Errorf("with no query directory answered %d: %s", code, raw)
	}
	s.QueryDir = queryWorkspace(t, []string{"A"})
	for _, c := range []struct {
		query, step string
		code        int
	}{
		{"acme/nothing", "expected", http.StatusNotFound},
		{"acme/a", "target", http.StatusBadRequest},
		{"acme/a", "../../etc/passwd", http.StatusBadRequest},
		{"", "expected", http.StatusBadRequest},
	} {
		if code, _, raw := showStep(t, s, c.query, c.step); code != c.code {
			t.Errorf("%q %q answered %d, want %d: %s", c.query, c.step, code, c.code, raw)
		}
	}
}
