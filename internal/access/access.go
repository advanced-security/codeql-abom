// Package access signs people in, through an OpenID Connect provider or through GitHub with ABOM's
// own App, and holds who they are and the ABOM role they hold, server-side, for the session.
package access

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// defaultRoleMap reads Entra app role values, as an app registration defines them, as ABOM roles.
var defaultRoleMap = map[string]string{
	"ABOM.Viewer": authz.RoleViewer, "ABOM.Analyst": authz.RoleAnalyst, "ABOM.Operator": authz.RoleOperator, "ABOM.Admin": authz.RoleAdmin,
}

// Cookie names. The session is Strict; the login's own state rides Lax, because the provider's
// redirect back is a cross-site navigation and a Strict cookie would not come with it.
const (
	SessionCookie = "abom_session"
	loginCookie   = "abom_login"
)

// How long a session lasts without signing in again. An identity provider's roles are read once,
// from the ID token, so a session through one is short enough that a role it takes away is gone
// within the hour; GitHub sign-in reads memberships again before a change, so its lasts the day.
const (
	oidcSessionTTL   = time.Hour
	githubSessionTTL = 8 * time.Hour
)

const loginTTL = 10 * time.Minute

// A session nobody has used for idleTTL ends whatever its lifetime. When it was last used is
// written at most every touchEvery, so following a session does not rewrite it on every request.
const (
	idleTTL    = 2 * time.Hour
	touchEvery = 5 * time.Minute
)

// SessionStore keeps sessions where a restart or a rollout does not reach: an opaque record by the
// digest of the session cookie, never the cookie, so a copy of what is kept signs nobody in.
type SessionStore interface {
	PutSession(digest string, record []byte, now, expires time.Time) error
	Session(digest string, now time.Time) ([]byte, bool, error)
	DeleteSession(digest string) error
}

// maxPendingLogins bounds the attempts held for the provider to answer, since anyone may start one.
const maxPendingLogins = 1024

// Config names the provider and this client registration with it.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	// RedirectURL is this deployment's /auth/callback as registered with the provider; its scheme
	// decides whether cookies are Secure, and its host is the one this deployment answers on.
	RedirectURL string
	// RolesClaim is the ID token claim holding app roles; empty is "roles", which is Entra's.
	RolesClaim string
	// RoleMap reads claim values as ABOM roles; nil is defaultRoleMap.
	RoleMap    map[string]string
	SessionTTL time.Duration
	// Sessions keeps who is signed in across restarts; nil holds them in memory alone.
	Sessions SessionStore
	// Log is where a refused sign-in's own reason goes; the browser is told only which kind it was.
	Log io.Writer
}

// User is a person signed in, as the provider vouched for them.
type User struct {
	// ID is how decisions name the person: the provider's issuer and its subject for them, which a
	// changed address or a renamed account leaves alone.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Username is what the provider calls them: a user principal name, a preferred username or an
	// email address, or a GitHub login.
	Username string `json:"username"`
	// Role is the highest ABOM role their app roles grant; empty is none.
	Role string `json:"role"`
	// Claimed is what the provider sent that the role map names: app roles or groups, or the
	// memberships GitHub confirmed.
	Claimed []string `json:"claimed"`
	// Grants are the claims that granted a role, each with the role it granted; Role is the highest.
	Grants  []Grant   `json:"grants"`
	Expires time.Time `json:"expires"`
	// Provider is who vouched for them, as a sentence names it: GitHub, or the issuer's host.
	Provider string `json:"provider"`
}

// Grant is one claim or membership and the ABOM role it granted.
type Grant struct {
	By   string `json:"by"`
	Role string `json:"role"`
}

// Called is how a sentence names the person.
func (u User) Called() string { return cmp.Or(u.Username, u.Name, u.ID) }

// Principal is the person as a decision names them: by ID, with what they were called as evidence.
func (u User) Principal() authz.Principal { return authz.SignedIn(u.ID, u.Called()) }

type pending struct {
	state, nonce, verifier, returnTo string
	expires                          time.Time
}

// provider is who vouches for a person: an OpenID Connect issuer, or GitHub through ABOM's App.
type provider interface {
	// authorizeURL is where the browser goes to sign in, for one attempt.
	authorizeURL(p pending) string
	// identify completes an attempt from the code the browser came back with.
	identify(ctx context.Context, code string, p pending) (User, error)
	// reread reads a person's role again; false where a provider's roles cannot change under a session.
	reread(ctx context.Context, u User) (role string, rereads bool, err error)
	// name is the provider as a sentence names it.
	name() string
}

type held struct {
	user User
	// roleAt is when the role was last read, and usedAt when the session was last used.
	roleAt, usedAt time.Time
}

