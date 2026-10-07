package ghapp_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

const (
	manifestCode  = "c0de-from-github-0123456789"
	webhookSecret = "whsec-0123456789abcdef"
	clientSecret  = "client-secret-never-kept-0123"
)

func guarded(h http.Handler, method, target string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:5555"+target, strings.NewReader("{}"))
	req.Header.Set("X-Abom-Control", "1")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func browse(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:5555"+target, nil))
	return rec
}

type manifestAnswer struct {
	Action   string `json:"action"`
	Manifest string `json:"manifest"`
}

func issueState(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := guarded(h, http.MethodPost, "/manifest", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /manifest = %d: %s", rec.Code, rec.Body.String())
	}
	var got manifestAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body.String(), err)
	}
	u, err := url.Parse(got.Action)
	if err != nil {
		t.Fatalf("action %q: %v", got.Action, err)
	}
	return u.Query().Get("state")
}

// fakeConversion answers POST /app-manifests/{code}/conversions as GitHub does, once per code.
func fakeConversion(t *testing.T, calls *atomic.Int32) *httptest.Server {
	key, _ := ghapp.KeysForTest(t)
	m := ghapp.NewManifest("x", "https://example.com", "https://smee.io/x", "http://127.0.0.1/callback")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/"+manifestCode+"/conversions" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 4242, "slug": "abom-has-ghas", "client_id": "Iv23liTESTCLIENT", "client_secret": clientSecret,
			"webhook_secret": webhookSecret, "pem": ghapp.PEMForTest(key),
			"html_url":    "https://github.com/apps/abom-has-ghas",
			"owner":       map[string]string{"login": "has-ghas", "type": "Organization"},
			"permissions": m.DefaultPermissions, "events": m.DefaultEvents,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newSetup(dir, apiURL string, hc *http.Client) *ghapp.Setup {
	return &ghapp.Setup{
		Owner: "has-ghas", OwnerKind: "organization", Name: "abom-has-ghas",
		Homepage: "https://github.com/advanced-security/codeql-abom", WebhookURL: "https://smee.io/abc",
		WebURL: "https://github.com", APIURL: apiURL, HTTP: hc, Dir: dir,
		RedirectURL: "http://127.0.0.1:5555/callback", Created: make(chan ghapp.Credentials, 1),
	}
}

// GitHub documents no default for public, so the manifest states private outright.
func TestTheAppIsCreatedInstallableOnlyOnItsOwner(t *testing.T) {
	rec := guarded(newSetup(filepath.Join(t.TempDir(), "github-app"), "", nil).Handler(), http.MethodPost, "/manifest", nil)
	var got manifestAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("POST /manifest = %d %s", rec.Code, rec.Body.String())
	}
	var wire map[string]any
	if err := json.Unmarshal([]byte(got.Manifest), &wire); err != nil || wire["public"] != false {
		t.Errorf("manifest public = %v, want false sent to GitHub, not left to its default", wire["public"])
	}
}

// The manifest and its state go only to a loopback, same-origin request with the control header.
func TestTheManifestEndpointIsControlGuarded(t *testing.T) {
	h := newSetup(filepath.Join(t.TempDir(), "github-app"), "", nil).Handler()

	for name, mutate := range map[string]func(*http.Request){
		"without the control header": func(r *http.Request) { r.Header.Del("X-Abom-Control") },
		"from another site":          func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") },
		"under a rebound name":       func(r *http.Request) { r.Host = "evil.example:5555" },
	} {
		if rec := guarded(h, http.MethodPost, "/manifest", mutate); rec.Code != http.StatusForbidden {
			t.Errorf("a request %s got %d, want 403", name, rec.Code)
		}
	}

	rec := guarded(h, http.MethodPost, "/manifest", nil)
	var got manifestAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &got); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("POST /manifest = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.HasPrefix(got.Action, "https://github.com/organizations/has-ghas/settings/apps/new?state=") {
		t.Errorf("action %q is not the organization's new-App page", got.Action)
	}
	var m ghapp.Manifest
	if err := json.Unmarshal([]byte(got.Manifest), &m); err != nil {
		t.Fatalf("manifest %q: %v", got.Manifest, err)
	}
	want := ghapp.NewManifest("", "", "", "").DefaultPermissions
	if len(m.DefaultPermissions) != len(want) || m.RedirectURL != "http://127.0.0.1:5555/callback" ||
		m.HookAttributes.URL != "https://smee.io/abc" {
		t.Errorf("manifest = %+v, want the table's permissions, this redirect and this hook", m)
	}
	if first, second := issueState(t, h), issueState(t, h); len(first) < 32 || first == second {
		t.Errorf("states %q and %q: want each unguessable and distinct", first, second)
	}

	page := browse(h, "/")
	if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") ||
		!strings.Contains(page.Body.String(), "X-Abom-Control") {
		t.Errorf("GET / = %d %q, want the page that asks for the manifest with the control header",
			page.Code, page.Header().Get("Content-Type"))
	}
}

