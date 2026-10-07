package catalog

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// installedTokens is an App installed on the owners it names, lending the designated token to everyone else.
type installedTokens struct {
	own  map[string]string
	lent string
}

func (s installedTokens) Token(_ context.Context, owner string) (string, bool, error) {
	if token, ok := s.own[strings.ToLower(owner)]; ok {
		return token, false, nil
	}
	return s.lent, true, nil
}

func (s installedTokens) AccountToken(_ context.Context, owner string) (string, error) {
	if token, ok := s.own[strings.ToLower(owner)]; ok {
		return token, nil
	}
	return "", ghapp.ErrNotInstalled
}

// githubSeeing answers trees: public ones to any bearer, private ones only to their owner's installation.
func githubSeeing(t *testing.T, private map[string]string) (*httptest.Server, func() map[string]string) {
	var mu sync.Mutex
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		for repo, bearer := range private {
			if strings.HasPrefix(r.URL.Path, "/repos/"+repo+"/") && r.Header.Get("Authorization") != "Bearer "+bearer {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
		}
		if strings.Contains(r.URL.Path, "/git/trees/") {
			_, _ = w.Write([]byte(`{"tree":[{"path":"action.yml","mode":"100644","type":"blob","sha":"abc"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"errors":[{"message":"stop here"}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string]string {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]string{}
		for k, v := range seen {
			out[k] = v
		}
		return out
	}
}

// Each read carries its account's own token, a lent one for a repository of an uninstalled owner, or none for its account.
func TestEachReadCarriesTheTokenOfTheAccountItConcerns(t *testing.T) {
	srv, seen := githubSeeing(t, map[string]string{"has-ghas/widgets": "own-has-ghas", "acme/private": "own-acme"})
	client := New(srv.URL, "person-token", nil)
	client.Tokens = installedTokens{own: map[string]string{"has-ghas": "own-has-ghas"}, lent: "lent-designated"}
	ctx := context.Background()

	if _, err := client.Tree(ctx, "has-ghas/widgets", "abc"); err != nil {
		t.Errorf("an installed owner's private tree: %v", err)
	}
	if _, err := client.Tree(ctx, "actions/checkout", "abc"); err != nil {
		t.Errorf("a public tree of an owner without an installation: %v", err)
	}
	_, err := client.Tree(ctx, "acme/private", "abc")
	if !errors.Is(err, ghapp.ErrNotInstalled) || !errors.Is(err, ErrNoDatabase) || !strings.Contains(err.Error(), "acme") {
		t.Errorf("a private repository whose owner has no installation = %v; want ErrNotInstalled naming acme, still a 404", err)
	}
	if _, err := client.PropertySchema(ctx, "actions"); !errors.Is(err, ghapp.ErrNotInstalled) {
		t.Errorf("an account read without its own installation = %v; want ErrNotInstalled", err)
	}
	if _, err := client.ListAll(ctx, "actions"); !errors.Is(err, ghapp.ErrNotInstalled) {
		t.Errorf("listing an account without its own installation = %v; want ErrNotInstalled", err)
	}
	_, _ = client.ListAll(ctx, "has-ghas")

	got := seen()
	for path, want := range map[string]string{
		"/repos/has-ghas/widgets/git/trees/abc": "Bearer own-has-ghas",
		"/repos/actions/checkout/git/trees/abc": "Bearer lent-designated",
		"/graphql":                              "Bearer own-has-ghas",
	} {
		if got[path] != want {
			t.Errorf("%s carried %q, want %q", path, got[path], want)
		}
	}
	for path, bearer := range got {
		if strings.Contains(path, "/actions") && !strings.HasPrefix(path, "/repos/") {
			t.Errorf("%s was requested; an account without its own installation must not be read as one", path)
		}
		if strings.Contains(bearer, "person-token") {
			t.Errorf("%s carried the person's token while an App is configured", path)
		}
	}

	plain := New(srv.URL, "person-token", nil)
	_, _ = plain.Tree(ctx, "actions/checkout", "abc")
	_, _ = plain.PropertySchema(ctx, "has-ghas")
	got = seen()
	if got["/repos/actions/checkout/git/trees/abc"] != "Bearer person-token" ||
		got["/orgs/has-ghas/properties/schema"] != "Bearer person-token" {
		t.Errorf("without an App the person's token must be sent exactly as before: %v", got)
	}
}

// appGitHub is GitHub for an App on has-ghas (11) and advanced-security (12), with a private repository on acme.
func appGitHub(t *testing.T) (*httptest.Server, func() map[string]int64) {
	var mu sync.Mutex
	minted := map[string]int64{}
	servedBy := map[string]int64{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"id":12,"account":{"login":"advanced-security","type":"Organization"},"repository_selection":"selected"},
			{"id":11,"account":{"login":"has-ghas","type":"Organization"},"repository_selection":"all"}]`))
	})
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		token := "ghs_installation_" + r.PathValue("id")
		minted[token] = map[string]int64{"11": 11, "12": 12}[r.PathValue("id")]
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"token":"` + token + `","expires_at":"2099-01-01T00:00:00Z"}`))
	})
	mux.HandleFunc("GET /repos/{owner}/{repo}/git/trees/{sha}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		id, ok := minted[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		if !ok || (r.PathValue("owner") == "acme" && id != 99) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		servedBy[r.PathValue("owner")+"/"+r.PathValue("repo")] = id
		_, _ = w.Write([]byte(`{"tree":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() map[string]int64 {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int64{}
		for k, v := range servedBy {
			out[k] = v
		}
		return out
	}
}

// A real App drives the reader end to end: its own installation where it has one, the earliest lent elsewhere.
func TestARealAppReadsEachRepositoryThroughTheRightInstallation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv, servedBy := appGitHub(t)
	client := New(srv.URL, "", nil)
	client.Tokens = &ghapp.App{Credentials: ghapp.Credentials{AppID: 4242, Key: key}, API: srv.URL}
	ctx := context.Background()
	for _, repository := range []string{"advanced-security/codeql-abom-test1", "has-ghas/widgets", "actions/checkout"} {
		if _, err := client.Tree(ctx, repository, "abc"); err != nil {
			t.Errorf("Tree(%s): %v", repository, err)
		}
	}
	if _, err := client.Tree(ctx, "acme/private", "abc"); !errors.Is(err, ghapp.ErrNotInstalled) {
		t.Errorf("a private repository on an owner without an installation = %v; want ErrNotInstalled", err)
	}
	want := map[string]int64{"advanced-security/codeql-abom-test1": 12, "has-ghas/widgets": 11, "actions/checkout": 11}
	if got := servedBy(); len(got) != len(want) || got["actions/checkout"] != 11 ||
		got["advanced-security/codeql-abom-test1"] != 12 || got["has-ghas/widgets"] != 11 {
		t.Errorf("served by installations %v, want %v", got, want)
	}
}
