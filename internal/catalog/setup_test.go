package catalog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"
)

func TestDefaultSetupIsReadExactlyAsGitHubSaidIt(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/repos/octo-org/app/code-scanning/default-setup"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		fmt.Fprint(w, `{"state":"configured","languages":["javascript","javascript-typescript","typescript","actions"],
		  "query_suite":"extended","threat_model":"remote","updated_at":"2026-10-05T13:26:23Z",
		  "schedule":"weekly","runner_type":"labeled","runner_label":"code-scanning"}`)
	})
	got, err := c.DefaultSetup(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("DefaultSetup: %v", err)
	}
	want := DefaultSetup{State: "configured",
		Languages:  []string{"javascript", "javascript-typescript", "typescript", "actions"},
		QuerySuite: "extended", ThreatModel: "remote", UpdatedAt: "2026-10-05T13:26:23Z",
		Schedule: "weekly", RunnerType: "labeled", RunnerLabel: "code-scanning"}
	if !slices.Equal(got.Languages, want.Languages) {
		t.Errorf("languages %v, want %v kept in GitHub's names and order", got.Languages, want.Languages)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DefaultSetup = %+v, want %+v", got, want)
	}
}

// A refusal is an answer about the repository, and its reason is GitHub's own words.
func TestARefusedDefaultSetupReadCarriesGitHubsReason(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Advanced Security must be enabled for this repository to use code scanning."}`)
	})
	_, err := c.DefaultSetup(context.Background(), "octo-org/app")
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden ||
		status.Message != "Advanced Security must be enabled for this repository to use code scanning." {
		t.Fatalf("DefaultSetup error = %v, want a 403 carrying GitHub's message", err)
	}
}

func TestSecurityConfigurationsAreReadWithTheirDefaultSetupOptions(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/orgs/octo-org/code-security/configurations"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		fmt.Fprint(w, `[
		  {"id":17,"name":"GitHub recommended","target_type":"global","enforcement":"enforced",
		   "code_scanning_default_setup":"enabled","code_scanning_default_setup_options":null,"code_scanning_options":null},
		  {"id":233126,"name":"code-scanning-advanced","target_type":"organization","enforcement":"enforced",
		   "code_scanning_default_setup":"enabled",
		   "code_scanning_default_setup_options":{"runner_type":"labeled","runner_label":"big"},
		   "code_scanning_options":{"allow_advanced":true}}]`)
	})
	got, err := c.SecurityConfigurations(context.Background(), "octo-org")
	if err != nil {
		t.Fatalf("SecurityConfigurations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("read %d configurations, want 2: %+v", len(got), got)
	}
	if got[0].AllowAdvanced != nil || got[0].RunnerType != "" {
		t.Errorf("unset options must stay unset, got %+v", got[0])
	}
	if got[1].AllowAdvanced == nil || !*got[1].AllowAdvanced || got[1].RunnerType != "labeled" ||
		got[1].RunnerLabel != "big" || got[1].Enforcement != "enforced" || got[1].DefaultSetup != "enabled" {
		t.Errorf("configuration 233126 read as %+v", got[1])
	}
}

func TestConfigurationRepositoriesFollowGitHubsCursor(t *testing.T) {
	var c *Client
	c = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/orgs/octo-org/code-security/configurations/17/repositories"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if r.URL.Query().Get("after") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?per_page=100&after=abc>; rel="next"`, c.baseURL, r.URL.Path))
			fmt.Fprint(w, `[{"status":"enforced","repository":{"full_name":"octo-org/one"}}]`)
			return
		}
		fmt.Fprint(w, `[{"status":"failed","repository":{"full_name":"octo-org/two"}}]`)
	})
	got, err := c.ConfigurationRepositories(context.Background(), "octo-org", 17)
	if err != nil {
		t.Fatalf("ConfigurationRepositories: %v", err)
	}
	want := []ConfigurationRepository{{Repository: "octo-org/one", Status: "enforced"},
		{Repository: "octo-org/two", Status: "failed"}}
	if !slices.Equal(got, want) {
		t.Errorf("ConfigurationRepositories = %+v, want %+v", got, want)
	}
}

