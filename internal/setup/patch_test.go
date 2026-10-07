package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// fakeGitHub answers default setup reads and writes for one repository, recording what was asked.
type fakeGitHub struct {
	languages []string
	state     string
	patch     func(w http.ResponseWriter, body PatchBody)
	requests  []string
	ifMatch   string
	auth      string
	sent      []PatchBody
}

func (f *fakeGitHub) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.auth = r.Header.Get("Authorization")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			fmt.Fprint(w, `{"login":"data-douser"}`)
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", `W/"setup-1"`)
			raw, _ := json.Marshal(map[string]any{"state": f.state, "languages": f.languages})
			w.Write(raw)
		case r.Method == http.MethodPatch:
			f.ifMatch = r.Header.Get("If-Match")
			// As measured on has-ghas/test-repo-2: refused before the body is read.
			if f.ifMatch != "" {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(w, `{"message":"Bad Request","errors":["Conditional request headers are not allowed in unsafe requests unless supported by the endpoint"]}`)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var body PatchBody
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decoding the write: %v", err)
			}
			f.sent = append(f.sent, body)
			f.patch(w, body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func patcherFor(srv *httptest.Server, allowed ...string) *Patcher {
	return &Patcher{BaseURL: srv.URL, Token: "operator-token", HTTP: srv.Client(),
		Allowed: func(repository string) bool { return slices.Contains(allowed, repository) }}
}

func TestARepositoryOutsideTheTargetsIsNeverAsked(t *testing.T) {
	f := &fakeGitHub{state: "configured", languages: []string{"python"}}
	p := patcherFor(f.serve(t), "octo/test-repo")
	_, err := p.Write(context.Background(), "octo/production", "configured", []string{"python"},
		PatchBody{State: "configured", Languages: []string{"actions", "python"}})
	var refused *ErrNotATarget
	if !errors.As(err, &refused) || len(f.requests) != 0 {
		t.Errorf("Write = %v after %v, want refused before any request", err, f.requests)
	}
}

// The write token reads what it is about to replace, seconds before replacing it, and sends
// nothing if that is not what the preview and the fresh read saw.
func TestAWriteSendsNothingWhenWhatItWouldReplaceHasChanged(t *testing.T) {
	f := &fakeGitHub{state: "configured", languages: []string{"python", "go"}}
	p := patcherFor(f.serve(t), "octo/app")
	_, err := p.Write(context.Background(), "octo/app", "configured", []string{"python"},
		PatchBody{State: "configured", Languages: []string{"actions", "python"}})
	var changed *ErrChanged
	if !errors.As(err, &changed) || len(f.sent) != 0 {
		t.Errorf("Write = %v and sent %d, want ErrChanged and nothing sent", err, len(f.sent))
	}
}

// GitHub refuses any conditional header on this write, so the write token's own read seconds
// earlier is the guard against a change in between, and the read-back catches what slips past it.
func TestAWriteSendsNoConditionalHeaderAndCarriesTheOperatorsToken(t *testing.T) {
	f := &fakeGitHub{state: "configured", languages: []string{"python"},
		patch: func(w http.ResponseWriter, _ PatchBody) {
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprint(w, `{"run_id":42,"run_url":"https://api.github.com/repos/octo/app/actions/runs/42"}`)
		}}
	p := patcherFor(f.serve(t), "octo/app")
	got, err := p.Write(context.Background(), "octo/app", "configured", []string{"python"},
		PatchBody{State: "configured", Languages: []string{"actions", "python"}})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got.Status != http.StatusAccepted || got.RunID != 42 || got.RunURL == "" {
		t.Errorf("result %+v", got)
	}
	if f.ifMatch != "" || f.auth != "Bearer operator-token" {
		t.Errorf("If-Match %q and Authorization %q, want no conditional header and the operator's token", f.ifMatch, f.auth)
	}
	if want := []string{"GET /repos/octo/app/code-scanning/default-setup", "PATCH /repos/octo/app/code-scanning/default-setup"}; !slices.Equal(f.requests, want) {
		t.Errorf("requests %v, want %v", f.requests, want)
	}
}

func TestAConflictIsAnAnswerNotAFailure(t *testing.T) {
	f := &fakeGitHub{state: "not-configured", languages: []string{"actions"},
		patch: func(w http.ResponseWriter, _ PatchBody) {
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"message":"A validation run is in progress with a different configuration."}`)
		}}
	p := patcherFor(f.serve(t), "octo/app")
	got, err := p.Write(context.Background(), "octo/app", "not-configured", []string{"actions"},
		PatchBody{State: "configured", Languages: []string{"actions"}})
	if err != nil || got.Status != http.StatusConflict || got.Message == "" {
		t.Errorf("Write = %+v, %v; want the 409 and GitHub's message", got, err)
	}
}

func TestTheApproverIsTheWriteTokensOwner(t *testing.T) {
	f := &fakeGitHub{}
	p := patcherFor(f.serve(t))
	if login, err := p.Login(context.Background()); err != nil || login != "data-douser" {
		t.Errorf("Login = %q, %v", login, err)
	}
}
