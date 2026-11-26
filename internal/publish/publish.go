// Package publish opens pull requests in the one repository ABOM's publisher App may write to. It
// is the only writer ABOM holds, and it is shaped so that it cannot become a broader one: anything
// beyond one repository's contents and pull requests is refused before a byte is written.
package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	neturl "net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// permissions is everything the publisher may hold, and what each of its tokens carries.
var permissions = ghapp.PublisherPermissions

// Misinstalled is a publisher GitHub reports installed somewhere other than exactly one repository
// with exactly permissions; failing to read the installations at all is not one.
type Misinstalled struct{ Reason string }

func (e *Misinstalled) Error() string { return e.Reason }

// Target is the one repository exports go to.
type Target struct {
	Owner, Repo string
}

func (t Target) String() string { return t.Owner + "/" + t.Repo }

// File is one path an export writes, relative to the repository root.
type File struct {
	Path    string
	Content []byte
}

// Export is one pull request: its branch, its title and body, and every file it writes.
type Export struct {
	Title, Body, Branch string
	Files               []File
}

// Publisher acts as the publisher App.
type Publisher struct {
	App  *ghapp.App
	HTTP *http.Client
}

// Target reads where the publisher is installed and refuses unless it is exactly one repository with
// exactly permissions. Read each time, so a widened installation is refused at the next export.
func (p *Publisher) Target(ctx context.Context) (Target, error) {
	sync := p.App.ReadInstallations(ctx)
	if sync.Unread != "" {
		return Target{}, fmt.Errorf("the publisher App's installations cannot be read: %s", sync.Unread)
	}
	var active []int
	for i, inst := range sync.Installations {
		if !inst.Suspended {
			active = append(active, i)
		}
	}
	switch len(active) {
	case 0:
		return Target{}, &Misinstalled{"the publisher App is not installed; install it on the one repository exports go to"}
	case 1:
	default:
		return Target{}, &Misinstalled{fmt.Sprintf("the publisher App is installed on %d accounts; it may write to one repository only",
			len(active))}
	}
	inst := sync.Installations[active[0]]
	switch {
	case inst.RepositorySelection != "selected":
		return Target{}, &Misinstalled{fmt.Sprintf("the publisher App is installed on every repository of %s; "+
			"choose only the repository exports go to", inst.Owner)}
	case inst.RepositoriesUnread != "":
		return Target{}, fmt.Errorf("the publisher App's repositories cannot be read: %s", inst.RepositoriesUnread)
	case len(inst.Repositories) != 1:
		return Target{}, &Misinstalled{fmt.Sprintf("the publisher App is installed on %d repositories of %s; it may write to one only",
			len(inst.Repositories), inst.Owner)}
	}
	if wrong := differ(inst.Permissions); len(wrong) > 0 {
		return Target{}, &Misinstalled{fmt.Sprintf("the publisher App holds %s; it may hold contents and pull requests write "+
			"and metadata read, and nothing else", strings.Join(wrong, ", "))}
	}
	return Target{Owner: inst.Owner, Repo: inst.Repositories[0]}, nil
}

// differ names every permission granted beyond permissions, short of it, or at another level.
func differ(granted map[string]string) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(granted)) {
		if want, ok := permissions[name]; !ok || want != granted[name] {
			out = append(out, name+": "+granted[name])
		}
	}
	for _, name := range slices.Sorted(maps.Keys(permissions)) {
		if _, ok := granted[name]; !ok {
			out = append(out, name+": not granted")
		}
	}
	return out
}

// Open writes the export's files as one commit on a new branch off the default branch, and opens a
// pull request from it; reused is a pull request already open from that branch, which answers for
// the same bundle without writing anything. Nothing is merged: you do that, in that repository.
func (p *Publisher) Open(ctx context.Context, t Target, e Export) (url string, reused bool, err error) {
	for _, f := range e.Files {
		if !utf8.Valid(f.Content) {
			return "", false, fmt.Errorf("%s is not text, and an export writes text only", f.Path)
		}
	}
	token, err := p.App.RepositoryTokenWith(ctx, t.Owner, t.Repo, permissions)
	if err != nil {
		return "", false, fmt.Errorf("minting the publisher's token: %w", err)
	}
	c := client{base: p.base(), token: token, http: p.client()}
	repo := "/repos/" + neturl.PathEscape(t.Owner) + "/" + neturl.PathEscape(t.Repo)

	var open []struct {
		HTMLURL string `json:"html_url"`
	}
	query := neturl.Values{"head": {t.Owner + ":" + e.Branch}, "state": {"open"}}
	if err := c.do(ctx, http.MethodGet, repo+"/pulls?"+query.Encode(), nil, &open); err != nil {
		return "", false, err
	}
	if len(open) > 0 {
		return open[0].HTMLURL, true, nil
	}
	var meta struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := c.do(ctx, http.MethodGet, repo, nil, &meta); err != nil {
		return "", false, err
	}
	switch err := c.do(ctx, http.MethodGet, repo+"/git/ref/heads/"+e.Branch, nil, nil); {
	case err == nil:
		// The branch is named after the bundle, so it already holds these files.
	case errors.Is(err, errNotFound):
		if err := p.commit(ctx, c, repo, meta.DefaultBranch, e); err != nil {
			return "", false, err
		}
	default:
		return "", false, err
	}
	var pull struct {
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(ctx, http.MethodPost, repo+"/pulls",
		map[string]any{"title": e.Title, "head": e.Branch, "base": meta.DefaultBranch, "body": e.Body}, &pull); err != nil {
		return "", false, err
	}
	return pull.HTMLURL, false, nil
}

// commit writes the export's files as one commit off base, on the export's branch.
func (p *Publisher) commit(ctx context.Context, c client, repo, base string, e Export) error {
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := c.do(ctx, http.MethodGet, repo+"/git/ref/heads/"+base, nil, &head); err != nil {
		return err
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err := c.do(ctx, http.MethodGet, repo+"/git/commits/"+head.Object.SHA, nil, &commit); err != nil {
		return err
	}
	entries := make([]map[string]string, 0, len(e.Files))
	for _, f := range e.Files {
		entries = append(entries, map[string]string{"path": f.Path, "mode": "100644", "type": "blob", "content": string(f.Content)})
	}
	var tree, made struct {
		SHA string `json:"sha"`
	}
	if err := c.do(ctx, http.MethodPost, repo+"/git/trees",
		map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &tree); err != nil {
		return err
	}
	if err := c.do(ctx, http.MethodPost, repo+"/git/commits",
		map[string]any{"message": e.Title, "tree": tree.SHA, "parents": []string{head.Object.SHA}}, &made); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, repo+"/git/refs",
		map[string]any{"ref": "refs/heads/" + e.Branch, "sha": made.SHA}, nil)
}

func (p *Publisher) base() string {
	if p.App.API != "" {
		return strings.TrimSuffix(p.App.API, "/")
	}
	return ghapp.DefaultAPIURL
}

func (p *Publisher) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

type client struct {
	base, token string
	http        *http.Client
}

var errNotFound = errors.New("not found")

// do makes one request; a refusal carries GitHub's message and never the token.
func (c client) do(ctx context.Context, method, path string, body, into any) error {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %s", method, path, strings.ReplaceAll(err.Error(), c.token, "[token]"))
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(answer, &msg)
		err := fmt.Errorf("%s %s: GitHub answered %d: %s", method, path, resp.StatusCode, msg.Message)
		if resp.StatusCode == http.StatusNotFound {
			err = errors.Join(errNotFound, err)
		}
		return err
	}
	if into != nil {
		return json.Unmarshal(answer, into)
	}
	return nil
}
