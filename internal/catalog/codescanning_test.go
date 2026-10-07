package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const alertsPageOne = `[
  {
    "number": 13,
    "state": "open",
    "rule": {"id": "actions/unpinned-tag", "security_severity_level": "medium"},
    "tool": {"name": "CodeQL", "version": "2.23.1"},
    "most_recent_instance": {
      "ref": "refs/heads/main",
      "analysis_key": "dynamic/github-code-scanning/codeql:analyze",
      "category": "/language:actions",
      "commit_sha": "9fb69279f0e816ea68f781524b429a2d0a892137",
      "location": {"path": ".github/workflows/6-copilot-on-github.yml",
                   "start_line": 125, "end_line": 125, "start_column": 15, "end_column": 46}
    }
  }
]`

const alertsPageTwo = `[
  {
    "number": 4,
    "state": "dismissed",
    "rule": {"id": "actions/unpinned-tag"},
    "tool": {"name": "CodeQL", "version": "2.23.1"},
    "most_recent_instance": {
      "ref": "refs/heads/main",
      "category": "/language:actions",
      "commit_sha": "9fb69279f0e816ea68f781524b429a2d0a892137",
      "location": {"path": ".github/workflows/2-application-initial-setup.yml",
                   "start_line": 72, "end_line": 72, "start_column": 15, "end_column": 46}
    }
  }
]`

// Every state is read, because a dismissed alert is one a developer already decided about and a
// fixed one reopens: leaving either out would count it as new, or as nothing, and both are wrong.
func TestAlertsAreReadAcrossEveryPageAndEveryState(t *testing.T) {
	var queries []string
	var c *Client
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/code-scanning/alerts"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, alertsPageTwo)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/octo-org/app/code-scanning/alerts?tool_name=CodeQL&per_page=100&page=2>; rel="next"`, c.baseURL))
		fmt.Fprint(w, alertsPageOne)
	})

	alerts, err := c.CodeScanningAlerts(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("CodeScanningAlerts: %v", err)
	}
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want both pages' 2", len(alerts))
	}
	for _, q := range queries {
		if strings.Contains(q, "state=") {
			t.Errorf("the read asked for one state (%s), and would miss the others", q)
		}
		if !strings.Contains(q, "tool_name=CodeQL") {
			t.Errorf("the read did not narrow to CodeQL's alerts: %s", q)
		}
	}
	got := alerts[0]
	want := Alert{
		Number: 13, RuleID: "actions/unpinned-tag", State: "open", Tool: "CodeQL", ToolVersion: "2.23.1",
		Category: "/language:actions", Ref: "refs/heads/main",
		CommitSHA: "9fb69279f0e816ea68f781524b429a2d0a892137",
		Path:      ".github/workflows/6-copilot-on-github.yml",
		StartLine: 125, StartColumn: 15, EndLine: 125, EndColumn: 46,
	}
	if got != want {
		t.Errorf("first alert = %+v, want %+v", got, want)
	}
	if alerts[1].State != "dismissed" {
		t.Errorf("second alert state = %q, want dismissed", alerts[1].State)
	}
}

// A repository code scanning never analyzed has no alerts to compare with, which is not the same
// statement as holding none: the first is a repository nothing would raise an alert in.
func TestARepositoryNeverAnalyzedReadsAsNoAnalysisRatherThanNoAlerts(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"no analysis found","status":"404"}`)
	})

	alerts, err := c.CodeScanningAlerts(context.Background(), "octo-org/app")
	if !errors.Is(err, ErrNoAnalysis) {
		t.Fatalf("got %v, want ErrNoAnalysis", err)
	}
	if alerts != nil {
		t.Errorf("got %d alerts alongside the refusal, want none", len(alerts))
	}
	var none *NoAnalysisError
	if !errors.As(err, &none) || none.Reason != "no analysis found" {
		t.Errorf("the error %q does not carry GitHub's own reason", err)
	}
}

// Alerts GitHub will not show are unread, never an empty list: reading a refusal as zero alerts
// would report every result there as new.
func TestAlertsGitHubRefusesToShowAreAnErrorNotAnEmptyList(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Advanced Security must be enabled for this repository to use code scanning."}`)
	})

	_, err := c.CodeScanningAlerts(context.Background(), "octo-org/app")
	if err == nil {
		t.Fatal("a refused read returned no error")
	}
	if errors.Is(err, ErrNoAnalysis) {
		t.Errorf("a refusal read as no analysis: %v", err)
	}
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden {
		t.Fatalf("got %v, want a 403 StatusError", err)
	}
	if status.Message != "Advanced Security must be enabled for this repository to use code scanning." {
		t.Errorf("message = %q, want GitHub's own", status.Message)
	}
}

const analysesBody = `[
  {"id": 3, "ref": "refs/heads/main", "commit_sha": "cccc", "category": "/language:javascript-typescript",
   "analysis_key": "dynamic/github-code-scanning/codeql:analyze", "created_at": "2026-09-20T10:00:00Z",
   "tool": {"name": "CodeQL", "version": "2.27.0"}},
  {"id": 2, "ref": "refs/heads/main", "commit_sha": "bbbb", "category": "/language:actions",
   "analysis_key": "dynamic/github-code-scanning/codeql:analyze", "created_at": "2026-09-19T10:00:00Z",
   "tool": {"name": "CodeQL", "version": "2.27.0"}},
  {"id": 1, "ref": "refs/heads/main", "commit_sha": "aaaa", "category": "/language:actions",
   "analysis_key": "dynamic/github-code-scanning/codeql:analyze", "created_at": "2026-09-12T10:00:00Z",
   "tool": {"name": "CodeQL", "version": "2.26.4"}}
]`

