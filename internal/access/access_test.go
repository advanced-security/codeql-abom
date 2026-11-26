package access

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/access/oidctest"
	"github.com/advanced-security/codeql-abom/internal/authz"
)

// issuer is the shared provider in miniature, where Ada signs in under the subject user-1.
type issuer struct {
	*oidctest.Issuer
	srv *httptest.Server
}

// ada is Ada as the provider describes her, with claims over the usual ones.
func ada(claims map[string]any) oidctest.Person {
	c := map[string]any{"preferred_username": "ada@example.com"}
	maps.Copy(c, claims)
	return oidctest.Person{Name: "Ada Lovelace", Claims: c}
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	i := &issuer{}
	i.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { i.ServeHTTP(w, r) }))
	t.Cleanup(i.srv.Close)
	i.Issuer = oidctest.New(i.srv.URL, "abom", map[string]oidctest.Person{"user-1": ada(nil)})
	return i
}

// authorize is the provider's half of the redirect, as a person signing in would cause: it must
// carry PKCE and a nonce, and comes back with a code.
func (i *issuer) authorize(t *testing.T, location string) (code, state string) {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil || !strings.HasPrefix(location, i.srv.URL+"/authorize") {
		t.Fatalf("login redirected to %q, not the issuer", location)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
		t.Fatalf("the authorization request lacks PKCE or a nonce: %s", location)
	}
	client := i.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Get(location)
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("the issuer answered the authorization with %v, %v", resp, err)
	}
	resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return back.Query().Get("code"), back.Query().Get("state")
}

