package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// Patcher makes the one write ABOM makes to code scanning — default setup's languages — with the
// operator's own token, and only to a repository Allowed names. The guard is here rather than in
// its callers because the token can also delete and rename repositories.
type Patcher struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// Allowed names the repositories that may be written; nil allows none.
	Allowed func(repository string) bool
}

// PatchResult is GitHub's answer to a write: 202 starts a setup run, named here.
type PatchResult struct {
	Status  int
	RunID   int64
	RunURL  string
	Message string
}

// ErrNotATarget refuses a repository outside the server's write targets, before any request.
type ErrNotATarget struct{ Repository string }

func (e *ErrNotATarget) Error() string {
	return e.Repository + " is not a write target; name it in --enable-targets or its account in --enable-accounts"
}

// ErrChanged is default setup no longer saying what the write was planned against.
type ErrChanged struct {
	Repository string
	State      string
	Languages  []string
}

func (e *ErrChanged) Error() string {
	return fmt.Sprintf("%s now reads %s for %s, which is not what the write was planned against",
		e.Repository, e.State, strings.Join(e.Languages, ", "))
}

// Write reads default setup with the write token and sends body only if it still reads state and
// languages. GitHub refuses a conditional header on this write, so that read, seconds earlier, is
// the guard against a change in between, and the read-back catches what slips past it.
func (p *Patcher) Write(ctx context.Context, repository, state string, languages []string, body PatchBody) (PatchResult, error) {
	if p.Allowed == nil || !p.Allowed(repository) {
		return PatchResult{}, &ErrNotATarget{Repository: repository}
	}
	owner, name, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return PatchResult{}, fmt.Errorf("%q is not owner/name", repository)
	}
	target := fmt.Sprintf("%s/repos/%s/%s/code-scanning/default-setup",
		strings.TrimSuffix(p.BaseURL, "/"), url.PathEscape(owner), url.PathEscape(name))

	resp, err := p.do(ctx, http.MethodGet, target, nil)
	if err != nil {
		return PatchResult{}, err
	}
	var read struct {
		State     string   `json:"state"`
		Languages []string `json:"languages"`
	}
	err = json.NewDecoder(resp.Body).Decode(&read)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || err != nil {
		return PatchResult{}, fmt.Errorf("reading %s before writing it answered %d: %v", repository, resp.StatusCode, err)
	}
	if read.State != state || !slices.Equal(WriteNames(read.Languages), WriteNames(languages)) {
		return PatchResult{}, &ErrChanged{Repository: repository, State: read.State, Languages: read.Languages}
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return PatchResult{}, err
	}
	resp, err = p.do(ctx, http.MethodPatch, target, raw)
	if err != nil {
		return PatchResult{}, err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed struct {
		RunID   int64  `json:"run_id"`
		RunURL  string `json:"run_url"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(answer, &parsed)
	return PatchResult{Status: resp.StatusCode, RunID: parsed.RunID, RunURL: parsed.RunURL, Message: parsed.Message}, nil
}

// Login names the write token's owner, which is who GitHub records as making the change.
func (p *Patcher) Login(ctx context.Context) (string, error) {
	resp, err := p.do(ctx, http.MethodGet, strings.TrimSuffix(p.BaseURL, "/")+"/user", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil || resp.StatusCode != http.StatusOK || user.Login == "" {
		return "", fmt.Errorf("the write token's owner could not be read (%d): %v", resp.StatusCode, err)
	}
	return user.Login, nil
}

func (p *Patcher) do(ctx context.Context, method, target string, body []byte) (*http.Response, error) {
	if p.Token == "" {
		return nil, fmt.Errorf("no write token: default setup is written with the operator's own token")
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Authorization", "Bearer "+p.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}