// The converted key lands owner-only beside the database, not in it, and the secret where the receiver accepts it.
func TestTheConversionStoresTheKeyOwnerOnlyAndTheSecretWhereTheReceiverReadsIt(t *testing.T) {
	var calls atomic.Int32
	gh := fakeConversion(t, &calls)
	root := t.TempDir()
	dbPath := filepath.Join(root, "db", "abom.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(dbPath, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	dir := ghapp.DefaultDir(dbPath)
	setup := newSetup(dir, gh.URL, gh.Client())
	h := setup.Handler()

	rec := browse(h, "/callback?code="+manifestCode+"&state="+issueState(t, h))
	if rec.Code != http.StatusOK {
		t.Fatalf("callback = %d: %s", rec.Code, rec.Body.String())
	}
	key, _ := ghapp.KeysForTest(t)
	pemText := ghapp.PEMForTest(key)
	for _, secret := range []string{manifestCode, webhookSecret, clientSecret, "PRIVATE KEY"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the callback's page shows %q", secret)
		}
	}
	select {
	case creds := <-setup.Created:
		if creds.AppID != 4242 {
			t.Errorf("created %+v", creds)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the setup never reported the App it created")
	}

	for path, want := range map[string]os.FileMode{dir: 0o700, ghapp.KeyFile(dir): 0o600,
		ghapp.WebhookSecretFile(dir): 0o600} {
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %o", path, info.Mode().Perm(), err, want)
		}
	}
	keyBody := strings.Split(pemText, "\n")[5]
	var holding []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := os.ReadFile(path)
		if bytes.Contains(data, []byte(keyBody)) {
			holding = append(holding, path)
		}
		if bytes.Contains(data, []byte(clientSecret)) {
			t.Errorf("%s holds the client secret, which nothing reads", path)
		}
		return nil
	})
	if len(holding) != 1 || holding[0] != ghapp.KeyFile(dir) {
		t.Errorf("the private key is in %v, want only %s", holding, ghapp.KeyFile(dir))
	}

	secret, err := ghapp.WebhookSecret(dir)
	if err != nil || string(secret) != webhookSecret {
		t.Fatalf("WebhookSecret = %q, %v", secret, err)
	}
	receiver := (&serve.Server{Store: s, Events: &serve.EventReceiver{Secret: secret}}).Handler()
	body := []byte(`{"zen":"Keep it logically awesome.","hook_id":1}`)
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(body)
	req := httptest.NewRequest(http.MethodPost, "/api/events/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "ping")
	req.Header.Set("X-GitHub-Delivery", "d-ping")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	delivered := httptest.NewRecorder()
	receiver.ServeHTTP(delivered, req)
	if delivered.Code != http.StatusAccepted {
		t.Errorf("the receiver refused a delivery signed with the App's secret: %d %s",
			delivered.Code, delivered.Body.String())
	}

	if loaded, err := ghapp.Load(dir); err != nil || !loaded.Key.Equal(key) {
		t.Errorf("Load = %v; want the converted key back", err)
	}
	if rec := guarded(h, http.MethodPost, "/manifest", nil); rec.Code != http.StatusConflict {
		t.Errorf("a second manifest for a configured directory got %d, want 409", rec.Code)
	}
}

// A forged or spent state is refused before the code is exchanged, or a forged redirect would adopt somebody else's App.
func TestACallbackWithAStateThisProcessDidNotIssueIsRefused(t *testing.T) {
	var calls atomic.Int32
	gh := fakeConversion(t, &calls)
	h := newSetup(filepath.Join(t.TempDir(), "github-app"), gh.URL, gh.Client()).Handler()

	if rec := browse(h, "/callback?code="+manifestCode+"&state=forged"); rec.Code != http.StatusForbidden {
		t.Errorf("a forged state got %d, want 403", rec.Code)
	}
	if rec := browse(h, "/callback?code="+manifestCode); rec.Code != http.StatusForbidden {
		t.Errorf("a missing state got %d, want 403", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("a refused callback exchanged the code %d times", calls.Load())
	}

	state := issueState(t, h)
	if rec := browse(h, "/callback?code="+manifestCode+"&state="+state); rec.Code != http.StatusOK {
		t.Fatalf("the issued state got %d: %s", rec.Code, rec.Body.String())
	}
	if rec := browse(h, "/callback?code="+manifestCode+"&state="+state); rec.Code != http.StatusForbidden {
		t.Errorf("a spent state got %d, want 403", rec.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("the code was exchanged %d times, want once", calls.Load())
	}
}