// Every CodeQL analysis on the newest page is returned, whichever setup made it: telling the two
// apart is what the page is read for.
func TestCodeQLAnalysesKeepEveryKeyOnTheNewestPage(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Query().Get("tool_name"), "CodeQL"; got != want {
			t.Errorf("tool_name = %q, want %q", got, want)
		}
		fmt.Fprint(w, `[
		  {"id":3,"ref":"refs/pull/4/merge","commit_sha":"p","category":"/language:actions",
		   "analysis_key":".github/workflows/codeql.yml:analyze","created_at":"2026-10-03T00:00:00Z"},
		  {"id":2,"ref":"refs/heads/main","commit_sha":"b","category":"/language:python",
		   "analysis_key":".github/workflows/codeql.yml:analyze","created_at":"2026-10-02T00:00:00Z"},
		  {"id":1,"ref":"refs/heads/main","commit_sha":"a","category":"/language:actions",
		   "analysis_key":"dynamic/github-code-scanning/codeql:analyze","created_at":"2026-10-01T00:00:00Z"}]`)
	})
	got, err := c.CodeQLAnalyses(context.Background(), "octo-org/app")
	if err != nil {
		t.Fatalf("CodeQLAnalyses: %v", err)
	}
	ids := []int64{}
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if !slices.Equal(ids, []int64{3, 2, 1}) {
		t.Errorf("analyses %v, want every one of 3, 2, 1 in GitHub's order", ids)
	}
}

func TestARepositorysConfigurationIsReadAndNoneIsAnAnswer(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/octo-org/app/code-security-configuration":
			fmt.Fprint(w, `{"status":"enforced","configuration":{"id":17,"name":"GitHub recommended",
			  "target_type":"global","enforcement":"enforced","code_scanning_default_setup":"enabled"}}`)
		case "/repos/octo-org/bare/code-security-configuration":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("asked for %s", r.URL.Path)
		}
	})
	got, err := c.RepositoryConfiguration(context.Background(), "octo-org/app")
	if err != nil || got.Configuration == nil || got.Status != "enforced" || got.Configuration.ID != 17 ||
		got.Configuration.DefaultSetup != "enabled" || got.Configuration.Enforcement != "enforced" {
		t.Errorf("RepositoryConfiguration = %+v, %v", got, err)
	}
	none, err := c.RepositoryConfiguration(context.Background(), "octo-org/bare")
	if err != nil || none.Configuration != nil {
		t.Errorf("a 204 is no configuration attached, got %+v, %v", none, err)
	}
}

func TestActionsPermissionsReadTheSelectedActionsOnlyWhenTheyDecide(t *testing.T) {
	var asked []string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		switch r.URL.Path {
		case "/repos/octo-org/open/actions/permissions":
			fmt.Fprint(w, `{"enabled":true,"allowed_actions":"all"}`)
		case "/repos/octo-org/picky/actions/permissions":
			fmt.Fprint(w, `{"enabled":true,"allowed_actions":"selected"}`)
		case "/repos/octo-org/picky/actions/permissions/selected-actions":
			fmt.Fprint(w, `{"github_owned_allowed":false,"verified_allowed":false,"patterns_allowed":["github/codeql-action/*"]}`)
		}
	})
	open, err := c.ActionsPermissions(context.Background(), "octo-org/open")
	if err != nil || !open.Enabled || open.AllowedActions != "all" || open.Selected != nil {
		t.Errorf("open = %+v, %v", open, err)
	}
	picky, err := c.ActionsPermissions(context.Background(), "octo-org/picky")
	if err != nil || picky.Selected == nil || picky.Selected.GitHubOwnedAllowed ||
		!slices.Equal(picky.Selected.PatternsAllowed, []string{"github/codeql-action/*"}) {
		t.Errorf("picky = %+v, %v", picky, err)
	}
	if len(asked) != 3 {
		t.Errorf("asked %v, want the selected actions read only where they decide", asked)
	}
}
