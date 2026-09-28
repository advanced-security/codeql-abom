package ghauth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const userJSON = `{"login":"octocat","type":"User","html_url":"https://github.com/octocat"}`

// gh -i prints the response headers, a blank line, then the body. The scopes are only ever
// stated there, and they decide whether an unlisted organization is a contradiction or a
// question nobody could answer.
func withHeaders(scopes, body string) []byte {
	h := "HTTP/2.0 200 OK\r\nContent-Type: application/json\r\n"
	if scopes != "" {
		h += "X-Oauth-Scopes: " + scopes + "\r\n"
	}
	return []byte(h + "\r\n" + body)
}

const membershipsJSON = `[
  {"state":"active","role":"admin","organization":{"login":"octo-org"}},
  {"state":"active","role":"member","organization":{"login":"another-org"}}
]`

// fakeGH answers the calls a test cares about and records what was asked, so a test can assert
// on the arguments as well as the result.
type fakeGH struct {
	replies map[string]reply
	calls   [][]string
}

type reply struct {
	out []byte
	err error
}

func (f *fakeGH) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	r, ok := f.replies[strings.Join(args, " ")]
	if !ok {
		return nil, fmt.Errorf("unexpected call: %s %s", name, strings.Join(args, " "))
	}
	return r.out, r.err
}

func working() *fakeGH {
	return &fakeGH{replies: map[string]reply{
		"api -i user":                          {out: withHeaders("gist, read:org, repo", userJSON)},
		"api user/memberships/orgs --paginate": {out: []byte(membershipsJSON)},
	}}
}