// The newest Actions analysis says which commit the alerts describe, and another language's
// analysis says nothing about Actions however recent it is.
func TestTheNewestActionsAnalysisIsFoundAmongOtherLanguages(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/code-scanning/analyses"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		fmt.Fprint(w, analysesBody)
	})

	analysis, found, err := c.ActionsAnalysis(context.Background(), "octo-org/app", "")
	if err != nil {
		t.Fatalf("ActionsAnalysis: %v", err)
	}
	if !found {
		t.Fatal("an Actions analysis on the page was not found")
	}
	if analysis.CommitSHA != "bbbb" || analysis.CreatedAt != "2026-09-19T10:00:00Z" {
		t.Errorf("got %+v, want the newest Actions analysis, at bbbb", analysis)
	}
}

func TestNoActionsAnalysisAmongOthersIsNotFound(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"id": 3, "commit_sha": "cccc", "category": "/language:go", "created_at": "2026-09-20T10:00:00Z"}]`)
	})

	_, found, err := c.ActionsAnalysis(context.Background(), "octo-org/app", "")
	if err != nil {
		t.Fatalf("ActionsAnalysis: %v", err)
	}
	if found {
		t.Error("a Go analysis was reported as an Actions analysis")
	}
}

// The alerts compared are the default branch's, so the analysis that says which commit they
// describe must be too. A pull request's analysis is newer in any busy repository and describes a
// merge commit no alert on the default branch came from.
func TestTheActionsAnalysisIsTheDefaultBranchsNotAPullRequests(t *testing.T) {
	var asked string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("ref")
		fmt.Fprint(w, `[
		  {"id": 3, "ref": "refs/pull/12/merge", "commit_sha": "pppp", "category": "/language:actions",
		   "created_at": "2026-09-21T10:00:00Z"},
		  {"id": 2, "ref": "refs/heads/main", "commit_sha": "mmmm", "category": "/language:actions",
		   "created_at": "2026-09-19T10:00:00Z"}]`)
	})

	analysis, found, err := c.ActionsAnalysis(context.Background(), "octo-org/app", "refs/heads/main")
	if err != nil || !found {
		t.Fatalf("ActionsAnalysis = %v, %v", found, err)
	}
	if asked != "refs/heads/main" {
		t.Errorf("the read asked for ref %q, want the default branch's", asked)
	}
	if analysis.CommitSHA != "mmmm" {
		t.Errorf("the analysis found is at %q, want the default branch's mmmm", analysis.CommitSHA)
	}
}

// The analysis that can stand in is the newest at one commit, which need not be the newest of all.
func TestEveryActionsAnalysisIsListedNewestFirstWithTheIDThatFetchesIt(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, analysesBody)
	})

	analyses, err := c.ActionsAnalyses(context.Background(), "octo-org/app", "")
	if err != nil {
		t.Fatalf("ActionsAnalyses: %v", err)
	}
	var got []string
	for _, a := range analyses {
		got = append(got, fmt.Sprintf("%d@%s", a.ID, a.CommitSHA))
	}
	if want := []string{"2@bbbb", "1@aaaa"}; !slices.Equal(got, want) {
		t.Errorf("analyses = %v, want %v: the Actions ones, newest first, each with its id", got, want)
	}
}

// codeScanningFixture is generated from the controlled workflow (regen-codescanning-fixture.sh).
const codeScanningFixture = "../../testdata/codescanning/synthetic/"

// The generated document is passed byte for byte in the media type that returns SARIF, not a summary.
func TestAnAnalysisIsDownloadedAsItsSarifDocument(t *testing.T) {
	want, err := os.ReadFile(codeScanningFixture + "analysis.sarif")
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/has-ghas/test-repo-3/code-scanning/analyses/1"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get("Accept"); got != "application/sarif+json" {
			t.Errorf("Accept = %q, want application/sarif+json", got)
		}
		_, _ = w.Write(want)
	})

	dest := filepath.Join(t.TempDir(), "analysis.sarif")
	if err := c.DownloadAnalysis(context.Background(), "has-ghas/test-repo-3", 1, dest); err != nil {
		t.Fatalf("DownloadAnalysis: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("downloaded %d bytes, want %d unchanged", len(got), len(want))
	}
}

// Half a document read as a whole one would take its missing results as rules that found nothing.
func TestAFailedAnalysisDownloadLeavesNoDocument(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by integration"}`)
	})

	dest := filepath.Join(t.TempDir(), "analysis.sarif")
	err := c.DownloadAnalysis(context.Background(), "has-ghas/test-repo-3", 1, dest)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden {
		t.Fatalf("got %v, want the 403", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a refused download left a document behind")
	}
}

// reports it.
func TestCompareReportsHowFarTheHeadHasMovedPastACommit(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/compare/aaaa...bbbb"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		fmt.Fprint(w, `{"status": "ahead", "ahead_by": 7, "behind_by": 0, "commits": [], "files": []}`)
	})

	moved, err := c.CompareCommits(context.Background(), "octo-org/app", "aaaa", "bbbb")
	if err != nil {
		t.Fatalf("CompareCommits: %v", err)
	}
	if moved.Status != "ahead" || moved.AheadBy != 7 || moved.BehindBy != 0 {
		t.Errorf("got %+v, want ahead by 7", moved)
	}
}
