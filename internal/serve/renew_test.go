package serve

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// Renewal writes to other people's repositories and cannot be undone, so it carries the same
// control-header requirement as every other mutating endpoint.
func TestRenewRequiresTheControlHeader(t *testing.T) {
	var ran bool
	s := &Server{
		ApprovedTargets: func() []string { ran = true; return nil },
	}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost, "/api/submissions/renew", nil)
	s.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatal("renew succeeded without the control header")
	}
	if ran {
		t.Fatal("the handler ran without the control header")
	}
}

// A report that says how many were checked but not which were written is not an audit trail. A
// submission cannot be withdrawn, so the answer has to name every repository it touched.
func TestTheRenewalReportNamesWhatItWrote(t *testing.T) {
	s := renewServer(t, map[string]SnapshotPreview{
		"a/stale":   {Submitted: true, NeedsRenewal: true, Transitive: 2},
		"a/current": {Submitted: true, NeedsRenewal: false},
	}, nil)

	got := renewVia(t, s)
	if len(got.Renewed) != 1 || got.Renewed[0].Repository != "a/stale" {
		t.Errorf("renewed = %+v, want only the stale repository", got.Renewed)
	}
	if len(got.UpToDate) != 1 || got.UpToDate[0] != "a/current" {
		t.Errorf("upToDate = %v, want the current repository named", got.UpToDate)
	}
	if got.Checked != 2 {
		t.Errorf("checked = %d, want 2", got.Checked)
	}
}

// Renewal renews. A repository on the allow-list nobody has submitted to yet would be a *first*
// submission, which is a deliberate act: otherwise adding a name to --submit-targets writes to it
// immediately, with nobody having decided to.
func TestABatchRenewalNeverMakesAFirstSubmission(t *testing.T) {
	s := renewServer(t, map[string]SnapshotPreview{
		"a/never": {Submitted: false, NeedsRenewal: false, Transitive: 3},
	}, nil)

	got := renewVia(t, s)
	if len(got.Renewed) != 0 {
		t.Fatalf("wrote a first submission to %+v", got.Renewed)
	}
	if len(got.Skipped) != 1 || got.Skipped[0] != "a/never" {
		t.Errorf("skipped = %v, want the unsubmitted repository named", got.Skipped)
	}
}

// One unreachable repository must not discard the others. The pass is several independent writes
// and a partial answer is still worth having — the rule enumeration already follows.
func TestOneFailureDoesNotAbandonTheRest(t *testing.T) {
	s := renewServer(t, map[string]SnapshotPreview{
		"a/broken": {Submitted: true, NeedsRenewal: true},
		"a/fine":   {Submitted: true, NeedsRenewal: true},
	}, map[string]bool{"a/broken": true})

	got := renewVia(t, s)
	if len(got.Renewed) != 1 || got.Renewed[0].Repository != "a/fine" {
		t.Errorf("renewed = %+v, want the healthy repository still written", got.Renewed)
	}
	if len(got.Failed) != 1 || got.Failed[0].Repository != "a/broken" {
		t.Errorf("failed = %+v, want the broken repository named with its reason", got.Failed)
	}
}

// A build with no renewal wiring must say so rather than answering an empty report, which reads
// as "nothing needed renewing" and is a different claim entirely.
func TestRenewSaysWhenTheBuildCannotDoIt(t *testing.T) {
	s := &Server{AllowRemoteControl: true}

	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost, "/api/submissions/renew", nil)
	req.Header.Set(controlHeader, "1")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("an unwired build answered 200: %s", rec.Body)
	}
}

func renewServer(t *testing.T, states map[string]SnapshotPreview, fail map[string]bool) *Server {
	t.Helper()
	var targets []string
	for r := range states {
		targets = append(targets, r)
	}
	sort.Strings(targets)

	return &Server{
		AllowRemoteControl: true,
		ApprovedTargets:    func() []string { return targets },
		SnapshotPreview: func(repo string) (SnapshotPreview, error) {
			p := states[repo]
			p.Repository = repo
			return p, nil
		},
		SnapshotSubmit: func(repo string) (SnapshotResult, error) {
			if fail[repo] {
				return SnapshotResult{}, errors.New("submission refused")
			}
			return SnapshotResult{Repository: repo, Result: "SUCCESS",
				Submitted: states[repo].Transitive}, nil
		},
	}
}

func renewVia(t *testing.T, s *Server) RenewalReport {
	t.Helper()
	rec := httptest.NewRecorder()
	req := localRequest(http.MethodPost, "/api/submissions/renew", nil)
	req.Header.Set(controlHeader, "1")
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	var got RenewalReport
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}
