// Package oidctest is an OpenID provider in miniature, for tests and for trying sign-in on a local
// cluster: discovery, keys, an authorization page that signs in whichever configured person is
// chosen, and a token endpoint that checks the PKCE verifier against the challenge. It keeps its key
// in memory and calls nothing; the production image does not build it.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Person is someone who can sign in, by the subject they are configured under.
type Person struct {
	Name  string
	Roles []string
	// Claims are added to their ID token, over those derived from the rest.
	Claims map[string]any
}

// Issuer answers as an OpenID provider at URL, for one client.
type Issuer struct {
	URL      string
	ClientID string
	Key      *rsa.PrivateKey
	People   map[string]Person
	// Audience and Nonce, when set, replace what every ID token carries, as a provider answering
	// another client or another sign-in would.
	Audience, Nonce string

	mu    sync.Mutex
	codes map[string]grant
}

type grant struct{ person, challenge, nonce string }

// New is an issuer with a key generated for it, which lasts as long as the process.
func New(issuerURL, clientID string, people map[string]Person) *Issuer {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("oidctest: generating a key: %v", err))
	}
	return &Issuer{URL: issuerURL, ClientID: clientID, Key: key, People: people, codes: map[string]grant{}}
}

func (i *Issuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/.well-known/openid-configuration":
		writeJSON(w, map[string]any{"issuer": i.URL, "authorization_endpoint": i.URL + "/authorize",
			"token_endpoint": i.URL + "/token", "jwks_uri": i.URL + "/keys",
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	case r.Method == http.MethodGet && r.URL.Path == "/keys":
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &i.Key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	case r.URL.Path == "/authorize":
		i.authorize(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/token":
		i.token(w, r)
	default:
		http.NotFound(w, r)
	}
}

var choosePage = template.Must(template.New("choose").Parse(`<!doctype html><title>Sign in</title>
<form method="post" action="/authorize">{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{index $v 0}}">{{end}}
{{range .People}}<button name="person" value="{{.}}" data-person="{{.}}">{{.}}</button> {{end}}</form>`))

// authorize signs in the one person there is, or the one chosen from a page listing them, and sends
// the browser back with a code bound to the challenge and nonce it brought.
func (i *Issuer) authorize(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirect, err := url.Parse(r.Form.Get("redirect_uri"))
	if err != nil || !redirect.IsAbs() || r.Form.Get("client_id") != i.ClientID ||
		r.Form.Get("code_challenge_method") != "S256" || r.Form.Get("code_challenge") == "" {
		http.Error(w, "invalid_request: a client, an absolute redirect and an S256 challenge are required", http.StatusBadRequest)
		return
	}
	person := r.Form.Get("person")
	if person == "" && len(i.People) == 1 {
		person = slices.Collect(maps.Keys(i.People))[0]
	}
	if _, ok := i.People[person]; !ok {
		params := url.Values{}
		for _, k := range []string{"client_id", "redirect_uri", "state", "nonce", "code_challenge", "code_challenge_method"} {
			params[k] = []string{r.Form.Get(k)}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = choosePage.Execute(w, map[string]any{"Params": params, "People": slices.Sorted(maps.Keys(i.People))})
		return
	}
	code := random()
	i.mu.Lock()
	i.codes[code] = grant{person: person, challenge: r.Form.Get("code_challenge"), nonce: r.Form.Get("nonce")}
	i.mu.Unlock()
	q := redirect.Query()
	q.Set("code", code)
	q.Set("state", r.Form.Get("state"))
	redirect.RawQuery = q.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// token exchanges a code once, and only with the verifier whose challenge it was issued for.
func (i *Issuer) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	i.mu.Lock()
	g, ok := i.codes[r.Form.Get("code")]
	delete(i.codes, r.Form.Get("code"))
	i.mu.Unlock()
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	idToken, err := i.sign(g.person, g.nonce)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"access_token": random(), "token_type": "Bearer", "expires_in": 3600, "id_token": idToken})
}

func (i *Issuer) sign(subject, nonce string) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: i.Key}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
	if err != nil {
		return "", err
	}
	p := i.People[subject]
	now := time.Now()
	claims := map[string]any{"iss": i.URL, "aud": i.ClientID, "sub": subject, "nonce": nonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "name": p.Name,
		"preferred_username": subject + "@example.test", "roles": p.Roles}
	if i.Audience != "" {
		claims["aud"] = i.Audience
	}
	if i.Nonce != "" {
		claims["nonce"] = i.Nonce
	}
	maps.Copy(claims, p.Claims)
	return jwt.Signed(signer).Claims(claims).Serialize()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func random() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
