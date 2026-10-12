package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/submit"
)

// Preview is how a target is inspected before anything irreversible happens, so it must never
// write. A submission cannot be withdrawn and there is no delete endpoint.
func TestPreviewDoesNotSubmit(t *testing.T) {
	var submitted bool
	s := &Server{
		SnapshotPreview: func(repo string) (SnapshotPreview, error) {
			return SnapshotPreview{Repository: repo, Transitive: 2, Approved: true}, nil
		},
		SnapshotSubmit: func(string) (SnapshotResult, error) {
			submitted = true
			return SnapshotResult{}, nil
		},
	}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost,
		"/api/repositories/advanced-security/codeql-abom-test1/snapshot/preview", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if submitted {
		t.Fatal("preview submitted a snapshot")
	}
	var got SnapshotPreview
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Transitive != 2 {
		t.Errorf("transitive = %d, want 2", got.Transitive)
	}
}

// The submit path writes to somebody else's repository and cannot be undone, so it carries the
// same control-header requirement as the other mutating endpoints rather than being reachable by
// a stray cross-site form post.
func TestSubmitRequiresTheControlHeader(t *testing.T) {
	var submitted bool
	s := &Server{
		SnapshotSubmit: func(string) (SnapshotResult, error) {
			submitted = true
			return SnapshotResult{}, nil
		},
	}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost,
		"/api/repositories/advanced-security/codeql-abom-test1/snapshot/submit", nil)

	s.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("submit succeeded without the control header")
	}
	if submitted {
		t.Fatal("the handler ran without the control header")
	}
}

// A refusal has to say which repository and why, because the alternative is an operator retrying
// against a production repository until something works.
//
// The refusal comes from the real submission client rather than a local stand-in: an earlier
// version of this test used a fake that satisfied the marker interface while the production error
// type did not, so the running server answered 500 and this test still passed.
func TestSubmitReportsWhyATargetWasRefused(t *testing.T) {
	s := &Server{
		AllowRemoteControl: true,
		SnapshotSubmit: func(repo string) (SnapshotResult, error) {
			return SnapshotResult{}, &submit.ErrNotApproved{Repository: repo}
		},
	}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost,
		"/api/repositories/advanced-security/production/snapshot/submit", nil)

	req.Header.Set(controlHeader, "1")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "advanced-security/production") {
		t.Errorf("body does not name the refused repository: %s", rec.Body)
	}
}

// A closure built from declarations ABOM has no certain record of would replace what GitHub found
// with less, and that is not the operator's mistake to retry: the answer is a conflict with the
// data, naming what has to be analyzed again. The refusal is the store's own type, for the reason
// the test above gives.
func TestSubmitSaysWhichCommitsMustBeAnalyzedAgainBeforeItWillWrite(t *testing.T) {
	s := &Server{
		AllowRemoteControl: true,
		SnapshotSubmit: func(repo string) (SnapshotResult, error) {
			return SnapshotResult{}, &store.UnobservedClosureError{
				Repository: repo, Commits: []string{"vendor/comp@c1"}}
		},
	}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost,
		"/api/repositories/advanced-security/codeql-abom-test1/snapshot/submit", nil)

	req.Header.Set(controlHeader, "1")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "vendor/comp@c1") {
		t.Errorf("body does not name the commit to analyze again: %s", rec.Body)
	}
}
