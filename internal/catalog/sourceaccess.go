package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

// SourceAccess is repository metadata, not a contractual licensing verdict.
type SourceAccess struct {
	Repository       string
	Visibility       string
	CodeSecurity     string
	AdvancedSecurity string
}

func (c *Client) SourceAccess(ctx context.Context, repository string) (SourceAccess, error) {
	got, err := c.sourceAccess(ctx, repository)
	if err == nil || ctx.Err() != nil || (c.token == "" && c.Tokens == nil) {
		return got, err
	}
	var status *StatusError
	refused := errors.As(err, &status) && (status.Code == 401 || status.Code == 403 || status.Code == 404)
	if !refused && !errors.Is(err, ghapp.ErrNotInstalled) {
		return got, err
	}
	// An SSO-limited credential can be less capable than an anonymous public read.
	public := New(c.baseURL, "", c.http)
	public.MaxRetries = 0
	anonymous, publicErr := public.sourceAccess(ctx, repository)
	c.requests.Add(public.requests.Load())
	if publicErr == nil && anonymous.Visibility == "public" {
		return anonymous, nil
	}
	return got, err
}

func (c *Client) sourceAccess(ctx context.Context, repository string) (SourceAccess, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return SourceAccess{}, err
	}
	resp, err := c.doUncached(ctx, fmt.Sprintf("%s/repos/%s/%s",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name)), jsonAccept)
	if err != nil {
		return SourceAccess{}, err
	}
	defer resp.Body.Close()
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return SourceAccess{}, err
	}
	if resp.Request.URL.Host != base.Host || resp.Request.URL.Scheme != base.Scheme {
		return SourceAccess{}, fmt.Errorf("repository metadata redirected outside %s", base.Host)
	}
	var r struct {
		FullName   string `json:"full_name"`
		Visibility string `json:"visibility"`
		Private    *bool  `json:"private"`
		Security   struct {
			CodeSecurity struct {
				Status string `json:"status"`
			} `json:"code_security"`
			AdvancedSecurity struct {
				Status string `json:"status"`
			} `json:"advanced_security"`
		} `json:"security_and_analysis"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return SourceAccess{}, fmt.Errorf("reading source access for %s: %w", repository, err)
	}
	if _, _, err := splitRepository(r.FullName); err != nil {
		return SourceAccess{}, fmt.Errorf("source access for %s returned no valid repository name", repository)
	}
	if r.Visibility == "" && r.Private != nil {
		r.Visibility = "public"
		if *r.Private {
			r.Visibility = "private"
		}
	}
	return SourceAccess{Repository: r.FullName, Visibility: r.Visibility,
		CodeSecurity: r.Security.CodeSecurity.Status, AdvancedSecurity: r.Security.AdvancedSecurity.Status}, nil
}

// CheckScanningAccess asks for one alert, not all alerts or only one tool's findings.
func (c *Client) CheckScanningAccess(ctx context.Context, repository string) error {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return err
	}
	resp, err := c.doUncached(ctx, fmt.Sprintf("%s/repos/%s/%s/code-scanning/alerts?per_page=1",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name)), jsonAccept)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var alerts []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		return fmt.Errorf("checking code-scanning access for %s: %w", repository, err)
	}
	if alerts == nil {
		return fmt.Errorf("checking code-scanning access for %s returned no list", repository)
	}
	return nil
}

func (c *Client) allowSource(ctx context.Context, repository string) error {
	if c.SourcePolicy == nil {
		return nil
	}
	return c.SourcePolicy(ctx, repository)
}