// keptSession is a held session as the store keeps it.
type keptSession struct {
	User   User      `json:"user"`
	RoleAt time.Time `json:"roleAt"`
	UsedAt time.Time `json:"usedAt"`
}

// Authenticator runs the authorization code flow with PKCE as a backend for the interface: tokens
// never reach the browser, which holds only an opaque session cookie.
type Authenticator struct {
	provider provider
	redirect string
	secure   bool
	ttl      time.Duration
	// refresh is how old a role may be before a request that changes something reads it again.
	refresh time.Duration
	log     io.Writer
	now     func() time.Time
	keep    SessionStore

	mu       sync.Mutex
	logins   map[string]pending
	sessions map[string]held
}

func authenticate(p provider, redirectURL string, ttl, refresh time.Duration, keep SessionStore, log io.Writer) (*Authenticator, error) {
	redirect, err := url.Parse(redirectURL)
	if err != nil || redirect.Host == "" {
		return nil, fmt.Errorf("access: %q is not an absolute redirect URL", redirectURL)
	}
	return &Authenticator{provider: p, redirect: redirectURL, secure: redirect.Scheme == "https", ttl: ttl,
		refresh: refresh, keep: keep, log: log, now: time.Now, logins: map[string]pending{}, sessions: map[string]held{}}, nil
}

// oidcProvider signs people in through an OpenID Connect issuer, whose ID token carries their roles.
type oidcProvider struct {
	issuer     string
	oauth      oauth2.Config
	verifier   *oidc.IDTokenVerifier
	rolesClaim string
	roleMap    map[string]string
}

// New discovers the provider and readies the client.
func New(ctx context.Context, cfg Config) (*Authenticator, error) {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.RedirectURL == "" {
		return nil, errors.New("access: an issuer, a client ID and a redirect URL are required")
	}
	if _, err := url.Parse(cfg.RedirectURL); err != nil {
		return nil, fmt.Errorf("access: %q is not an absolute redirect URL", cfg.RedirectURL)
	}
	discovered, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("access: discovering %s: %w", cfg.Issuer, err)
	}
	issuer, _ := url.Parse(cfg.Issuer)
	p := &oidcProvider{
		issuer: cmp.Or(issuer.Host, cfg.Issuer),
		oauth: oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL,
			Endpoint: discovered.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
		verifier:   discovered.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		rolesClaim: cmp.Or(cfg.RolesClaim, "roles"),
		roleMap:    cfg.RoleMap,
	}
	if p.roleMap == nil {
		p.roleMap = defaultRoleMap
	}
	return authenticate(p, cfg.RedirectURL, cmp.Or(cfg.SessionTTL, oidcSessionTTL), 0, cfg.Sessions, cfg.Log)
}

// Host is the name this deployment answers on, from its redirect URL.
func (a *Authenticator) Host() string {
	u, _ := url.Parse(a.redirect)
	return u.Host
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Login sends the browser to the provider, remembering this attempt's state, nonce and verifier.
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	p := pending{state: random(), nonce: random(), verifier: oauth2.GenerateVerifier(),
		returnTo: localPath(r.URL.Query().Get("return")), expires: a.now().Add(loginTTL)}
	id := random()
	a.mu.Lock()
	oldest := ""
	for k, old := range a.logins {
		if a.now().After(old.expires) {
			delete(a.logins, k)
		} else if oldest == "" || old.expires.Before(a.logins[oldest].expires) {
			oldest = k
		}
	}
	// Full, the oldest attempt gives way: one started seconds ago is still being answered.
	if len(a.logins) >= maxPendingLogins {
		delete(a.logins, oldest)
	}
	a.logins[id] = p
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Value: id, Path: "/auth/", MaxAge: int(loginTTL.Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, a.provider.authorizeURL(p), http.StatusFound)
}

func (o *oidcProvider) authorizeURL(p pending) string {
	return o.oauth.AuthCodeURL(p.state, oidc.Nonce(p.nonce), oauth2.S256ChallengeOption(p.verifier))
}

// identify exchanges the code with this attempt's verifier; the ID token must verify for this client
// and carry the attempt's nonce.
func (o *oidcProvider) identify(ctx context.Context, code string, p pending) (User, error) {
	token, err := o.oauth.Exchange(ctx, code, oauth2.VerifierOption(p.verifier))
	if err != nil {
		return User{}, fmt.Errorf("the code could not be exchanged: %w", err)
	}
	raw, _ := token.Extra("id_token").(string)
	idToken, err := o.verifier.Verify(ctx, raw)
	if err != nil {
		return User{}, fmt.Errorf("the provider's ID token does not verify: %w", err)
	}
	if idToken.Nonce != p.nonce {
		return User{}, errors.New("the provider's ID token answers another sign-in")
	}
	return o.user(idToken)
}

