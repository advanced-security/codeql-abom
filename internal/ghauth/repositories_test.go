package ghauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const orgReposCall = "api orgs/has-ghas/repos?type=all&per_page=100 --paginate"

// gh versions differ on whether --paginate emits one array or adjacent arrays, and an
// organization with more than a hundred repositories is exactly where the second page matters.
func TestAnAccountsRepositoriesAreReadFromEveryPage(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{orgReposCall: {out: []byte(`[
	  {"name":"zeta","private":true,"archived":false,"fork":false,"owner":{"login":"has-ghas"}}
	][
	  {"name":"alpha","private":false,"archived":true,"fork":true,"owner":{"login":"has-ghas"}}
	]`)}}}

	got, err := ListRepositories(context.Background(), gh.run, "has-ghas", false)
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	want := []Repository{
		{Name: "alpha", Archived: true, Fork: true},
		{Name: "zeta", Private: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("repository %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

// `users/{name}/repos` lists only public repositories even for their owner, so the operator's own
// account is read through `user/repos`, which is the only route that names its private ones.
func TestTheOperatorsOwnAccountIsReadWithItsPrivateRepositories(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api user/repos?affiliation=owner&per_page=100 --paginate": {out: []byte(
			`[{"name":"dotfiles","private":true,"owner":{"login":"octocat"}}]`)},
	}}

	got, err := ListRepositories(context.Background(), gh.run, "octocat", true)
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(got) != 1 || got[0].Name != "dotfiles" || !got[0].Private {
		t.Errorf("got %+v, want the private dotfiles repository", got)
	}
}

// An answer naming another owner's repository would put it under this account's include list,
// declaring something nobody chose.
func TestARepositoryAnotherAccountOwnsIsNotOffered(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{orgReposCall: {out: []byte(`[
	  {"name":"mine","owner":{"login":"Has-GHAS"}},
	  {"name":"theirs","owner":{"login":"elsewhere"}}
	]`)}}}

	got, err := ListRepositories(context.Background(), gh.run, "has-ghas", false)
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(got) != 1 || got[0].Name != "mine" {
		t.Errorf("got %+v, want only the repository has-ghas owns", got)
	}
}

// The account name becomes part of a `gh api` path. A name GitHub could never issue is refused
// before anything runs, so a request cannot steer gh at some other endpoint.
func TestANameGitHubCouldNotIssueIsNeverAsked(t *testing.T) {
	for _, owner := range []string{"", "has-ghas/../user", "-flag", "has ghas", "a?b", strings.Repeat("a", 40)} {
		gh := &fakeGH{replies: map[string]reply{}}
		if _, err := ListRepositories(context.Background(), gh.run, owner, false); err == nil {
			t.Errorf("%q: listed, want a refusal", owner)
		}
		if len(gh.calls) != 0 {
			t.Errorf("%q: gh was asked %v", owner, gh.calls)
		}
	}
}

// SAML enforcement answers 403 whatever the token's scopes, and the remedy is authorizing the token
// for that organization. Calling it a missing scope would send the operator to the wrong fix.
func TestASingleSignOnRefusalNamesTheAuthorizationItNeeds(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api orgs/codeql/repos?type=all&per_page=100 --paginate": {err: errors.New(
			"gh api: exit status 1: gh: Resource protected by organization SAML enforcement. " +
				"You must grant your OAuth token access to this organization. (HTTP 403)")},
	}}

	_, err := ListRepositories(context.Background(), gh.run, "codeql", false)

	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("err = %v, want a Problem", err)
	}
	if !strings.Contains(p.Reason, "single sign-on") || !strings.Contains(p.Remedy, "https://github.com/orgs/codeql/sso") {
		t.Errorf("problem %+v does not name single sign-on and where to authorize", p)
	}
	if strings.Contains(p.Remedy, "read:org") {
		t.Errorf("remedy %q sends the operator after a scope that does not fix SAML", p.Remedy)
	}
}

// A listing that fails on a later page is not a smaller account. Returning what arrived would
// offer a prefix as the whole and let a reader conclude a repository does not exist.
func TestAListingThatFailsPartWayOffersNothing(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{orgReposCall: {
		out: []byte(`[{"name":"first","owner":{"login":"has-ghas"}}]`),
		err: errors.New("gh api: exit status 1: HTTP 502: Bad Gateway"),
	}}}

	got, err := ListRepositories(context.Background(), gh.run, "has-ghas", false)

	if err == nil {
		t.Fatal("a failed page was reported as a complete listing")
	}
	if len(got) != 0 {
		t.Errorf("offered %+v from a listing that did not finish", got)
	}
}

func TestAnAccountGhCannotSeeSaysSo(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api orgs/nobody-here/repos?type=all&per_page=100 --paginate": {err: errors.New(
			"gh api: exit status 1: gh: Not Found (HTTP 404)")},
	}}

	_, err := ListRepositories(context.Background(), gh.run, "nobody-here", false)

	var p *Problem
	if !errors.As(err, &p) || !strings.Contains(p.Reason, "cannot see an organization named nobody-here") {
		t.Fatalf("err = %v, want a Problem saying gh cannot see the account", err)
	}
}
