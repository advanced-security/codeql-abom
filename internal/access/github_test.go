package access

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// gitHub stands in for the App: it remembers the challenge it was asked with, signs in whoever it
// was told to with the verifier that answers it, and reports memberships it was given.
type gitHub struct {
	challenge string
	person    GitHubUser
	// roles is a person's role by "org|login" or "org/team|login".
	roles map[string]string
	down  error
	asked int
}

func (g *gitHub) AuthorizeURL(state, challenge, redirect string) string {
	g.challenge = challenge
	return "https://github.example/login/oauth/authorize?" + url.Values{"state": {state}, "redirect_uri": {redirect}}.Encode()
}

func (g *gitHub) SignIn(_ context.Context, code, verifier, _ string) (GitHubUser, error) {
	sum := sha256.Sum256([]byte(verifier))
	if code != "code-1" || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		return GitHubUser{}, errors.New("bad_verification_code")
	}
	return g.person, nil
}

func (g *gitHub) Role(_ context.Context, org, team, login string) (string, error) {
	g.asked++
	if g.down != nil {
		return "", g.down
	}
	group := org
	if team != "" {
		group += "/" + team
	}
	return g.roles[group+"|"+login], nil
}

func newGitHubSignIn(t *testing.T, g *gitHub, roleMap map[string]string) (*Authenticator, *time.Time) {
	t.Helper()
	a, err := NewGitHub(GitHubConfig{App: g, Web: "https://github.com", RedirectURL: "https://abom.example/auth/callback",
		RoleMap: roleMap, Refresh: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	return a, &now
}

func signInWithGitHub(t *testing.T, a *Authenticator) *http.Cookie {
	t.Helper()
	login := httptest.NewRecorder()
	a.Login(login, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	u, err := url.Parse(login.Header().Get("Location"))
	if err != nil || u.Host != "github.example" {
		t.Fatalf("login went to %q, not GitHub", login.Header().Get("Location"))
	}
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code=code-1&state="+u.Query().Get("state"), nil)
	for _, c := range login.Result().Cookies() {
		r.AddCookie(c)
	}
	done := httptest.NewRecorder()
	a.Callback(done, r)
	c := session(done.Result().Cookies())
	if c == nil {
		t.Fatalf("signing in with GitHub left no session: %d %s", done.Code, done.Header().Get("Location"))
	}
	return c
}

func changing(c *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/discovery/start", nil)
	r.AddCookie(c)
	return r
}

var octocat = GitHubUser{ID: 583231, Login: "octocat", Name: "The Octocat"}

// GitHub vouches for the account, and the account's ID names the person: a renamed login is the
// same person. The role is the highest any mapped organization or team grants.
func TestSigningInWithGitHubNamesThePersonByTheirAccount(t *testing.T) {
	g := &gitHub{person: octocat, roles: map[string]string{"acme|octocat": "member", "acme/abom-operators|octocat": "member"}}
	a, _ := newGitHubSignIn(t, g, map[string]string{"acme": authz.RoleViewer, "acme/abom-operators": authz.RoleOperator})

	u, ok := a.User(asked(signInWithGitHub(t, a)))

	if !ok || u.ID != "https://github.com#583231" || u.Username != "octocat" || u.Name != "The Octocat" ||
		u.Role != authz.RoleOperator {
		t.Errorf("User = %+v, %v; want the Octocat by account ID, as an operator", u, ok)
	}
}

// An organization's owners are a role only where the map says so; membership alone grants what the
// organization maps to.
func TestOwnersAreMappedOnlyWhereTheMapSaysSo(t *testing.T) {
	roles := map[string]string{"acme|octocat": "admin", "acme|hubot": "member"}
	for login, want := range map[string]string{"octocat": authz.RoleAdmin, "hubot": authz.RoleViewer} {
		g := &gitHub{person: GitHubUser{ID: 1, Login: login}, roles: roles}
		a, _ := newGitHubSignIn(t, g, map[string]string{"acme": authz.RoleViewer, "acme:owners": authz.RoleAdmin})
		if u, _ := a.User(asked(signInWithGitHub(t, a))); u.Role != want {
			t.Errorf("%s signed in as %q; want %q", login, u.Role, want)
		}
	}
}

// Someone removed from a team loses what it granted the next time they change something after the
// refresh window, not whenever their session happens to end. Reading is not held up to ask.
func TestARemovedMemberLosesTheRoleOnTheirNextChangeAfterTheRefreshWindow(t *testing.T) {
	g := &gitHub{person: octocat, roles: map[string]string{"acme/abom-operators|octocat": "member"}}
	a, now := newGitHubSignIn(t, g, map[string]string{"acme/abom-operators": authz.RoleOperator})
	c := signInWithGitHub(t, a)
	delete(g.roles, "acme/abom-operators|octocat")

	*now = now.Add(4 * time.Minute)
	if u, _ := a.User(changing(c)); u.Role != authz.RoleOperator {
		t.Errorf("within the window the role is %q; want it as read at sign-in", u.Role)
	}
	*now = now.Add(2 * time.Minute)
	if u, _ := a.User(asked(c)); u.Role != authz.RoleOperator {
		t.Errorf("a read re-asked GitHub and saw %q; reads use the role read at sign-in", u.Role)
	}
	if u, ok := a.User(changing(c)); !ok || u.Role != "" {
		t.Errorf("after the window a change runs as %+v, %v; want them signed in with no role", u, ok)
	}
}

// A role GitHub cannot be asked about is no role for a change, which fails closed; reading carries on.
func TestAMembershipThatCannotBeReadRefusesTheChangeNotTheReading(t *testing.T) {
	g := &gitHub{person: octocat, roles: map[string]string{"acme/abom-operators|octocat": "member"}}
	a, now := newGitHubSignIn(t, g, map[string]string{"acme/abom-operators": authz.RoleOperator})
	c := signInWithGitHub(t, a)
	g.down = errors.New("GitHub is unavailable")
	*now = now.Add(6 * time.Minute)

	if u, _ := a.User(changing(c)); u.Role != "" {
		t.Errorf("a change ran as %q while GitHub could not say; want no role", u.Role)
	}
	if u, _ := a.User(asked(c)); u.Role != authz.RoleOperator {
		t.Errorf("a read ran as %q; want the role read at sign-in", u.Role)
	}
}

// A map entry that names no ABOM role, or no organization, is refused before anyone signs in.
func TestARoleMapThatCannotBeReadIsRefused(t *testing.T) {
	for _, bad := range []map[string]string{{"acme": "superuser"}, {"/team": authz.RoleViewer}, {"acme:members": authz.RoleViewer}} {
		if _, err := NewGitHub(GitHubConfig{App: &gitHub{}, Web: "https://github.com",
			RedirectURL: "https://abom.example/auth/callback", RoleMap: bad}); err == nil {
			t.Errorf("NewGitHub accepted the role map %v", bad)
		}
	}
}

// GitHub signs people in as GitHub, and as the server's host when it is not github.com, and the
// page offers to sign in with it by that name.
func TestSigningInWithGitHubSaysSo(t *testing.T) {
	g := &gitHub{person: octocat, roles: map[string]string{"acme|octocat": "member"}}
	a, _ := newGitHubSignIn(t, g, map[string]string{"acme": authz.RoleViewer})
	if u, _ := a.User(asked(signInWithGitHub(t, a))); u.Provider != "GitHub" {
		t.Errorf("Provider = %q; want GitHub", u.Provider)
	}
	w := httptest.NewRecorder()
	a.Page(w, httptest.NewRequest(http.MethodGet, "/auth/", nil))
	if !strings.Contains(w.Body.String(), "Sign in with GitHub") {
		t.Errorf("the sign-in page says %q; want it to offer GitHub", w.Body.String())
	}

	enterprise, err := NewGitHub(GitHubConfig{App: g, Web: "https://ghe.example.com", RedirectURL: "https://abom.example/auth/callback",
		RoleMap: map[string]string{"acme": authz.RoleViewer}})
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := enterprise.User(asked(signInWithGitHub(t, enterprise))); u.Provider != "GitHub at ghe.example.com" {
		t.Errorf("Provider = %q; want GitHub at ghe.example.com", u.Provider)
	}
}

// Each membership that granted a role is said with the role it granted, so somebody can see which
// one they hold their role by; the highest is the one that applies.
func TestEachGitHubMembershipSaysWhichRoleItGranted(t *testing.T) {
	g := &gitHub{person: octocat, roles: map[string]string{"acme|octocat": "admin", "acme/abom-operators|octocat": "member"}}
	a, _ := newGitHubSignIn(t, g, map[string]string{"acme": authz.RoleViewer, "acme:owners": authz.RoleAdmin,
		"acme/abom-operators": authz.RoleOperator, "acme/other": authz.RoleAnalyst})

	u, _ := a.User(asked(signInWithGitHub(t, a)))

	want := []Grant{{By: "acme", Role: authz.RoleViewer}, {By: "acme/abom-operators", Role: authz.RoleOperator},
		{By: "acme:owners", Role: authz.RoleAdmin}}
	if !slices.Equal(u.Grants, want) || u.Role != authz.RoleAdmin {
		t.Errorf("Grants = %+v, Role = %q; want %+v and admin", u.Grants, u.Role, want)
	}
}
