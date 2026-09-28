package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/lockverify"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func verifyRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/locks/verify", strings.NewReader(body))
	r.Host = "127.0.0.1:8080"
	r.Header.Set(controlHeader, "1")
	r.Header.Set("Content-Type", "application/json")
	return r
}

// A verification runs only when somebody asks, through the one guarded action, and answers with
// what the tool said.
func TestAVerificationIsRunOnRequestAndAnswersWithTheResult(t *testing.T) {
	srv, _ := newServer(t)
	var asked []string
	srv.VerifyLock = func(_ context.Context, repository, mode string) (store.LockVerification, error) {
		asked = append(asked, repository+" "+mode)
		return store.LockVerification{Repository: repository, Commit: "c1", Mode: mode,
			Outcome: lockverify.OutcomeWarnings, Findings: []store.LockVerificationFinding{}}, nil
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, verifyRequest(`{"repository":"octo/app","mode":"network"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got store.LockVerification
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != lockverify.OutcomeWarnings || len(asked) != 1 || asked[0] != "octo/app network" {
		t.Errorf("answered %+v after asking %v", got, asked)
	}
}

// Each refusal says why, with a status a client can act on: nothing to verify is a conflict with
// the state of the repository, and a spent budget is a request to come back later.
func TestARefusedVerificationSaysWhy(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"nothing to verify": {lockverify.ErrNothingToVerify, http.StatusConflict},
		"too soon":          {fmt.Errorf("%w: last run now", lockverify.ErrTooSoon), http.StatusTooManyRequests},
		"budget":            {fmt.Errorf("%w: 50 in the last day", lockverify.ErrBudget), http.StatusTooManyRequests},
		"bad request":       {errors.New("name a repository as owner/name"), http.StatusBadRequest},
	} {
		srv, _ := newServer(t)
		srv.VerifyLock = func(context.Context, string, string) (store.LockVerification, error) {
			return store.LockVerification{}, c.err
		}
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, verifyRequest(`{"repository":"octo/app","mode":"local"}`))
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), strings.Split(c.err.Error(), ":")[0]) {
			t.Errorf("%s: status %d, body %s; want %d naming the reason", name, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// A client that is not local cannot start one unless the operator allowed remote control, the
// same as every other action that spends something.
func TestARemoteClientCannotStartAVerification(t *testing.T) {
	srv, _ := newServer(t)
	srv.VerifyLock = func(context.Context, string, string) (store.LockVerification, error) {
		t.Fatal("a remote request ran a verification")
		return store.LockVerification{}, nil
	}
	r := verifyRequest(`{"repository":"octo/app","mode":"local"}`)
	r.Host = "abom.example.com"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		t.Errorf("status %d, want a refusal", rec.Code)
	}
}

// The history is a read, and never runs the tool.
func TestTheHistoryIsReadWithoutRunningAnything(t *testing.T) {
	srv, st := newServer(t)
	srv.VerifyLock = func(context.Context, string, string) (store.LockVerification, error) {
		t.Fatal("reading the history ran a verification")
		return store.LockVerification{}, nil
	}
	if _, err := st.RecordLockVerification(store.LockVerification{Repository: "octo/app", Commit: "c1",
		Mode: "local", Outcome: "passed", VerifiedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/locks/verifications?repository=octo/app", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var got []store.LockVerification
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got) != 1 {
		t.Errorf("history %s, %v; want one run", rec.Body.String(), err)
	}
}