// reread is never possible: the roles are the ID token's, read once at sign-in.
func (o *oidcProvider) reread(context.Context, User) (string, bool, error) { return "", false, nil }

func (o *oidcProvider) name() string { return o.issuer }

// Callback completes a login: the state must be this browser's, the code is exchanged with its
// verifier, and the ID token must verify for this client and carry the login's nonce.
func (a *Authenticator) Callback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(loginCookie)
	if err != nil {
		a.refuse(w, r, "not-started", "no sign-in was started in this browser")
		return
	}
	a.mu.Lock()
	p, ok := a.logins[cookie.Value]
	delete(a.logins, cookie.Value)
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: loginCookie, Path: "/auth/", MaxAge: -1, HttpOnly: true, Secure: a.secure})
	if !ok || a.now().After(p.expires) || r.URL.Query().Get("state") != p.state {
		a.refuse(w, r, "mismatch", "the callback does not answer the sign-in started here")
		return
	}
	if problem := r.URL.Query().Get("error"); problem != "" {
		a.refuse(w, r, "refused", "the provider refused the sign-in: "+problem+" "+r.URL.Query().Get("error_description"))
		return
	}
	u, err := a.provider.identify(r.Context(), r.URL.Query().Get("code"), p)
	if err != nil {
		a.refuse(w, r, "failed", err.Error())
		return
	}
	u.Expires, u.Provider = a.now().Add(a.ttl), a.provider.name()
	id := random()
	a.update(id, held{user: u, roleAt: a.now(), usedAt: a.now()}, true)
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", MaxAge: int(a.ttl.Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode})
	// A page that navigates on, rather than a redirect, so the Strict cookie goes with the next
	// request: a redirect would still count as the provider's cross-site navigation.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	target := html.EscapeString(p.returnTo)
	fmt.Fprintf(w, `<!doctype html><meta http-equiv="refresh" content="0;url=%s"><a href="%s">Continue to ABOM</a>`, target, target)
}

// refuse sends the browser to the sign-in page, which says why a sign-in failed from its kind alone:
// not-started, mismatch, refused or failed. The reason, in the provider's or the library's words,
// goes to the log, where whoever runs ABOM can read it and a crafted callback cannot write to the
// page.
func (a *Authenticator) refuse(w http.ResponseWriter, r *http.Request, kind, reason string) {
	if a.log != nil {
		fmt.Fprintf(a.log, "access: a sign-in was refused (%s): %s\n", kind, reason)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/auth/?signin="+kind, http.StatusFound)
}

// Page says where signing in stands for a browser with no session, and offers to sign in. It reads
// nothing ABOM holds, so it is served before anyone signs in, and it never signs in by itself:
// otherwise signing out would last only until the provider answered again, and a sign-in that
// keeps failing would loop.
func (a *Authenticator) Page(w http.ResponseWriter, r *http.Request) {
	said := "Sign in to use ABOM."
	switch r.URL.Query().Get("signin") {
	case "signed-out":
		said = "You signed out of ABOM. You may still be signed in to " + a.provider.name() +
			", which can sign you in again without asking."
	case "not-started":
		said = "That sign-in was not started in this browser, so it was not used."
	case "mismatch":
		said = "That sign-in did not answer the one this browser started, so it was not used."
	case "refused":
		said = a.provider.name() + " refused the sign-in."
	case "failed":
		said = "The sign-in could not be completed; whoever runs ABOM can see why in its log."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, signInPage, html.EscapeString(said), html.EscapeString(a.provider.name()))
}

// signInPage stands alone: the interface's own styles need a session, like everything it serves.
const signInPage = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in to ABOM</title>
<style>
:root { color-scheme: light dark; font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; }
body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: Canvas; color: CanvasText; }
main { max-width: 30rem; padding: 2rem; text-align: center; }
h1 { font-size: 1.25rem; font-weight: 600; margin: 0 0 0.75rem; }
p { margin: 0 0 1.5rem; line-height: 1.5; }
a { display: inline-block; padding: 0.5rem 1.25rem; border-radius: 6px; background: #1f6feb; color: #fff;
    font-weight: 600; text-decoration: none; }
