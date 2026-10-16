package catalog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// DefaultSetupKeyPrefix begins the analysis key of every analysis default setup makes; any other
// key is advanced setup, whatever ran it.
const DefaultSetupKeyPrefix = "dynamic/github-code-scanning/"

// DefaultSetup is a repository's code scanning default setup, in GitHub's own words.
//
// When State is `not-configured`, Languages is what GitHub would analyze if it were turned on;
// otherwise it is what is configured. Names are kept as read, including ones the write does not take.
type DefaultSetup struct {
	State       string
	Languages   []string
	QuerySuite  string
	ThreatModel string
	RunnerType  string
	RunnerLabel string
	Schedule    string
	UpdatedAt   string
}

// DefaultSetup reads one repository's default setup.
func (c *Client) DefaultSetup(ctx context.Context, repository string) (DefaultSetup, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return DefaultSetup{}, err
	}
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/code-scanning/default-setup",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name)), jsonAccept)
	if err != nil {
		return DefaultSetup{}, err
	}
	defer resp.Body.Close()
	var p struct {
		State       string   `json:"state"`
		Languages   []string `json:"languages"`
		QuerySuite  string   `json:"query_suite"`
		ThreatModel string   `json:"threat_model"`
		UpdatedAt   *string  `json:"updated_at"`
		Schedule    *string  `json:"schedule"`
		RunnerType  *string  `json:"runner_type"`
		RunnerLabel *string  `json:"runner_label"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return DefaultSetup{}, fmt.Errorf("decoding the default setup of %s: %w", repository, err)
	}
	if p.Languages == nil {
		p.Languages = []string{}
	}
	return DefaultSetup{State: p.State, Languages: p.Languages, QuerySuite: p.QuerySuite,
		ThreatModel: p.ThreatModel, UpdatedAt: deref(p.UpdatedAt), Schedule: deref(p.Schedule),
		RunnerType: deref(p.RunnerType), RunnerLabel: deref(p.RunnerLabel)}, nil
}

// SecurityConfiguration is one code security configuration an organization offers.
type SecurityConfiguration struct {
	ID           int64
	Name         string
	TargetType   string
	Enforcement  string
	DefaultSetup string
	// AllowAdvanced is nil where the configuration does not say.
	AllowAdvanced *bool
	RunnerType    string
	RunnerLabel   string
}

// SecurityConfigurations reads every configuration an organization offers, its own and those its
// enterprise or GitHub applies to it.
func (c *Client) SecurityConfigurations(ctx context.Context, org string) ([]SecurityConfiguration, error) {
	if org == "" {
		return nil, fmt.Errorf("an organization is needed to read its configurations")
	}
	next := fmt.Sprintf("%s/orgs/%s/code-security/configurations?per_page=%d",
		c.baseURL, url.PathEscape(org), reposPerPage)
	out := []SecurityConfiguration{}
	for next != "" {
		resp, err := c.do(ctx, next, jsonAccept)
		if err != nil {
			return nil, err
		}
		var page []struct {
			ID                  int64  `json:"id"`
			Name                string `json:"name"`
			TargetType          string `json:"target_type"`
			Enforcement         string `json:"enforcement"`
			DefaultSetup        string `json:"code_scanning_default_setup"`
			DefaultSetupOptions *struct {
				RunnerType  *string `json:"runner_type"`
				RunnerLabel *string `json:"runner_label"`
			} `json:"code_scanning_default_setup_options"`
			CodeScanningOptions *struct {
				AllowAdvanced *bool `json:"allow_advanced"`
			} `json:"code_scanning_options"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding the configurations of %s: %w", org, err)
		}
		for _, p := range page {
			cfg := SecurityConfiguration{ID: p.ID, Name: p.Name, TargetType: p.TargetType,
				Enforcement: p.Enforcement, DefaultSetup: p.DefaultSetup}
			if o := p.DefaultSetupOptions; o != nil {
				cfg.RunnerType, cfg.RunnerLabel = deref(o.RunnerType), deref(o.RunnerLabel)
			}
			if o := p.CodeScanningOptions; o != nil {
				cfg.AllowAdvanced = o.AllowAdvanced
			}
			out = append(out, cfg)
		}
		next = nextLink(link)
	}
	return out, nil
}

// ConfigurationRepository is one repository a configuration is applied to, with GitHub's status.
type ConfigurationRepository struct {
	Repository string
	Status     string
}

