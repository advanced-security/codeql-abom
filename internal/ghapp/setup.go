package ghapp

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Setup serves the manifest flow on loopback; the operator's click on GitHub's page is what creates the App.
type Setup struct {
	Owner      string
	OwnerKind  string
	Name       string
	Homepage   string
	WebhookURL string
	WebURL     string
	APIURL     string
	Dir        string
	// RedirectURL is where GitHub sends the operator back, set once the listener is bound.
	RedirectURL string
	HTTP        *http.Client
	Now         func() time.Time
	// Created receives the saved credentials once GitHub's code has been exchanged.
	Created chan Credentials

	mu     sync.Mutex
	states map[string]time.Time
}

// stateLifetime is GitHub's limit on completing the manifest flow.
const stateLifetime = time.Hour

const controlHeader = "X-Abom-Control"

func (s *Setup) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler serves the flow, refusing any request not addressed to this machine by a loopback name.
func (s *Setup) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("POST /manifest", s.manifest)
	mux.HandleFunc("GET /callback", s.callback)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		// The callback's URL carries the code, and its page links to GitHub.
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !isLoopback(r.Host) {
			http.Error(w, "this setup answers only on a loopback address", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Setup) manifest(w http.ResponseWriter, r *http.Request) {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		http.Error(w, "a cross-site request cannot ask for the manifest", http.StatusForbidden)
		return
	}
	if r.Header.Get(controlHeader) == "" {
		http.Error(w, "missing "+controlHeader, http.StatusForbidden)
		return
	}
	if Configured(s.Dir) {
		http.Error(w, fmt.Sprintf("an App is already configured in %s; move it aside to create another", s.Dir),
			http.StatusConflict)
		return
	}
	state, err := s.issue()
	if err != nil {
		http.Error(w, "drawing a state: "+err.Error(), http.StatusInternalServerError)
		return
	}
	manifest, err := json.Marshal(NewManifest(s.Name, s.Homepage, s.WebhookURL, s.RedirectURL))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var events []string
	for _, e := range Events {
		events = append(events, e.Event)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"action": NewAppURL(s.WebURL, s.Owner, s.OwnerKind, state), "manifest": string(manifest),
		"owner": s.Owner, "webhookUrl": s.WebhookURL,
		"permissions": requested(), "events": events,
	})
}

func (s *Setup) issue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := hex.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.states == nil {
		s.states = map[string]time.Time{}
	}
	now := s.now()
	maps.DeleteFunc(s.states, func(_ string, until time.Time) bool { return !now.Before(until) })
	s.states[state] = now.Add(stateLifetime)
	return state, nil
}

// spend reports whether state was issued here and is unexpired, and makes sure it never is again.
func (s *Setup) spend(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.states[state]
	delete(s.states, state)
	return ok && state != "" && s.now().Before(until)
}

