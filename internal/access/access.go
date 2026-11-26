// Package access signs people in, through an OpenID Connect provider or through GitHub with ABOM's
// own App, and holds who they are and the ABOM role they hold, server-side, for the session.
package access

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
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

// defaultSessionTTL is how long a session lasts without signing in again.
const defaultSessionTTL = 8 * time.Hour

const loginTTL = 10 * time.Minute

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
	Role    string    `json:"role"`
	Claimed []string  `json:"claimed"`
	Expires time.Time `json:"expires"`
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
}

type held struct {
	user User
	// roleAt is when the role was last read.
	roleAt time.Time
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

	mu       sync.Mutex
	logins   map[string]pending
	sessions map[string]held
}

func authenticate(p provider, redirectURL string, ttl, refresh time.Duration, log io.Writer) (*Authenticator, error) {
	redirect, err := url.Parse(redirectURL)
	if err != nil || redirect.Host == "" {
		return nil, fmt.Errorf("access: %q is not an absolute redirect URL", redirectURL)
	}
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &Authenticator{provider: p, redirect: redirectURL, secure: redirect.Scheme == "https", ttl: ttl,
		refresh: refresh, log: log, now: time.Now, logins: map[string]pending{}, sessions: map[string]held{}}, nil
}

// oidcProvider signs people in through an OpenID Connect issuer, whose ID token carries their roles.
type oidcProvider struct {
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
	p := &oidcProvider{
		oauth: oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: cfg.RedirectURL,
			Endpoint: discovered.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "profile", "email"}},
		verifier:   discovered.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		rolesClaim: cmp.Or(cfg.RolesClaim, "roles"),
		roleMap:    cfg.RoleMap,
	}
	if p.roleMap == nil {
		p.roleMap = defaultRoleMap
	}
	return authenticate(p, cfg.RedirectURL, cfg.SessionTTL, 0, cfg.Log)
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
	for k, old := range a.logins {
		if a.now().After(old.expires) {
			delete(a.logins, k)
		}
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
	u.Expires = a.now().Add(a.ttl)
	id := random()
	a.mu.Lock()
	a.sessions[id] = held{user: u, roleAt: a.now()}
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: id, Path: "/", MaxAge: int(a.ttl.Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode})
	// A page that navigates on, rather than a redirect, so the Strict cookie goes with the next
	// request: a redirect would still count as the provider's cross-site navigation.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	target := html.EscapeString(p.returnTo)
	fmt.Fprintf(w, `<!doctype html><meta http-equiv="refresh" content="0;url=%s"><a href="%s">Continue to ABOM</a>`, target, target)
}

// refuse sends the browser back to the interface, which says why a sign-in failed from its kind
// alone: not-started, mismatch, refused or failed. The reason, in the provider's or the library's
// words, goes to the log, where whoever runs ABOM can read it and a crafted callback cannot write to
// the page.
func (a *Authenticator) refuse(w http.ResponseWriter, r *http.Request, kind, reason string) {
	if a.log != nil {
		fmt.Fprintf(a.log, "access: a sign-in was refused (%s): %s\n", kind, reason)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/?signin="+kind, http.StatusFound)
}

func (o *oidcProvider) user(t *oidc.IDToken) (User, error) {
	var claims map[string]any
	if err := t.Claims(&claims); err != nil {
		return User{}, fmt.Errorf("the provider's claims cannot be read: %w", err)
	}
	text := func(name string) string { s, _ := claims[name].(string); return s }
	u := User{ID: t.Issuer + "#" + t.Subject, Name: text("name"), Username: cmp.Or(text("preferred_username"), text("email")),
		Claimed: []string{}}
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
		if role := o.roleMap[claimed]; authz.Rank(role) > authz.Rank(u.Role) {
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
	a.mu.Lock()
	s, ok := a.sessions[cookie.Value]
	if ok && a.now().After(s.user.Expires) {
		delete(a.sessions, cookie.Value)
		ok = false
	}
	a.mu.Unlock()
	if !ok || a.refresh <= 0 || !changes(r) || a.now().Sub(s.roleAt) <= a.refresh {
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
	a.mu.Lock()
	if _, still := a.sessions[cookie.Value]; still {
		a.sessions[cookie.Value] = s
	}
	a.mu.Unlock()
	return s.user, true
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
		a.mu.Lock()
		delete(a.sessions, cookie.Value)
		a.mu.Unlock()
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
