package oidctest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func noRedirects(c *http.Client) *http.Client {
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}

// With several people configured, a browser chooses who signs in, and the ID token the code is
// exchanged for, with the verifier its challenge came from, is that person's.
func TestABrowserChoosesWhoSignsInAndGetsThatPersonsToken(t *testing.T) {
	var i *Issuer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { i.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	i = New(srv.URL, "abom", map[string]Person{
		"vera": {Name: "Vera Viewer", Roles: []string{"ABOM.Viewer"}},
		"otto": {Name: "Otto Operator", Roles: []string{"ABOM.Operator"}},
	})
	client := noRedirects(srv.Client())
	verifier := "a-verifier-of-enough-length-to-be-one-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {"abom"}, "redirect_uri": {"https://abom.example/auth/callback"}, "state": {"s1"},
		"nonce": {"n1"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}

	page, err := client.Get(srv.URL + "/authorize?" + q.Encode())
	if err != nil || page.StatusCode != http.StatusOK {
		t.Fatalf("the authorization page answered %v, %v", page, err)
	}
	page.Body.Close()
	choice := url.Values{"person": {"otto"}}
	for k, v := range q {
		choice[k] = v
	}
	chosen, err := client.PostForm(srv.URL+"/authorize", choice)
	if err != nil || chosen.StatusCode != http.StatusFound {
		t.Fatalf("choosing answered %v, %v", chosen, err)
	}
	chosen.Body.Close()
	back, _ := url.Parse(chosen.Header.Get("Location"))
	if back.Host != "abom.example" || back.Query().Get("state") != "s1" {
		t.Fatalf("the browser was sent back to %s", back)
	}

	bad, err := client.PostForm(srv.URL+"/token", url.Values{"code": {back.Query().Get("code")}, "code_verifier": {"wrong"}})
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode != http.StatusBadRequest {
		t.Errorf("a code exchanged with the wrong verifier answered %d", bad.StatusCode)
	}
	chosen, err = client.PostForm(srv.URL+"/authorize", choice)
	if err != nil {
		t.Fatal(err)
	}
	chosen.Body.Close()
	back, _ = url.Parse(chosen.Header.Get("Location"))
	token, err := client.PostForm(srv.URL+"/token", url.Values{"code": {back.Query().Get("code")}, "code_verifier": {verifier}})
	if err != nil || token.StatusCode != http.StatusOK {
		t.Fatalf("the exchange answered %v, %v", token, err)
	}
	defer token.Body.Close()
	var answer struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(token.Body).Decode(&answer); err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.ParseSigned(answer.IDToken, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := parsed.Claims(&i.Key.PublicKey, &claims); err != nil {
		t.Fatalf("the ID token does not verify with the issuer's key: %v", err)
	}
	if claims["sub"] != "otto" || claims["name"] != "Otto Operator" || claims["nonce"] != "n1" || claims["iss"] != srv.URL ||
		!strings.Contains(claims["preferred_username"].(string), "otto@") {
		t.Errorf("claims = %v; want Otto's, for this sign-in", claims)
	}
}