// Without this the corpus cannot tell "you do not belong to facebook" from "this token could
// never have seen facebook", and it presented the second as the first.
func TestVerifyRecordsTheScopesTheTokenCarries(t *testing.T) {
	id, err := Verify(context.Background(), working().run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	want := []string{"gist", "read:org", "repo"}
	if len(id.Scopes) != len(want) {
		t.Fatalf("scopes %v, want %v", id.Scopes, want)
	}
	for i, s := range id.Scopes {
		if s != want[i] {
			t.Errorf("scope %d is %q, want %q", i, s, want[i])
		}
	}
}

// A token that reports no scopes at all — a fine-grained PAT, say — must not be recorded as one
// that carries them, and must not be invented into one either.
func TestVerifyRecordsNoScopesWhenGhDoesNotReportThem(t *testing.T) {
	gh := working()
	gh.replies["api -i user"] = reply{out: withHeaders("", userJSON)}

	id, err := Verify(context.Background(), gh.run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Scopes) != 0 {
		t.Errorf("scopes %v, want none reported", id.Scopes)
	}
}

func TestVerifyReportsTheAuthenticatedAccountAndEveryOrgItBelongsTo(t *testing.T) {
	id, err := Verify(context.Background(), working().run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if id.Login != "octocat" {
		t.Errorf("login %q, want octocat", id.Login)
	}
	want := []Account{
		// The authenticated account comes first: it is the one membership that needs no lookup.
		{Owner: "octocat", Kind: "user", Role: "owner"},
		{Owner: "another-org", Kind: "organization", Role: "member"},
		{Owner: "octo-org", Kind: "organization", Role: "admin"},
	}
	if len(id.Accounts) != len(want) {
		t.Fatalf("accounts %+v, want %+v", id.Accounts, want)
	}
	for i, a := range id.Accounts {
		if a != want[i] {
			t.Errorf("account %d is %+v, want %+v", i, a, want[i])
		}
	}
	if id.VerifiedAt.IsZero() {
		t.Error("a verified identity has to record when it was verified")
	}
}

func TestHostComesFromTheAccountRatherThanBeingAssumed(t *testing.T) {
	// An enterprise install answers on its own host.
	gh := working()
	gh.replies["api -i user"] = reply{out: withHeaders("read:org",
		`{"login":"octocat","type":"User","html_url":"https://ghe.example.com/octocat"}`)}

	id, err := Verify(context.Background(), gh.run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.Host != "ghe.example.com" {
		t.Errorf("host %q, want ghe.example.com", id.Host)
	}
}

func TestAPendingInvitationIsNotMembership(t *testing.T) {
	gh := working()
	gh.replies["api user/memberships/orgs --paginate"] = reply{out: []byte(`[
      {"state":"pending","role":"member","organization":{"login":"not-yet"}},
      {"state":"active","role":"member","organization":{"login":"octo-org"}}
    ]`)}

	id, err := Verify(context.Background(), gh.run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, a := range id.Accounts {
		if a.Owner == "not-yet" {
			t.Error("an invitation that has not been accepted was counted as membership")
		}
	}
}

func TestEveryPageOfMembershipsIsRead(t *testing.T) {
	// gh 2.97 merges --paginate into one array; older versions emit one array per page back to
	// back. Both have to work, or the org list quietly stops at the first page.
	for _, tc := range []struct {
		name string
		out  string
	}{
		{"merged into one array", `[{"state":"active","role":"member","organization":{"login":"first-org"}},
		   {"state":"active","role":"admin","organization":{"login":"second-org"}}]`},
		{"one array per page", `[{"state":"active","role":"member","organization":{"login":"first-org"}}]` +
			`[{"state":"active","role":"admin","organization":{"login":"second-org"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gh := working()
			gh.replies["api user/memberships/orgs --paginate"] = reply{out: []byte(tc.out)}

			id, err := Verify(context.Background(), gh.run)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			found := map[string]bool{}
			for _, a := range id.Accounts {
				found[a.Owner] = true
			}
			if !found["first-org"] || !found["second-org"] {
				t.Errorf("accounts %+v, want both pages", id.Accounts)
			}
		})
	}
}

func TestAMissingGHSaysHowToInstallIt(t *testing.T) {
	gh := &fakeGH{replies: map[string]reply{
		"api -i user": {err: fmt.Errorf("looking for gh: %w", ErrNotInstalled)},
	}}

	_, err := Verify(context.Background(), gh.run)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("error %v, want a Problem the operator can act on", err)
	}
	if !strings.Contains(p.Remedy, "cli.github.com") {
		t.Errorf("remedy %q does not say where to get gh", p.Remedy)
	}
}

func TestBeingLoggedOutSaysHowToLogIn(t *testing.T) {
	// Verbatim from gh 2.97 with no stored credentials.
	gh := &fakeGH{replies: map[string]reply{
		"api -i user": {err: errors.New(
			"To get started with GitHub CLI, please run:  gh auth login")},
	}}

	_, err := Verify(context.Background(), gh.run)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("error %v, want a Problem the operator can act on", err)
	}
	if !strings.Contains(p.Remedy, "gh auth login") {
		t.Errorf("remedy %q does not name the command that fixes it", p.Remedy)
	}
}

func TestARejectedTokenSaysHowToLogInAgain(t *testing.T) {
	// Verbatim from gh 2.97 with an invalid token: the status arrives on stderr, which Exec
	// folds into the error.
	gh := &fakeGH{replies: map[string]reply{
		"api -i user": {err: errors.New("gh api user: exit status 1: gh: Bad credentials (HTTP 401)")},
	}}

	_, err := Verify(context.Background(), gh.run)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("error %v, want a Problem the operator can act on", err)
	}
	if !strings.Contains(p.Remedy, "gh auth login") {
		t.Errorf("remedy %q does not name the command that fixes it", p.Remedy)
	}
}

func TestATokenWithoutOrgScopeNamesTheCommandThatGrantsIt(t *testing.T) {
	// The user call succeeds on any token, so the scope only fails at the membership lookup.
	// Reporting that as "no orgs" would be a guess dressed as a measurement.
	gh := working()
	gh.replies["api user/memberships/orgs --paginate"] = reply{err: errors.New(
		"gh: Requires at least one of the following scopes: ['read:org'] (HTTP 403)")}

	_, err := Verify(context.Background(), gh.run)
	var p *Problem
	if !errors.As(err, &p) {
		t.Fatalf("error %v, want a Problem the operator can act on", err)
	}
	if !strings.Contains(p.Remedy, "gh auth refresh") || !strings.Contains(p.Remedy, "read:org") {
		t.Errorf("remedy %q does not name the scope or the command that grants it", p.Remedy)
	}
}

func TestBelongingToNoOrgIsAnAnswerRatherThanAFailure(t *testing.T) {
	gh := working()
	gh.replies["api user/memberships/orgs --paginate"] = reply{out: []byte(`[]`)}

	id, err := Verify(context.Background(), gh.run)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(id.Accounts) != 1 || id.Accounts[0].Owner != "octocat" {
		t.Errorf("accounts %+v, want only the authenticated account", id.Accounts)
	}
}

func TestNothingFromGitHubReachesTheCommandLine(t *testing.T) {
	// Account names arrive from a remote API. They are read out of the response and never
	// interpolated into an argument, so the calls are the same fixed ones every time.
	gh := working()
	if _, err := Verify(context.Background(), gh.run); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	want := [][]string{
		{"gh", "api", "-i", "user"},
		{"gh", "api", "user/memberships/orgs", "--paginate"},
	}
	if len(gh.calls) != len(want) {
		t.Fatalf("calls %v, want %v", gh.calls, want)
	}
	for i, c := range gh.calls {
		if strings.Join(c, " ") != strings.Join(want[i], " ") {
			t.Errorf("call %d is %v, want %v", i, c, want[i])
		}
	}
}

func TestAnUnreadableAnswerIsAnErrorRatherThanAnEmptyIdentity(t *testing.T) {
	gh := working()
	gh.replies["api -i user"] = reply{out: withHeaders("read:org", `{"login":`)}

	if _, err := Verify(context.Background(), gh.run); err == nil {
		t.Error("a truncated response was accepted as an identity")
	}
}

func TestAnAnswerWithoutALoginIsRejected(t *testing.T) {
	// An empty login would otherwise be stored as an account named "", which nothing owns.
	gh := working()
	gh.replies["api -i user"] = reply{out: withHeaders("read:org", `{"login":"","type":"User"}`)}

	if _, err := Verify(context.Background(), gh.run); err == nil {
		t.Error("an identity with no login was accepted")
	}
}
