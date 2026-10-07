package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	keyOnce  sync.Once
	keyCache [2]*rsa.PrivateKey
)

// testKeys are generated once: a 2048-bit key, which is what GitHub issues, costs a second each.
func testKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) {
	t.Helper()
	keyOnce.Do(func() {
		for i := range keyCache {
			k, err := rsa.GenerateKey(rand.Reader, 2048)
			if err != nil {
				panic(err)
			}
			keyCache[i] = k
		}
	})
	return keyCache[0], keyCache[1]
}

func pemOf(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fakeInstallation struct {
	ID        int64
	Login     string
	Type      string
	Selection string
	Repos     []string
	Suspended bool
}

// fakeGitHub answers the App endpoints ABOM calls, checking each JWT the way GitHub does.
type fakeGitHub struct {
	t     *testing.T
	srv   *httptest.Server
	key   *rsa.PrivateKey
	appID int64
	now   func() time.Time

	mu            sync.Mutex
	installations []fakeInstallation
	calls         map[string]int
	tokens        map[string]int64
	minted        int
	listStatus    int
	mintStatus    map[int64]int
	reposStatus   map[int64]int
	// echo makes every failure repeat the request's Authorization header, as a careless server might.
	echo       bool
	conversion map[string]any
}

const testAppID = 4242
const testClientID = "Iv23liTESTCLIENT"

func newFakeGitHub(t *testing.T, key *rsa.PrivateKey, now func() time.Time, inst ...fakeInstallation) *fakeGitHub {
	f := &fakeGitHub{t: t, key: key, appID: testAppID, now: now, installations: inst,
		calls: map[string]int{}, tokens: map[string]int64{},
		mintStatus: map[int64]int{}, reposStatus: map[int64]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeGitHub) install(i fakeInstallation) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installations = append(f.installations, i)
}

func (f *fakeGitHub) uninstall(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.installations[:0]
	for _, i := range f.installations {
		if i.ID != id {
			kept = append(kept, i)
		}
	}
	f.installations = kept
}

func (f *fakeGitHub) credentials() Credentials {
	return Credentials{AppID: f.appID, ClientID: testClientID, Owner: "has-ghas", APIURL: f.srv.URL, Key: f.key}
}

func (f *fakeGitHub) app(now func() time.Time) *App {
	return &App{Credentials: f.credentials(), HTTP: f.srv.Client(), Now: now, API: f.srv.URL}
}

func (f *fakeGitHub) fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	if f.echo {
		message += " (you sent " + r.Header.Get("Authorization") + ")"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// jwtOK verifies the bearer the way GitHub does: RS256 under the App's key, naming the App, unexpired.
func (f *fakeGitHub) jwtOK(r *http.Request) bool {
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(bearer, ".")
	if !ok || len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&f.key.PublicKey, crypto.SHA256, sum[:], sig) != nil {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	if json.Unmarshal(raw, &claims) != nil {
		return false
	}
	now := f.now().Unix()
	return (claims.Iss == testClientID || claims.Iss == strconv.Itoa(int(f.appID))) &&
		claims.Exp > now && claims.Exp-now <= 600 && claims.Iat <= now
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.Method+" "+r.URL.Path]++
	w.Header().Set("Content-Type", "application/json")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/{code}/conversions", f.convert)
	mux.HandleFunc("GET /app", f.asApp(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": f.appID, "slug": "abom-test"})
	}))
	mux.HandleFunc("GET /app/installations", f.asApp(f.listInstallations))
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", f.asApp(f.mint))
	mux.HandleFunc("GET /installation/repositories", f.repositories)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { f.fail(w, r, http.StatusNotFound, "Not Found") })
	mux.ServeHTTP(w, r)
}

func (f *fakeGitHub) asApp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !f.jwtOK(r) {
			f.fail(w, r, http.StatusUnauthorized, "A JSON web token could not be decoded")
			return
		}
		next(w, r)
	}
}

func (f *fakeGitHub) convert(w http.ResponseWriter, r *http.Request) {
	if f.conversion == nil {
		f.fail(w, r, http.StatusNotFound, "Not Found")
		return
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(f.conversion)
}

func (f *fakeGitHub) listInstallations(w http.ResponseWriter, r *http.Request) {
	if f.listStatus != 0 {
		f.fail(w, r, f.listStatus, "listing refused")
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	out := []map[string]any{}
	for i, inst := range f.installations {
		if per > 0 && page > 0 && (i < (page-1)*per || i >= page*per) {
			continue
		}
		item := map[string]any{"id": inst.ID, "repository_selection": inst.Selection,
			"account": map[string]any{"login": inst.Login, "type": inst.Type}}
		if inst.Suspended {
			item["suspended_at"] = "2026-10-04T00:00:00Z"
		}
		out = append(out, item)
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (f *fakeGitHub) mint(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if status := f.mintStatus[id]; status != 0 {
		f.fail(w, r, status, "minting refused")
		return
	}
	if !slices.ContainsFunc(f.installations, func(i fakeInstallation) bool { return i.ID == id }) {
		f.fail(w, r, http.StatusNotFound, "Not Found")
		return
	}
	f.minted++
	token := fmt.Sprintf("ghs_test_%d_%d", id, f.minted)
	f.tokens[token] = id
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"token": token,
		"expires_at": f.now().Add(time.Hour).Format(time.RFC3339)})
}

func (f *fakeGitHub) repositories(w http.ResponseWriter, r *http.Request) {
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	id, ok := f.tokens[bearer]
	if !ok {
		f.fail(w, r, http.StatusUnauthorized, "Bad credentials")
		return
	}
	if status := f.reposStatus[id]; status != 0 {
		f.fail(w, r, status, "repositories refused")
		return
	}
	repos := []map[string]any{}
	for _, inst := range f.installations {
		if inst.ID == id {
			for _, name := range inst.Repos {
				repos = append(repos, map[string]any{"name": name, "full_name": inst.Login + "/" + name})
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(repos), "repositories": repos})
}
