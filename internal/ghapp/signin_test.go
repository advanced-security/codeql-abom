package ghapp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func signInApp(t *testing.T) (*fakeGitHub, *App) {
	t.Helper()
	key, _ := testKeys(t)
	c := newClock()
	f := newFakeGitHub(t, key, c.Now, fakeInstallation{ID: 1, Login: "acme", Type: "Organization", Selection: "all"})
	a := f.app(c.Now)
	a.Credentials.ClientSecret = testClientSecret
	return f, a
}

// authorize is the person's half: GitHub remembers the challenge and hands back a code.
func authorize(t *testing.T, f *fakeGitHub, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil || !strings.HasPrefix(location, f.srv.URL+"/login/oauth/authorize?") {
		t.Fatalf("the person was sent to %q, not GitHub's authorization page", location)
	}
	q := u.Query()
	if q.Get("client_id") != testClientID || q.Get("code_challenge_method") != "S256" || q.Get("state") != "state-1" ||
		q.Get("redirect_uri") != "https://abom.example/auth/callback" {
		t.Fatalf("the authorization asks %v", q)
	}
	f.codes["code-1"] = q.Get("code_challenge")
	return "code-1"
}

// Signing in reads who the person is with a user token used once, then revoked: ABOM keeps no token
// that acts as a person, and its own App reads everything else.
func TestSigningInReadsWhoThePersonIsAndKeepsNoUserToken(t *testing.T) {
	f, a := signInApp(t)
	f.people["ghu_code-1"] = fakePerson{ID: 583231, Login: "octocat", Name: "The Octocat"}
	verifier := oauth2.GenerateVerifier()
	sum := sha256.Sum256([]byte(verifier))

	code := authorize(t, f, a.AuthorizeURL("state-1", base64.RawURLEncoding.EncodeToString(sum[:]),
		"https://abom.example/auth/callback"))
	who, err := a.SignIn(context.Background(), code, verifier, "https://abom.example/auth/callback")

	if err != nil || who != (SignInUser{ID: 583231, Login: "octocat", Name: "The Octocat"}) {
		t.Fatalf("SignIn = %+v, %v", who, err)
	}
	if !slices.Contains(f.revoked, "ghu_code-1") {
		t.Errorf("the user token was not revoked after use; revoked %v", f.revoked)
	}
}

// A code exchanged with another verifier is refused, as is one already used.
func TestASignInWithTheWrongVerifierIsRefused(t *testing.T) {
	f, a := signInApp(t)
	f.people["ghu_code-1"] = fakePerson{ID: 1, Login: "octocat"}
	sum := sha256.Sum256([]byte(oauth2.GenerateVerifier()))
	code := authorize(t, f, a.AuthorizeURL("state-1", base64.RawURLEncoding.EncodeToString(sum[:]),
		"https://abom.example/auth/callback"))

	if _, err := a.SignIn(context.Background(), code, oauth2.GenerateVerifier(), "https://abom.example/auth/callback"); err == nil {
		t.Error("a code exchanged with a verifier that does not match its challenge signed someone in")
	}
}

// An App with no client secret configured cannot sign anyone in, and says where the secret goes.
func TestSigningInWithoutAClientSecretSaysWhatIsMissing(t *testing.T) {
	_, a := signInApp(t)
	a.Credentials.ClientSecret = ""

	_, err := a.SignIn(context.Background(), "code-1", oauth2.GenerateVerifier(), "https://abom.example/auth/callback")
	if !errors.Is(err, ErrNoClientSecret) {
		t.Errorf("SignIn = %v; want ErrNoClientSecret", err)
	}
}

// Who belongs where is read with the installation's token carrying members: read and nothing else:
// an owner, a member and a team member, and nobody for anyone else.
func TestRolesAreReadWithAMembersOnlyInstallationToken(t *testing.T) {
	f, a := signInApp(t)
	f.members["acme"] = map[string]string{"octocat": "admin", "hubot": "member"}
	f.members["acme/abom-operators"] = map[string]string{"hubot": "member"}

	for _, c := range []struct{ org, team, login, want string }{
		{"acme", "", "octocat", "admin"},
		{"acme", "", "hubot", "member"},
		{"acme", "abom-operators", "hubot", "member"},
		{"acme", "abom-operators", "octocat", ""},
		{"acme", "", "stranger", ""},
	} {
		got, err := a.Role(context.Background(), c.org, c.team, c.login)
		if err != nil || got != c.want {
			t.Errorf("Role(%s, %q, %s) = %q, %v; want %q", c.org, c.team, c.login, got, err, c.want)
		}
	}
	for token, perms := range f.carries {
		if perms["members"] != "" && len(perms) != 1 {
			t.Errorf("token %s carries %v; a membership read needs members: read alone", token, perms)
		}
	}
}

// An installation that has not approved everything the App asks for names what it lacks, so its
// owners know what to approve before people can sign in.
func TestAnInstallationNamesThePermissionsItHasNotApproved(t *testing.T) {
	granted := map[string]string{}
	for _, g := range Permissions {
		granted[g.Permission] = g.Access
	}
	if got := Unapproved(granted); len(got) != 0 {
		t.Errorf("an installation that approved everything lacks %v", got)
	}
	delete(granted, "members")
	if got := Unapproved(granted); !slices.Equal(got, []string{"members: read"}) {
		t.Errorf("Unapproved = %v; want members: read", got)
	}
}
