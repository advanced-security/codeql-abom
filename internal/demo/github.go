package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"os/exec"
	"strings"
	"time"
)

// Provider answers, for the fictional repositories alone, the reads ABOM makes of GitHub: Git's
// smart HTTP upload service and the REST reads a run and its follow-ups ask. It writes nothing,
// serves nothing it was not given, and answers only requests addressed to loopback.
type Provider struct {
	Layout   Layout
	Manifest Manifest
	// Git is the git executable whose http-backend serves fetches; empty finds it on PATH.
	Git string
}

// Handler is the provider's whole surface; anything it does not name is not part of GitHub here.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /git/{owner}/{repo}/info/refs", p.git)
	mux.HandleFunc("POST /git/{owner}/{repo}/git-upload-pack", p.git)
	mux.HandleFunc("GET /repos/{owner}/{repo}", p.repository)
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{ref}", p.commit)
	mux.HandleFunc("GET /repos/{owner}/{repo}/compare/{spec}", p.compare)
	mux.HandleFunc("GET /repos/{owner}/{repo}/code-scanning/", p.codeScanning)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			answer(w, http.StatusMethodNotAllowed, "the ABOM demo provider is read-only")
			return
		}
		answer(w, http.StatusNotFound, "Not Found: the ABOM demo provider does not simulate this")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackName(r.Host) || !loopbackPeer(r.RemoteAddr) {
			answer(w, http.StatusForbidden, "the ABOM demo provider answers only on loopback")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// Listen serves the provider on an ephemeral loopback port until ctx ends, and returns its URL.
func (p *Provider) Listen(ctx context.Context) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("listening for the demo provider: %w", err)
	}
	srv := &http.Server{Handler: p.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = ln.Close()
		}
	}()
	return "http://" + ln.Addr().String(), nil
}

func (p *Provider) named(w http.ResponseWriter, r *http.Request) (Repository, bool) {
	r2, ok := p.Manifest.Has(r.PathValue("owner") + "/" + strings.TrimSuffix(r.PathValue("repo"), ".git"))
	if !ok {
		answer(w, http.StatusNotFound, "Not Found: not a repository in the ABOM demo dataset")
	}
	return r2, ok
}

// git hands the upload service to git's own http-backend, which reads the bare repository.
func (p *Provider) git(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.PathValue("repo"), ".git") {
		answer(w, http.StatusNotFound, "Not Found")
		return
	}
	if _, ok := p.named(w, r); !ok {
		return
	}
	if strings.HasSuffix(r.URL.Path, "/info/refs") && r.URL.Query().Get("service") != "git-upload-pack" {
		answer(w, http.StatusForbidden, "the ABOM demo provider serves fetches only")
		return
	}
	git := p.Git
	if git == "" {
		var err error
		if git, err = exec.LookPath("git"); err != nil {
			answer(w, http.StatusServiceUnavailable, "git is not installed")
			return
		}
	}
	env := append(gitEnvironment(), "GIT_PROJECT_ROOT="+p.Layout.Git(), "GIT_HTTP_EXPORT_ALL=1")
	if r.Header.Get("Git-Protocol") == "version=2" {
		env = append(env, "GIT_PROTOCOL=version=2")
	}
	(&cgi.Handler{Path: git, Args: []string{"http-backend"}, Root: "/git", Env: env}).ServeHTTP(w, r)
}

func (p *Provider) repository(w http.ResponseWriter, r *http.Request) {
	repo, ok := p.named(w, r)
	if !ok {
		return
	}
	owner, name, _ := strings.Cut(repo.Name, "/")
	writeJSON(w, map[string]any{
		"name": name, "full_name": repo.Name, "default_branch": repo.Branch,
		"owner":   map[string]any{"login": owner, "type": "Organization"},
		"private": false, "visibility": "public", "archived": false, "fork": false,
	})
}

// commit resolves the one commit each fictional repository holds, by any name for it.
func (p *Provider) commit(w http.ResponseWriter, r *http.Request) {
	repo, ok := p.named(w, r)
	if !ok {
		return
	}
	switch ref := r.PathValue("ref"); ref {
	case "HEAD", repo.Branch, repo.Commit:
		writeJSON(w, map[string]any{"sha": repo.Commit})
	default:
		answer(w, http.StatusUnprocessableEntity, "No commit found for SHA: "+ref)
	}
}

func (p *Provider) compare(w http.ResponseWriter, r *http.Request) {
	repo, ok := p.named(w, r)
	if !ok {
		return
	}
	base, head, ok := strings.Cut(r.PathValue("spec"), "...")
	if !ok || base != repo.Commit || head != repo.Commit {
		answer(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, map[string]any{"status": "identical", "ahead_by": 0, "behind_by": 0})
}

// codeScanning answers as GitHub does for a repository nobody has scanned.
func (p *Provider) codeScanning(w http.ResponseWriter, r *http.Request) {
	if _, ok := p.named(w, r); ok {
		answer(w, http.StatusNotFound, "no analysis found")
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func answer(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func loopbackName(host string) bool {
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

func loopbackPeer(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