a:focus-visible { outline: 2px solid #1f6feb; outline-offset: 2px; }
</style>
</head>
<body>
<main>
<h1>ABOM</h1>
<p data-abom-signin>%s</p>
<a href="/auth/login?return=%%2F">Sign in with %s</a>
</main>
</body>
</html>
`

func (o *oidcProvider) user(t *oidc.IDToken) (User, error) {
	var claims map[string]any
	if err := t.Claims(&claims); err != nil {
		return User{}, fmt.Errorf("the provider's claims cannot be read: %w", err)
	}
	text := func(name string) string { s, _ := claims[name].(string); return s }
	u := User{ID: t.Issuer + "#" + t.Subject, Name: text("name"), Username: cmp.Or(text("preferred_username"), text("email")),
		Claimed: []string{}, Grants: []Grant{}}
	values, _ := claims[o.rolesClaim].([]any)
	if one, ok := claims[o.rolesClaim].(string); ok {
		values = []any{one}
	}
	for _, v := range values {
		claimed, ok := v.(string)
		if !ok {
			continue
		}
		u.Claimed = append(u.Claimed, claimed)
		role := o.roleMap[claimed]
		if role == "" {
			continue
		}
		u.Grants = append(u.Grants, Grant{By: claimed, Role: role})
		if authz.Rank(role) > authz.Rank(u.Role) {
			u.Role = role
		}
	}
	return u, nil
}

// User is who holds the request's session, if anyone. A request that changes something, from a
// person whose role was read longer ago than the refresh window, reads it again first; one that
// cannot be read is no role for that request.
func (a *Authenticator) User(r *http.Request) (User, bool) {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return User{}, false
	}
	s, ok := a.held(cookie.Value)
	if !ok {
		return User{}, false
	}
	if now := a.now(); now.After(s.user.Expires) || now.Sub(s.usedAt) > idleTTL {
		a.forget(cookie.Value)
		return User{}, false
	} else if now.Sub(s.usedAt) > touchEvery {
		s.usedAt = now
		a.update(cookie.Value, s, false)
	}
	if a.refresh <= 0 || !changes(r) || a.now().Sub(s.roleAt) <= a.refresh {
		return s.user, ok
	}
	role, rereads, err := a.provider.reread(r.Context(), s.user)
	switch {
	case !rereads:
		return s.user, true
	case err != nil:
		if a.log != nil {
			fmt.Fprintf(a.log, "access: %s's role could not be read again, so this change runs with none: %v\n",
				s.user.Called(), err)
		}
		u := s.user
		u.Role = ""
		return u, true
	}
	s.user.Role, s.roleAt = role, a.now()
	a.update(cookie.Value, s, false)
	return s.user, true
}

// held is the session a cookie names, from memory or, after a restart, from where it is kept.
func (a *Authenticator) held(id string) (held, bool) {
	a.mu.Lock()
	s, ok := a.sessions[id]
	a.mu.Unlock()
	if ok || a.keep == nil {
		return s, ok
	}
	record, found, err := a.keep.Session(sessionDigest(id), a.now())
	if err != nil && a.log != nil {
		fmt.Fprintf(a.log, "access: a kept session could not be read, so it signs nobody in: %v\n", err)
	}
	var k keptSession
	if !found || err != nil || json.Unmarshal(record, &k) != nil {
		return held{}, false
	}
	s = held{user: k.User, roleAt: k.RoleAt, usedAt: k.UsedAt}
	a.mu.Lock()
	a.sessions[id] = s
	a.mu.Unlock()
	return s, true
}

// update holds a session, and keeps it where the next server reads it. Only a new one is added: a
// session ended while this request ran stays ended.
func (a *Authenticator) update(id string, s held, created bool) {
	a.mu.Lock()
	_, still := a.sessions[id]
	if created || still {
		a.sessions[id] = s
	}
	a.mu.Unlock()
	if (!created && !still) || a.keep == nil {
		return
	}
	record, err := json.Marshal(keptSession{User: s.user, RoleAt: s.roleAt, UsedAt: s.usedAt})
	if err == nil {
		err = a.keep.PutSession(sessionDigest(id), record, a.now(), s.user.Expires)
	}
	if err != nil && a.log != nil {
		fmt.Fprintf(a.log, "access: %s's session could not be kept, so a restart signs them out: %v\n", s.user.Called(), err)
	}
}

// forget ends a session here and wherever it is kept.
func (a *Authenticator) forget(id string) {
	a.mu.Lock()
	delete(a.sessions, id)
	a.mu.Unlock()
	if a.keep == nil {
		return
	}
	if err := a.keep.DeleteSession(sessionDigest(id)); err != nil && a.log != nil {
		fmt.Fprintf(a.log, "access: an ended session is still kept, until it expires: %v\n", err)
	}
}

// sessionDigest is how a session is named where it is kept: never by the cookie itself.
func sessionDigest(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// changes reports whether a request may change something, which is when a stale role is read again.
func changes(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

// Logout ends the session here. The provider's own session is the provider's to end.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(SessionCookie); err == nil {
		a.forget(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure,
		SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

// localPath keeps where a browser returns to on this site: a path, never another host.
func localPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	if u, err := url.Parse(p); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return p
}
