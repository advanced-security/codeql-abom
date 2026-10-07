package ghapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// No token, JWT, key or manifest code reaches an error or response, even when GitHub echoes the credential.
func TestNoTokenOrKeyAppearsInAnyErrorOrLog(t *testing.T) {
	key, other := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "selected", Repos: []string{"a"}},
		fakeInstallation{ID: 12, Login: "advanced-security", Type: "Organization", Selection: "selected"})
	gh.echo = true
	ctx := context.Background()
	jwt, _ := signJWT(key, testClientID, c.Now())
	wrongJWT, _ := signJWT(other, testClientID, c.Now())
	const code = "c0de-0123456789abcdef"

	var said []string
	note := func(err error) {
		if err != nil {
			said = append(said, err.Error())
		}
	}

	app := gh.app(c.Now)
	gh.mintStatus[12] = http.StatusInternalServerError
	_, err := app.AccountToken(ctx, "advanced-security")
	note(err)
	tok, err := app.AccountToken(ctx, "has-ghas")
	note(err)
	gh.reposStatus[11] = http.StatusForbidden
	sync := app.ReadInstallations(ctx)
	for _, inst := range sync.Installations {
		said = append(said, inst.RepositoriesUnread)
	}
	gh.listStatus = http.StatusBadGateway
	said = append(said, app.ReadInstallations(ctx).Unread)
	_, err = gh.app(c.Now).AccountToken(ctx, "has-ghas")
	note(err)
	wrong := gh.app(c.Now)
	wrong.Credentials.Key = other
	note(wrong.Verify(ctx))

	_, err = Convert(ctx, gh.srv.Client(), gh.srv.URL, code)
	note(err)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, err = Convert(ctx, closed.Client(), closed.URL, code)
	note(err)

	setup := &Setup{Owner: "has-ghas", Dir: filepath.Join(t.TempDir(), "app"), APIURL: gh.srv.URL,
		HTTP: gh.srv.Client(), RedirectURL: "http://127.0.0.1:1/callback"}
	state, _ := setup.issue()
	rec := httptest.NewRecorder()
	setup.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"http://127.0.0.1:1/callback?code="+code+"&state="+state, nil))
	said = append(said, rec.Body.String())

	dir := filepath.Join(t.TempDir(), "github-app")
	broken := strings.Replace(pemOf(key), "\n", "\nAAAA", 3)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, appFile), []byte(`{"appId":4242}`), 0o600)
	_ = os.WriteFile(KeyFile(dir), []byte(broken), 0o600)
	_, err = Load(dir)
	note(err)
	_ = os.WriteFile(KeyFile(dir), []byte(pemOf(key)), 0o644)
	_, err = Load(dir)
	note(err)

	if len(said) < 10 || tok == "" {
		t.Fatalf("only %d failures were produced (token %q); the scenarios did not run", len(said), tok)
	}
	secrets := map[string]string{"the App JWT": jwt, "another key's JWT": wrongJWT, "the manifest code": code,
		"part of the App JWT's signature": strings.Split(jwt, ".")[2][:32]}
	for minted := range gh.tokens {
		secrets["installation token "+minted] = minted
	}
	for i, line := range strings.Split(pemOf(key), "\n") {
		if len(line) >= 40 {
			secrets["private key line "+string(rune('0'+i%10))] = line
		}
	}
	for _, text := range said {
		for what, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s appears in %q", what, text)
			}
		}
		if strings.Contains(text, "BEGIN RSA PRIVATE KEY") {
			t.Errorf("a private key appears in %q", text)
		}
	}
	if !strings.Contains(strings.Join(said, "\n"), "[redacted]") {
		t.Error("GitHub's echo never reached an error, so the scrub was never exercised")
	}
}
