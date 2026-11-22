package ghapp

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
)

// With an App, who may declare is read from where it is installed, never from a person's gh session,
// and a listing that fails is a problem to show rather than an identity that belongs to nothing.
func TestWithAnAppTheIdentityIsWhereItIsInstalled(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"},
		fakeInstallation{ID: 12, Login: "data-douser", Type: "User", Selection: "selected"},
		fakeInstallation{ID: 13, Login: "paused-org", Type: "Organization", Selection: "all", Suspended: true})
	app := gh.app(c.Now)
	app.Credentials.Slug = "abom-has-ghas"

	id, err := app.Identity(context.Background())
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}
	if id.Login != "abom-has-ghas[bot]" || id.Host != app.Host() || id.VerifiedAt.IsZero() || len(id.Scopes) != 0 {
		t.Errorf("identity %+v: want the App's bot on its API's host, verified now, with no scopes", id)
	}
	for api, want := range map[string]string{"": "github.com", "https://api.github.com": "github.com",
		"https://ghe.example.com/api/v3": "ghe.example.com", "https://api.acme.ghe.com": "acme.ghe.com"} {
		if got := (&App{API: api}).Host(); got != want {
			t.Errorf("Host() for %q = %q, want %q", api, got, want)
		}
	}
	want := []ghauth.Account{
		{Owner: "data-douser", Kind: "user", Role: ghauth.RoleInstallation},
		{Owner: "has-ghas", Kind: "organization", Role: ghauth.RoleInstallation},
	}
	if !slices.Equal(id.Accounts, want) {
		t.Errorf("accounts %+v, want %+v: every installation not suspended, and nothing else", id.Accounts, want)
	}

	gh.listStatus = 500
	app = gh.app(c.Now)
	_, err = app.Identity(context.Background())
	var problem *ghauth.Problem
	if !errors.As(err, &problem) || !strings.Contains(problem.Reason, "App") || strings.Contains(problem.Remedy, "gh ") {
		t.Errorf("a failed listing = %v; want a problem about the App that does not send anyone to gh", err)
	}
}

// The picker offers what the installation includes and nothing a lent token could see: a repository
// the App cannot read is not one a production instance can be answerable for reading.
func TestWithAnAppThePickerListsWhatTheInstallationIncludes(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"},
		fakeInstallation{ID: 12, Login: "advanced-security", Type: "Organization", Selection: "selected",
			Repos: []string{"codeql-abom-test1", "archived", "codeql-abom", "codeql-abom-ghsa-4f8h-9q2r-x7mw"}})
	app := gh.app(c.Now)

	repos, err := app.Repositories(context.Background(), "advanced-security")
	if err != nil {
		t.Fatalf("Repositories: %v", err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
		if !r.Private || r.Archived != (r.Name == "archived") {
			t.Errorf("%s: private=%v archived=%v, want GitHub's answer carried", r.Name, r.Private, r.Archived)
		}
	}
	if !slices.Equal(names, []string{"archived", "codeql-abom", "codeql-abom-test1"}) {
		t.Errorf("listed %v, want the installation's repositories, sorted", names)
	}

	_, err = app.Repositories(context.Background(), "actions")
	var problem *ghauth.Problem
	if !errors.As(err, &problem) || !strings.Contains(problem.Reason, "actions") {
		t.Errorf("an account the App is not installed on = %v; want a problem naming it", err)
	}
	if n := gh.count("POST /app/installations/11/access_tokens"); n != 0 {
		t.Errorf("has-ghas's installation was minted %d tokens to list another account", n)
	}
}

// The property picker's reads go through the named organization's own installation, follow
// GitHub's pages, and refuse anything that is not about one account, such as the person's own.
func TestWithAnAppThePropertyPickerReadsThroughTheAccountsOwnInstallation(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"})
	app := gh.app(c.Now)
	run := app.Runner()
	ctx := context.Background()

	defs, err := ghauth.ReadPropertySchema(ctx, run, "has-ghas")
	if err != nil || len(defs) != 1 || defs[0].Name != "team" {
		t.Errorf("schema = %+v, %v; want the organization's one property", defs, err)
	}
	values, err := ghauth.ReadPropertyValues(ctx, run, "has-ghas")
	if err != nil || len(values) != 2 || values[1].Repository != "has-ghas/two" {
		t.Errorf("values = %+v, %v; want both pages", values, err)
	}

	if out, err := run(ctx, "gh", "api", "user/memberships/orgs"); err == nil || len(out) != 0 {
		t.Errorf("a read about the person = %q, %v; want it refused", out, err)
	}
	_, err = ghauth.ReadPropertySchema(ctx, run, "actions")
	var problem *ghauth.Problem
	if !errors.As(err, &problem) || !strings.Contains(problem.Reason, "actions") || strings.Contains(problem.Remedy, "gh auth") {
		t.Errorf("an account without an installation = %v; want the App's problem, not gh's", err)
	}
	if bytes.Contains([]byte(problem.Reason+problem.Remedy), []byte("ghs_")) {
		t.Errorf("a problem carries a token: %+v", problem)
	}
}