func newAuthenticator(t *testing.T, i *issuer) *Authenticator {
	t.Helper()
	a, err := New(context.Background(), Config{Issuer: i.srv.URL, ClientID: "abom", ClientSecret: "s3cret",
		RedirectURL: "https://abom.example/auth/callback"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// signIn runs the browser's part of the flow and returns the cookies it ends up holding.
func signIn(t *testing.T, a *Authenticator, i *issuer, returnTo string) (*httptest.ResponseRecorder, []*http.Cookie) {
	t.Helper()
	login := httptest.NewRecorder()
	a.Login(login, httptest.NewRequest(http.MethodGet, "/auth/login?return="+url.QueryEscape(returnTo), nil))
	if login.Code != http.StatusFound {
		t.Fatalf("login answered %d", login.Code)
	}
	code, state := i.authorize(t, login.Header().Get("Location"))
	r := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state="+state, nil)
	for _, c := range login.Result().Cookies() {
		r.AddCookie(c)
	}
	done := httptest.NewRecorder()
	a.Callback(done, r)
	return done, done.Result().Cookies()
}

func session(cookies []*http.Cookie) *http.Cookie {
	for _, c := range cookies {
		if c.Name == SessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func asked(c *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/summary", nil)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

// Signing in establishes who the person is and the highest ABOM role their app roles grant, held
// in a session only the server can read, in a cookie scripts cannot touch.
func TestSigningInEstablishesTheUserAndTheirHighestRole(t *testing.T) {
	i := newIssuer(t)
	i.People["user-1"] = ada(map[string]any{"roles": []string{"ABOM.Viewer", "ABOM.Operator", "Unrelated.Role"}})
	a := newAuthenticator(t, i)

	done, cookies := signIn(t, a, i, "/inventory?tab=coverage")
	c := session(cookies)
	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || !c.Secure {
		t.Fatalf("session cookie = %+v; want HttpOnly, SameSite=Strict, Secure", c)
	}
	if !strings.Contains(done.Body.String(), "/inventory?tab=coverage") {
		t.Errorf("after signing in the page goes to %q; want where the person was", done.Body.String())
	}
	u, ok := a.User(asked(c))
	if !ok || u.Username != "ada@example.com" || u.Name != "Ada Lovelace" || u.Role != authz.RoleOperator {
		t.Errorf("User = %+v, %v; want Ada as an operator", u, ok)
	}
}

// A person is named by their provider's issuer and its subject for them, which a changed address or
// a renamed account leaves alone: decisions signed before and after name the same person.
func TestAPersonIsNamedByWhatTheirProviderKeepsNotWhatTheyCanChange(t *testing.T) {
	i := newIssuer(t)
	i.People["user-1"] = ada(map[string]any{"roles": []string{"ABOM.Viewer"}})
	a := newAuthenticator(t, i)
	_, before := signIn(t, a, i, "/")
	i.People["user-1"].Claims["preferred_username"] = "ada.lovelace@example.com"
	_, after := signIn(t, a, i, "/")

	first, _ := a.User(asked(session(before)))
	second, _ := a.User(asked(session(after)))
	if first.ID != i.srv.URL+"#user-1" || second.ID != first.ID {
		t.Errorf("IDs = %q then %q; want %q both times", first.ID, second.ID, i.srv.URL+"#user-1")
	}
	if second.Username != "ada.lovelace@example.com" || second.Principal().Name != "ada.lovelace@example.com" {
		t.Errorf("User = %+v; the new address is what they are called, and evidence of who asked", second)
	}
}

// A provider that sends groups as one string, as some do for a single membership, still grants the
// role that string maps to.
func TestARoleClaimOfOneStringGrantsItsRole(t *testing.T) {
	i := newIssuer(t)
	i.People["user-1"] = ada(map[string]any{"groups": "abom-operators"})
	a, err := New(context.Background(), Config{Issuer: i.srv.URL, ClientID: "abom", ClientSecret: "s3cret",
		RedirectURL: "https://abom.example/auth/callback", RolesClaim: "groups",
		RoleMap: map[string]string{"abom-operators": authz.RoleOperator}})
	if err != nil {
		t.Fatal(err)
	}

	_, cookies := signIn(t, a, i, "/")

	if u, ok := a.User(asked(session(cookies))); !ok || u.Role != authz.RoleOperator {
		t.Errorf("User = %+v, %v; want an operator", u, ok)
	}
}

// A callback that does not answer this browser's own login, or an ID token not issued for this
// client, signs nobody in, and sends the browser back to the interface saying which it was.
func TestACallbackThatIsNotThisLoginsAnswerSignsNobodyIn(t *testing.T) {
	t.Run("another state", func(t *testing.T) {
		i := newIssuer(t)
		a := newAuthenticator(t, i)
		login := httptest.NewRecorder()
		a.Login(login, httptest.NewRequest(http.MethodGet, "/auth/login", nil))
		code, _ := i.authorize(t, login.Header().Get("Location"))
		r := httptest.NewRequest(http.MethodGet, "/auth/callback?code="+code+"&state=forged", nil)
		for _, c := range login.Result().Cookies() {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		a.Callback(w, r)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/?signin=mismatch" || session(w.Result().Cookies()) != nil {
			t.Errorf("a forged state answered %d to %q with session %v", w.Code, w.Header().Get("Location"),
				session(w.Result().Cookies()))
		}
	})
	t.Run("another sign-in's nonce", func(t *testing.T) {
		i := newIssuer(t)
		i.Nonce = "replayed"
		a := newAuthenticator(t, i)
		w, cookies := signIn(t, a, i, "/")
		if w.Code == http.StatusOK || session(cookies) != nil {
			t.Errorf("a token carrying another nonce answered %d and signed in", w.Code)
		}
	})
	t.Run("another audience", func(t *testing.T) {
		i := newIssuer(t)
		i.Audience = "some-other-app"
		a := newAuthenticator(t, i)
		w, cookies := signIn(t, a, i, "/")
		if w.Header().Get("Location") != "/?signin=failed" || session(cookies) != nil {
			t.Errorf("a token for another client answered %d to %q and signed in", w.Code, w.Header().Get("Location"))
		}
	})
}

// No role, no access: a person ABOM's app roles say nothing about is signed in, and told so, with
// no role to act or read under.
func TestAPersonWithNoAppRoleHasNoRole(t *testing.T) {
	i := newIssuer(t)
	a := newAuthenticator(t, i)
	_, cookies := signIn(t, a, i, "/")
	u, ok := a.User(asked(session(cookies)))
	if !ok || u.Role != "" {
		t.Errorf("User = %+v, %v; want signed in with no role", u, ok)
	}
}

// Where the browser goes afterwards is a path on this site and nothing else.
func TestTheReturnAfterSigningInStaysOnThisSite(t *testing.T) {
	for _, target := range []string{"//evil.example/x", "https://evil.example/", "/\\evil.example"} {
		i := newIssuer(t)
		a := newAuthenticator(t, i)
		done, _ := signIn(t, a, i, target)
		if strings.Contains(done.Body.String(), "evil.example") {
			t.Errorf("return %q led off the site: %s", target, done.Body.String())
		}
	}
}

// A session ends when it expires or its holder signs out, and a cookie nobody issued is no session.
func TestASessionEndsAndCannotBeInvented(t *testing.T) {
	i := newIssuer(t)
	a := newAuthenticator(t, i)
	_, cookies := signIn(t, a, i, "/")
	c := session(cookies)

	if _, ok := a.User(asked(&http.Cookie{Name: SessionCookie, Value: "invented"})); ok {
		t.Error("an invented session was honored")
	}
	out := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	r.AddCookie(c)
	a.Logout(out, r)
	if _, ok := a.User(asked(c)); ok {
		t.Error("a session outlived signing out")
	}

	_, cookies = signIn(t, a, i, "/")
	c = session(cookies)
	a.now = func() time.Time { return time.Now().Add(defaultSessionTTL + time.Minute) }
	if _, ok := a.User(asked(c)); ok {
		t.Error("a session outlived its lifetime")
	}
}

// Anyone can ask to sign in, so what is held for attempts not yet answered is bounded, and the newest
// attempt still completes however many came before it.
func TestAttemptsToSignInAreHeldWithinABound(t *testing.T) {
	i := newIssuer(t)
	i.People["user-1"] = ada(map[string]any{"roles": []string{"ABOM.Viewer"}})
	a := newAuthenticator(t, i)

	for range maxPendingLogins + 50 {
		a.Login(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/auth/login", nil))
	}

	a.mu.Lock()
	held := len(a.logins)
	a.mu.Unlock()
	if held > maxPendingLogins {
		t.Errorf("%d attempts held; want at most %d", held, maxPendingLogins)
	}
	if _, cookies := signIn(t, a, i, "/"); session(cookies) == nil {
		t.Error("the newest attempt did not complete after the bound was reached")
	}
}