func (s *Setup) callback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if !s.spend(query.Get("state")) {
		http.Error(w, "this redirect carries no state this setup issued, or one already used; "+
			"start again from the setup page", http.StatusForbidden)
		return
	}
	code := query.Get("code")
	if code == "" {
		http.Error(w, "GitHub redirected back without a code", http.StatusBadRequest)
		return
	}
	conversion, err := Convert(r.Context(), s.HTTP, s.APIURL, code)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	creds, err := Save(s.Dir, conversion, s.WebhookURL, s.APIURL)
	if err != nil {
		http.Error(w, "GitHub created App "+fmt.Sprint(conversion.ID)+" but saving it failed: "+err.Error()+
			". Generate a new private key in its settings, or delete it there and start again.",
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = createdPage.Execute(w, map[string]any{
		"Slug": creds.Slug, "AppID": creds.AppID, "Dir": s.Dir, "Mismatch": granted(conversion),
		"Install": strings.TrimSuffix(creds.HTMLURL, "/") + "/installations/new",
	})
	select {
	case s.Created <- creds:
	default:
	}
}

// granted names any difference between what GitHub created and what the table requested.
func granted(c Conversion) string {
	want := NewManifest("", "", "", "")
	var diff []string
	for _, p := range slices.Sorted(maps.Keys(want.DefaultPermissions)) {
		if c.Permissions[p] != want.DefaultPermissions[p] {
			diff = append(diff, fmt.Sprintf("%s is %q, requested %q", p, c.Permissions[p], want.DefaultPermissions[p]))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(c.Permissions)) {
		if _, ok := want.DefaultPermissions[p]; !ok {
			diff = append(diff, fmt.Sprintf("%s is %q, never requested", p, c.Permissions[p]))
		}
	}
	if got, w := slices.Sorted(slices.Values(c.Events)), slices.Sorted(slices.Values(want.DefaultEvents)); !slices.Equal(got, w) {
		diff = append(diff, fmt.Sprintf("events are %v, requested %v", got, w))
	}
	return strings.Join(diff, "; ")
}

// isLoopback checks the Host header, which a rebinding attack controls, rather than the socket.
func isLoopback(host string) bool {
	name := host
	if h, _, err := net.SplitHostPort(host); err == nil {
		name = h
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

var createdPage = template.Must(template.New("created").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Created {{.Slug}}</title></head>
<body style="font-family:system-ui,sans-serif;max-width:52rem;margin:2rem auto;padding:0 1rem">
<h1>Created {{.Slug}}</h1>
<p>App {{.AppID}} is saved in {{.Dir}}. Its private key and webhook secret are readable by this account only.</p>
{{if .Mismatch}}<p><strong>GitHub granted something other than what was requested:</strong> {{.Mismatch}}</p>{{end}}
<p>Next, install it and choose only the repositories ABOM should read: <a href="{{.Install}}">{{.Install}}</a></p>
<p>You can close this tab. The command that served it has finished.</p>
</body></html>
`))

// setupPage renders the guarded endpoint's JSON through textContent, so nothing becomes markup.
const setupPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Create the ABOM GitHub App</title>
<style>
body{font-family:system-ui,sans-serif;max-width:56rem;margin:2rem auto;padding:0 1rem;color:#1f2328}
table{border-collapse:collapse;width:100%}td,th{border:1px solid #d0d7de;padding:.4rem;text-align:left;vertical-align:top}
button{font-size:1rem;padding:.5rem 1rem}#problem{color:#cf222e}
</style></head>
<body>
<h1>Create the ABOM GitHub App</h1>
<p id="intro">Reading the manifest…</p>
<table><thead><tr><th>Permission</th><th>Access</th><th>Needed by</th></tr></thead><tbody id="permissions"></tbody></table>
<p id="events"></p>
<p>Nothing is created until you click <strong>Create GitHub App</strong> on GitHub's page. That click is your approval.</p>
<button id="go" disabled>Continue to GitHub</button>
<p id="problem" role="alert"></p>
<script>
(async () => {
  const problem = document.getElementById("problem");
  const res = await fetch("/manifest", {method: "POST", headers: {"X-Abom-Control": "1"}});
  if (!res.ok) { problem.textContent = await res.text(); return; }
  const m = await res.json();
  document.getElementById("intro").textContent = "The App is created under " + m.owner +
    ", installable only on " + m.owner +
    ". Deliveries go to " + m.webhookUrl + ". It requests these permissions and nothing else:";
  const rows = document.getElementById("permissions");
  for (const p of m.permissions) {
    const row = rows.insertRow();
    for (const v of [p.permission, p.access, p.why]) row.insertCell().textContent = v;
  }
  document.getElementById("events").textContent = "Events: " + m.events.join(", ");
  const go = document.getElementById("go");
  go.disabled = false;
  go.onclick = () => {
    const form = document.createElement("form");
    form.method = "post";
    form.action = m.action;
    const input = document.createElement("input");
    input.type = "hidden";
    input.name = "manifest";
    input.value = m.manifest;
    form.appendChild(input);
    document.body.appendChild(form);
    form.submit();
  };
})();
</script>
</body></html>
`

func (s *Setup) page(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(setupPage))
}
