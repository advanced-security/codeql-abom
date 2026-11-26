package publish

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// github is the slice of GitHub the publisher talks to: its installations, a token, and one
// repository's Git data and pull requests, recording every write.
type github struct {
	installations []map[string]any
	repositories  []string
	mu            sync.Mutex
	tokenPerms    map[string]string
	tokenRepos    []any
	trees         []map[string]any
	refs          []map[string]any
	moved         []map[string]any
	pulls         []map[string]any
	// branches and open hold what the repository already has: branch names, and the open pull
	// requests' head branches with their addresses.
	branches map[string]bool
	open     map[string]string
}

func (g *github) serve(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	read := func(r *http.Request) map[string]any {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		return body
	}
	mux.HandleFunc("GET /app/installations", func(w http.ResponseWriter, _ *http.Request) { reply(w, g.installations) })
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		body := read(r)
		perms, _ := body["permissions"].(map[string]any)
		g.mu.Lock()
		g.tokenPerms = map[string]string{}
		for k, v := range perms {
			g.tokenPerms[k], _ = v.(string)
		}
		g.tokenRepos, _ = body["repositories"].([]any)
		g.mu.Unlock()
		reply(w, map[string]any{"token": "ghs_publisher", "expires_at": "2099-01-01T00:00:00Z"})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		var repos []map[string]any
		for _, name := range g.repositories {
			repos = append(repos, map[string]any{"name": name})
		}
		reply(w, map[string]any{"total_count": len(repos), "repositories": repos})
	})
	mux.HandleFunc("GET /repos/acme/queries", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"default_branch": "main"})
	})
	mux.HandleFunc("GET /repos/acme/queries/git/ref/heads/main", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"object": map[string]any{"sha": "head-sha"}})
	})
	mux.HandleFunc("GET /repos/acme/queries/git/ref/heads/{branch...}", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.branches[r.PathValue("branch")] {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		reply(w, map[string]any{"object": map[string]any{"sha": "export-sha"}})
	})
	mux.HandleFunc("GET /repos/acme/queries/pulls", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		head := strings.TrimPrefix(r.URL.Query().Get("head"), "acme:")
		pulls := []map[string]any{}
		if url, ok := g.open[head]; ok && r.URL.Query().Get("state") == "open" {
			pulls = append(pulls, map[string]any{"html_url": url})
		}
		reply(w, pulls)
	})
	mux.HandleFunc("GET /repos/acme/queries/git/commits/head-sha", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"tree": map[string]any{"sha": "head-tree"}})
	})
	mux.HandleFunc("GET /repos/acme/queries/git/commits/export-sha", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, map[string]any{"tree": map[string]any{"sha": "export-tree"}})
	})
	mux.HandleFunc("PATCH /repos/acme/queries/git/refs/heads/{branch...}", func(w http.ResponseWriter, r *http.Request) {
		body := read(r)
		body["branch"] = r.PathValue("branch")
		g.mu.Lock()
		g.moved = append(g.moved, body)
		g.mu.Unlock()
		reply(w, map[string]any{"ref": "refs/heads/" + r.PathValue("branch")})
	})
	mux.HandleFunc("POST /repos/acme/queries/git/trees", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.trees = append(g.trees, read(r))
		g.mu.Unlock()
		reply(w, map[string]any{"sha": "new-tree"})
	})
	mux.HandleFunc("POST /repos/acme/queries/git/commits", func(w http.ResponseWriter, r *http.Request) {
		body := read(r)
		if body["tree"] != "new-tree" {
			http.Error(w, "wrong tree", http.StatusUnprocessableEntity)
			return
		}
		reply(w, map[string]any{"sha": "new-commit"})
	})
	mux.HandleFunc("POST /repos/acme/queries/git/refs", func(w http.ResponseWriter, r *http.Request) {
		body := read(r)
		g.mu.Lock()
		g.refs = append(g.refs, body)
		if g.branches == nil {
			g.branches = map[string]bool{}
		}
		ref, _ := body["ref"].(string)
		g.branches[strings.TrimPrefix(ref, "refs/heads/")] = true
		g.mu.Unlock()
		reply(w, map[string]any{"ref": ref})
	})
	mux.HandleFunc("POST /repos/acme/queries/pulls", func(w http.ResponseWriter, r *http.Request) {
		body := read(r)
		g.mu.Lock()
		g.pulls = append(g.pulls, body)
		url := fmt.Sprintf("https://github.com/acme/queries/pull/%d", 6+len(g.pulls))
		if g.open == nil {
			g.open = map[string]string{}
		}
		head, _ := body["head"].(string)
		g.open[head] = url
		g.mu.Unlock()
		reply(w, map[string]any{"html_url": url, "number": 6 + len(g.pulls)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func installation(perms map[string]string, selection string) map[string]any {
	return map[string]any{"id": 42, "account": map[string]any{"login": "acme", "type": "Organization"},
		"repository_selection": selection, "permissions": perms}
}

var exact = map[string]string{"contents": "write", "pull_requests": "write", "metadata": "read"}

func publisher(t *testing.T, g *github) *Publisher {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := g.serve(t)
	return &Publisher{App: &ghapp.App{Credentials: ghapp.Credentials{AppID: 9, Key: key}, API: srv.URL}}
}

// The publisher writes to exactly one repository with exactly the permissions it needs: anything
// broader is refused before anything is written, naming what to change.
func TestThePublisherIsRefusedUnlessItHoldsExactlyOneRepositoryAndItsPermissions(t *testing.T) {
	for name, c := range map[string]struct {
		installations []map[string]any
		repositories  []string
		why           string
	}{
		"not installed": {nil, nil, "not installed"},
		"two accounts": {[]map[string]any{installation(exact, "selected"),
			{"id": 43, "account": map[string]any{"login": "other"}, "repository_selection": "selected", "permissions": exact}},
			[]string{"queries"}, "2 accounts"},
		"every repository": {[]map[string]any{installation(exact, "all")}, nil, "every repository"},
		"two repositories": {[]map[string]any{installation(exact, "selected")}, []string{"queries", "service"}, "2 repositories"},
		"a broader grant": {[]map[string]any{installation(map[string]string{"contents": "write", "pull_requests": "write",
			"metadata": "read", "administration": "write"}, "selected")}, []string{"queries"}, "administration"},
		"a missing grant": {[]map[string]any{installation(map[string]string{"contents": "write", "metadata": "read"},
			"selected")}, []string{"queries"}, "pull_requests"},
		"a read where write": {[]map[string]any{installation(map[string]string{"contents": "read", "pull_requests": "write",
			"metadata": "read"}, "selected")}, []string{"queries"}, "contents"},
	} {
		t.Run(name, func(t *testing.T) {
			g := &github{installations: c.installations, repositories: c.repositories}
			_, err := publisher(t, g).Target(context.Background())
			var mis *Misinstalled
			if !errors.As(err, &mis) || !strings.Contains(err.Error(), c.why) {
				t.Errorf("Target = %v; want it refused as misinstalled, naming %q", err, c.why)
			}
		})
	}
}

// A listing that could not be read says nothing about how the publisher is installed, so it is
// not a misinstall: serve keeps running and the next export asks again.
func TestAnUnreadableListingIsNotAMisinstall(t *testing.T) {
	g := &github{}
	p := publisher(t, g)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer down.Close()
	p.App.API = down.URL
	_, err := p.Target(context.Background())
	var mis *Misinstalled
	if err == nil || errors.As(err, &mis) {
		t.Errorf("Target = %v; want an error that is not a misinstall", err)
	}
}

// An export is one commit on a branch of its own, holding every file, and a pull request you
// merges; the token it writes with carries the publisher's permissions and no more.
func TestAnExportIsOneCommitOnItsOwnBranchAndAPullRequest(t *testing.T) {
	g := &github{installations: []map[string]any{installation(exact, "selected")}, repositories: []string{"queries"}}
	p := publisher(t, g)
	target, err := p.Target(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if target.Owner != "acme" || target.Repo != "queries" {
		t.Fatalf("target = %+v", target)
	}

	url, reused, err := p.Open(context.Background(), target, unpinned)
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/acme/queries/pull/7" || reused {
		t.Errorf("url = %q, reused %v", url, reused)
	}
	if len(g.trees) != 1 || g.trees[0]["base_tree"] != "head-tree" || len(g.trees[0]["tree"].([]any)) != 2 {
		t.Errorf("trees = %+v; want one tree of both files on the default branch's", g.trees)
	}
	if len(g.refs) != 1 || g.refs[0]["ref"] != "refs/heads/abom/export/unpinned-v3" || g.refs[0]["sha"] != "new-commit" {
		t.Errorf("refs = %+v", g.refs)
	}
	if len(g.pulls) != 1 || g.pulls[0]["head"] != "abom/export/unpinned-v3" || g.pulls[0]["base"] != "main" ||
		!strings.Contains(g.pulls[0]["body"].(string), "sha256:abc") {
		t.Errorf("pulls = %+v", g.pulls)
	}
	if len(g.tokenPerms) != len(exact) || g.tokenPerms["contents"] != "write" || g.tokenPerms["pull_requests"] != "write" {
		t.Errorf("the writing token carried %v; want exactly %v", g.tokenPerms, exact)
	}
	if len(g.tokenRepos) != 1 || g.tokenRepos[0] != "queries" {
		t.Errorf("the writing token reaches %v; want only the target repository", g.tokenRepos)
	}
}

var unpinned = Export{Title: "Add abom/unpinned v3", Body: "decision sha256:abc", Branch: "abom/export/unpinned-v3",
	Files: []File{
		{Path: "queries/unpinned/qlpack.yml", Content: []byte("name: acme/unpinned\n")},
		{Path: "queries/unpinned/Unpinned.ql", Content: []byte("select 1\n")},
	}}

// The branch is named after the bundle, so exporting the same bundle again is the same proposal: the
// pull request already open for it is the answer, and nothing is written twice.
func TestExportingTheSameBundleAgainAnswersItsOpenPullRequest(t *testing.T) {
	g := &github{installations: []map[string]any{installation(exact, "selected")}, repositories: []string{"queries"}}
	p := publisher(t, g)
	target, err := p.Target(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := p.Open(context.Background(), target, unpinned)
	if err != nil {
		t.Fatal(err)
	}
	again, reused, err := p.Open(context.Background(), target, unpinned)
	if err != nil || again != first || !reused {
		t.Errorf("exporting again = %q, reused %v, %v; want the open pull request %q", again, reused, err, first)
	}
	if len(g.trees) != 1 || len(g.refs) != 1 || len(g.pulls) != 1 {
		t.Errorf("exporting again wrote %d trees, %d refs and %d pull requests; want one of each in all",
			len(g.trees), len(g.refs), len(g.pulls))
	}

	// Exported again after its pull request closed, under a decision of its own: the branch holds the
	// bundle already, and its record is brought up to this decision on top of it.
	delete(g.open, unpinned.Branch)
	again2 := unpinned
	again2.Files = append([]File{}, unpinned.Files...)
	again2.Files[len(again2.Files)-1] = File{Path: again2.Files[len(again2.Files)-1].Path, Content: []byte("{\"decision\":\"second\"}\n")}
	reopened, reused, err := p.Open(context.Background(), target, again2)
	if err != nil || reopened == first || reused {
		t.Errorf("exporting after its pull request closed = %q, reused %v, %v; want a new pull request", reopened, reused, err)
	}
	if len(g.refs) != 1 || len(g.pulls) != 2 || len(g.trees) != 2 || len(g.moved) != 1 {
		t.Fatalf("reopening wrote %d trees, created %d refs and moved %d; want the branch moved to a commit holding this decision",
			len(g.trees), len(g.refs), len(g.moved))
	}
	if g.trees[1]["base_tree"] != "export-tree" || !strings.Contains(fmt.Sprint(g.trees[1]["tree"]), "second") {
		t.Errorf("the re-export's tree is %+v; want this decision's record on the branch's own tree", g.trees[1])
	}
	if g.moved[0]["branch"] != unpinned.Branch || g.moved[0]["sha"] != "new-commit" {
		t.Errorf("the branch moved as %+v", g.moved[0])
	}
}
