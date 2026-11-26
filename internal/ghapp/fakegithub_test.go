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
	// Granted is what the installation approved; nil is everything the manifest asks for.
	Granted map[string]string
}

func (i fakeInstallation) granted() map[string]string {
	if i.Granted != nil {
		return i.Granted
	}
	all := map[string]string{}
	for _, g := range Permissions {
		all[g.Permission] = g.Access
	}
	return all
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
	// carries is the permissions GitHub gave each token: what was asked for, or everything granted.
	carries map[string]map[string]string
	// scopedTo is the repositories each token was asked for; absent is every one the installation covers.
	scopedTo    map[string][]string
	minted      int
	listStatus  int
	mintStatus  map[int64]int
	reposStatus map[int64]int
	// echo makes every failure repeat the request's Authorization header, as a careless server might.
	echo       bool
	conversion map[string]any

	// Sign-in: codes GitHub handed back for a challenge, the people user tokens belong to, the
	// tokens revoked, and who belongs to which organization ("org") and team ("org/team"), by role.
	codes   map[string]string
	people  map[string]fakePerson
	revoked []string
	members map[string]map[string]string
}

type fakePerson struct {
	ID          int64
	Login, Name string
}

const testClientSecret = "client-secret-value"

const testAppID = 4242
const testClientID = "Iv23liTESTCLIENT"

// level orders access the way GitHub does: write includes read.
var level = map[string]int{"read": 1, "write": 2, "admin": 3}

func newFakeGitHub(t *testing.T, key *rsa.PrivateKey, now func() time.Time, inst ...fakeInstallation) *fakeGitHub {
	f := &fakeGitHub{t: t, key: key, appID: testAppID, now: now, installations: inst,
		calls: map[string]int{}, tokens: map[string]int64{}, carries: map[string]map[string]string{},
		scopedTo:   map[string][]string{},
		mintStatus: map[int64]int{}, reposStatus: map[int64]int{},
		codes: map[string]string{}, people: map[string]fakePerson{}, members: map[string]map[string]string{}}
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
	mux.HandleFunc("GET /orgs/{org}/properties/schema", f.asInstallation(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{"property_name": "team", "value_type": "string",
			"values_editable_by": "org_actors"}})
	}))
	mux.HandleFunc("GET /orgs/{org}/properties/values", f.asInstallation(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "2" {
			w.Header().Set("Link", "<"+f.srv.URL+r.URL.Path+"?per_page=1&page=2>; rel=\"next\"")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"repository_full_name": r.PathValue("org") + "/one"}})
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"repository_full_name": r.PathValue("org") + "/two"}})
	}))
	mux.HandleFunc("POST /login/oauth/access_token", f.userToken)
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		p, ok := f.people[bearer]
		if !ok || slices.Contains(f.revoked, bearer) {
			f.fail(w, r, http.StatusUnauthorized, "Bad credentials")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": p.ID, "login": p.Login, "name": p.Name})
	})
	mux.HandleFunc("DELETE /applications/{client}/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		var body struct {
			Token string `json:"access_token"`
		}
		if !ok || id != testClientID || secret != testClientSecret || r.PathValue("client") != testClientID ||
			json.NewDecoder(r.Body).Decode(&body) != nil {
			f.fail(w, r, http.StatusUnauthorized, "Bad credentials")
			return
		}
		f.revoked = append(f.revoked, body.Token)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /orgs/{org}/memberships/{login}", f.asInstallation(func(w http.ResponseWriter, r *http.Request) {
		f.membership(w, r, r.PathValue("org"), map[string]string{"admin": "admin", "member": "member"})
	}))
	mux.HandleFunc("GET /orgs/{org}/teams/{team}/memberships/{login}", f.asInstallation(func(w http.ResponseWriter, r *http.Request) {
		f.membership(w, r, r.PathValue("org")+"/"+r.PathValue("team"), map[string]string{"maintainer": "maintainer", "member": "member"})
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { f.fail(w, r, http.StatusNotFound, "Not Found") })
	mux.ServeHTTP(w, r)
}

// userToken answers the App's code exchange only with the App's own secret and the verifier whose
// challenge the authorization was asked with.
func (f *fakeGitHub) userToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.Header.Get("Accept") != "application/json" ||
		r.Form.Get("client_id") != testClientID || r.Form.Get("client_secret") != testClientSecret {
		f.fail(w, r, http.StatusUnauthorized, "incorrect_client_credentials")
		return
	}
	challenge, ok := f.codes[r.Form.Get("code")]
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "bad_verification_code"})
		return
	}
	delete(f.codes, r.Form.Get("code"))
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ghu_" + r.Form.Get("code"), "token_type": "bearer"})
}

// membership answers as GitHub does: the person's role while active, and 404 for anyone else. The
// token must carry members: read.
func (f *fakeGitHub) membership(w http.ResponseWriter, r *http.Request, group string, roles map[string]string) {
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if f.carries[bearer]["members"] == "" {
		f.fail(w, r, http.StatusForbidden, "Resource not accessible by integration")
		return
	}
	role, ok := roles[f.members[group][strings.ToLower(r.PathValue("login"))]]
	if !ok {
		f.fail(w, r, http.StatusNotFound, "Not Found")
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"state": "active", "role": role})
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

// asInstallation answers only a token minted for the installation on the account the path names.
func (f *fakeGitHub) asInstallation(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, ok := f.tokens[bearer]
		if !ok {
			f.fail(w, r, http.StatusUnauthorized, "Bad credentials")
			return
		}
		for _, inst := range f.installations {
			if inst.ID == id && strings.EqualFold(inst.Login, r.PathValue("org")) {
				next(w, r)
				return
			}
		}
		f.fail(w, r, http.StatusNotFound, "Not Found")
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
			"account": map[string]any{"login": inst.Login, "type": inst.Type}, "permissions": inst.granted()}
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
	at := slices.IndexFunc(f.installations, func(i fakeInstallation) bool { return i.ID == id })
	if at < 0 {
		f.fail(w, r, http.StatusNotFound, "Not Found")
		return
	}
	granted := f.installations[at].granted()
	var body struct {
		Permissions  map[string]string `json:"permissions"`
		Repositories []string          `json:"repositories"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.fail(w, r, http.StatusBadRequest, "Problems parsing JSON")
			return
		}
	}
	carries := granted
	if body.Permissions != nil {
		// GitHub refuses a token asking for more than the installation approved, rather than trimming it.
		for name, access := range body.Permissions {
			if level[granted[name]] < level[access] {
				f.fail(w, r, http.StatusUnprocessableEntity, "The permissions requested are not granted to this installation.")
				return
			}
		}
		carries = body.Permissions
	}
	f.minted++
	token := fmt.Sprintf("ghs_test_%d_%d", id, f.minted)
	f.tokens[token] = id
	f.carries[token] = carries
	if body.Repositories != nil {
		f.scopedTo[token] = body.Repositories
	}
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
				repos = append(repos, map[string]any{"name": name, "full_name": inst.Login + "/" + name,
					"private": true, "archived": name == "archived", "fork": false,
					"owner": map[string]any{"login": inst.Login}})
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(repos), "repositories": repos})
}
