package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/toolchain"
)

type fakeToolchain struct {
	checks []toolchain.Check
	fresh  bool
	set    map[string]string
	refuse error
}

func (f *fakeToolchain) Checks(_ context.Context, fresh bool) []toolchain.Check {
	f.fresh = fresh
	return f.checks
}

func (f *fakeToolchain) SetTool(_ context.Context, tool, path, _ string) error {
	if f.refuse != nil {
		return f.refuse
	}
	f.set[tool] = path
	return nil
}

type readinessAnswer struct {
	Known  bool              `json:"known"`
	Ready  bool              `json:"ready"`
	Checks []toolchain.Check `json:"checks"`
}

func readReadiness(t *testing.T, s *Server, query string) readinessAnswer {
	t.Helper()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/authoring/readiness"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("readiness answered %d: %s", w.Code, w.Body.String())
	}
	var got readinessAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReadinessIsUnknownWhereNothingChecksIt(t *testing.T) {
	s, _ := newServer(t)
	got := readReadiness(t, s, "")
	if got.Known || got.Ready || got.Checks == nil || len(got.Checks) != 0 {
		t.Errorf("a server with no toolchain answered %+v", got)
	}
}

func TestASingleProblemMakesTheLoopNotReady(t *testing.T) {
	s, _ := newServer(t)
	tc := &fakeToolchain{checks: []toolchain.Check{
		{Key: toolchain.KeyCodeQL, State: toolchain.Ready},
		{Key: toolchain.KeyCopilot, State: toolchain.Problem, Reason: "no Copilot CLI", Remedy: "name one"},
	}}
	s.Toolchain = tc
	got := readReadiness(t, s, "?fresh=1")
	if !got.Known || got.Ready || len(got.Checks) != 2 || !tc.fresh {
		t.Errorf("answered %+v (fresh probe %v)", got, tc.fresh)
	}
	tc.checks[1].State = toolchain.Ready
	if got := readReadiness(t, s, ""); !got.Ready || tc.fresh {
		t.Errorf("with every check ready answered %+v (fresh probe %v)", got, tc.fresh)
	}
}

func TestADiscoveryHoldingCodeQLIsReportedBeforeASessionIsRefused(t *testing.T) {
	s, _ := newServer(t)
	s.Toolchain = &fakeToolchain{checks: []toolchain.Check{{Key: toolchain.KeyCodeQL, State: toolchain.Ready}}}
	s.Discovery = &crawling{state: supervisor.StateWorking}
	got := readReadiness(t, s, "")
	if got.Ready || len(got.Checks) != 2 || got.Checks[1].Key != "codeql-free" || got.Checks[1].Remedy == "" {
		t.Errorf("with discovery running answered %+v", got)
	}
}

func setTool(s *Server, tool, body string, control bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("PUT", "/api/authoring/tools/"+tool, bytes.NewBufferString(body))
	r.Host = "127.0.0.1:8080"
	if control {
		r.Header.Set(controlHeader, "1")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAToolPathIsSetOnlyAsAControlActAndAnUnusableOneIsRefused(t *testing.T) {
	s, _ := newServer(t)
	tc := &fakeToolchain{set: map[string]string{}, checks: []toolchain.Check{}}
	s.Toolchain = tc
	if w := setTool(s, "copilot", `{"path":"/opt/copilot","by":"alice"}`, false); w.Code != http.StatusForbidden {
		t.Errorf("without the control header answered %d, want 403", w.Code)
	}
	if len(tc.set) != 0 {
		t.Fatalf("a refused request set %v", tc.set)
	}
	if w := setTool(s, "copilot", `{"path":"/opt/copilot","by":"alice"}`, true); w.Code != http.StatusOK {
		t.Fatalf("setting answered %d: %s", w.Code, w.Body.String())
	}
	if tc.set["copilot"] != "/opt/copilot" {
		t.Errorf("set %v", tc.set)
	}
	tc.refuse = errors.Join(toolchain.ErrUnusable, errors.New("/opt/x is not executable"))
	if w := setTool(s, "codeql", `{"path":"/opt/x"}`, true); w.Code != http.StatusBadRequest {
		t.Errorf("an unusable path answered %d, want 400", w.Code)
	}
}