// ConfigurationRepositories reads every repository one configuration is applied to.
func (c *Client) ConfigurationRepositories(ctx context.Context, org string, id int64) ([]ConfigurationRepository, error) {
	if org == "" || id <= 0 {
		return nil, fmt.Errorf("an organization and a configuration are needed to read its repositories")
	}
	next := fmt.Sprintf("%s/orgs/%s/code-security/configurations/%d/repositories?per_page=%d",
		c.baseURL, url.PathEscape(org), id, reposPerPage)
	out := []ConfigurationRepository{}
	for next != "" {
		resp, err := c.do(ctx, next, jsonAccept)
		if err != nil {
			return nil, err
		}
		var page []struct {
			Status     string `json:"status"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		link := resp.Header.Get("Link")
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decoding the repositories of configuration %d in %s: %w", id, org, err)
		}
		for _, p := range page {
			if p.Repository.FullName == "" {
				continue
			}
			out = append(out, ConfigurationRepository{Repository: p.Repository.FullName, Status: p.Status})
		}
		next = nextLink(link)
	}
	return out, nil
}

// CodeQLAnalyses lists every CodeQL analysis on the newest page of a repository's, newest first,
// pull requests included: whichever setup made them is what the page is read to tell.
func (c *Client) CodeQLAnalyses(ctx context.Context, repository string) ([]Analysis, error) {
	return c.analysesPage(ctx, repository, "")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// FileContent reads one file of a repository at ref through the contents API.
func (c *Client) FileContent(ctx context.Context, repository, path, ref string) ([]byte, error) {
	if err := c.allowSource(ctx, repository); err != nil {
		return nil, err
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf("%s/repos/%s/%s/contents/%s", c.baseURL, url.PathEscape(owner), url.PathEscape(name),
		escapePath(path))
	if ref != "" {
		target += "?ref=" + url.QueryEscape(ref)
	}
	resp, err := c.do(ctx, target, jsonAccept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var p struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, fmt.Errorf("decoding %s in %s: %w", path, repository, err)
	}
	if p.Type != "file" || p.Encoding != "base64" {
		return nil, fmt.Errorf("%s in %s is a %s, not a file this can read", path, repository, p.Type)
	}
	return base64.StdEncoding.DecodeString(strings.ReplaceAll(p.Content, "\n", ""))
}

// escapePath escapes each segment of a repository path and keeps the slashes between them.
func escapePath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// RepositoryConfiguration is the configuration attached to one repository, and GitHub's status for it.
// Configuration is nil where none is attached.
type RepositoryConfiguration struct {
	Status        string
	Configuration *SecurityConfiguration
}

// RepositoryConfiguration reads which configuration one repository has. It needs the repository's
// own administration read, so it answers where its organization's whole list cannot be read.
func (c *Client) RepositoryConfiguration(ctx context.Context, repository string) (RepositoryConfiguration, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return RepositoryConfiguration{}, err
	}
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/code-security-configuration",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name)), jsonAccept)
	var status *StatusError
	if errors.As(err, &status) && status.Code == http.StatusNoContent {
		return RepositoryConfiguration{}, nil
	}
	if err != nil {
		return RepositoryConfiguration{}, err
	}
	defer resp.Body.Close()
	var p struct {
		Status        string `json:"status"`
		Configuration *struct {
			ID           int64  `json:"id"`
			Name         string `json:"name"`
			TargetType   string `json:"target_type"`
			Enforcement  string `json:"enforcement"`
			DefaultSetup string `json:"code_scanning_default_setup"`
		} `json:"configuration"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return RepositoryConfiguration{}, fmt.Errorf("decoding the configuration of %s: %w", repository, err)
	}
	out := RepositoryConfiguration{Status: p.Status}
	if cfg := p.Configuration; cfg != nil {
		out.Configuration = &SecurityConfiguration{ID: cfg.ID, Name: cfg.Name, TargetType: cfg.TargetType,
			Enforcement: cfg.Enforcement, DefaultSetup: cfg.DefaultSetup}
	}
	return out, nil
}

// ActionsPermissions is whether Actions runs in a repository and which actions it allows.
type ActionsPermissions struct {
	Enabled        bool
	AllowedActions string
	// Selected is read only where AllowedActions is "selected", the one case it decides.
	Selected *SelectedActions
}

// SelectedActions is the allowlist a repository keeps when it allows selected actions.
type SelectedActions struct {
	GitHubOwnedAllowed bool
	VerifiedAllowed    bool
	PatternsAllowed    []string
}

// ActionsPermissions reads a repository's Actions policy, and its allowlist where that decides.
func (c *Client) ActionsPermissions(ctx context.Context, repository string) (ActionsPermissions, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return ActionsPermissions{}, err
	}
	base := fmt.Sprintf("%s/repos/%s/%s/actions/permissions", c.baseURL, url.PathEscape(owner), url.PathEscape(name))
	resp, err := c.do(ctx, base, jsonAccept)
	if err != nil {
		return ActionsPermissions{}, err
	}
	var p struct {
		Enabled        bool   `json:"enabled"`
		AllowedActions string `json:"allowed_actions"`
	}
	err = json.NewDecoder(resp.Body).Decode(&p)
	resp.Body.Close()
	if err != nil {
		return ActionsPermissions{}, fmt.Errorf("decoding the Actions policy of %s: %w", repository, err)
	}
	out := ActionsPermissions{Enabled: p.Enabled, AllowedActions: p.AllowedActions}
	if !p.Enabled || p.AllowedActions != "selected" {
		return out, nil
	}
	resp, err = c.do(ctx, base+"/selected-actions", jsonAccept)
	if err != nil {
		return ActionsPermissions{}, err
	}
	defer resp.Body.Close()
	var s struct {
		GitHubOwnedAllowed bool     `json:"github_owned_allowed"`
		VerifiedAllowed    bool     `json:"verified_allowed"`
		PatternsAllowed    []string `json:"patterns_allowed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ActionsPermissions{}, fmt.Errorf("decoding the allowed actions of %s: %w", repository, err)
	}
	if s.PatternsAllowed == nil {
		s.PatternsAllowed = []string{}
	}
	out.Selected = &SelectedActions{GitHubOwnedAllowed: s.GitHubOwnedAllowed, VerifiedAllowed: s.VerifiedAllowed,
		PatternsAllowed: s.PatternsAllowed}
	return out, nil
}
