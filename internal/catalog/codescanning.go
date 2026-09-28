package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrNoAnalysis reports that code scanning holds no analysis for a repository, so it holds no
// alerts to compare with. Not the same answer as an analysis that raised none.
var ErrNoAnalysis = errors.New("code scanning holds no analysis for this repository")

// Alert is one code scanning alert as GitHub holds it now, at its most recent instance on the
// default branch.
type Alert struct {
	Number      int
	RuleID      string
	State       string
	Tool        string
	ToolVersion string
	Category    string
	Ref         string
	CommitSHA   string
	Path        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// CodeScanningAlerts reads every CodeQL alert a repository holds, in every state.
//
// Every state, because an open alert is one a developer sees, a dismissed one is one somebody
// already decided about, and a fixed one reopens when its result comes back. A read that asked
// for open alerts alone would count the other two as new.
func (c *Client) CodeScanningAlerts(ctx context.Context, repository string) ([]Alert, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return nil, err
	}
	next := fmt.Sprintf("%s/repos/%s/%s/code-scanning/alerts?tool_name=CodeQL&per_page=%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name), reposPerPage)

	alerts := []Alert{}
	for next != "" {
		resp, err := c.do(ctx, next, jsonAccept)
		if err != nil {
			return nil, noAnalysis(repository, err)
		}
		var page []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
			Rule   struct {
				ID string `json:"id"`
			} `json:"rule"`
			Tool struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"tool"`
			Instance struct {
				Ref       string `json:"ref"`
				Category  string `json:"category"`
				CommitSHA string `json:"commit_sha"`
				Location  struct {
					Path        string `json:"path"`
					StartLine   int    `json:"start_line"`
					StartColumn int    `json:"start_column"`
					EndLine     int    `json:"end_line"`
					EndColumn   int    `json:"end_column"`
				} `json:"location"`
			} `json:"most_recent_instance"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding the alerts of %s: %w", repository, err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()

		for _, p := range page {
			loc := p.Instance.Location
			alerts = append(alerts, Alert{
				Number: p.Number, RuleID: p.Rule.ID, State: p.State,
				Tool: p.Tool.Name, ToolVersion: p.Tool.Version,
				Category: p.Instance.Category, Ref: p.Instance.Ref, CommitSHA: p.Instance.CommitSHA,
				Path: loc.Path, StartLine: loc.StartLine, StartColumn: loc.StartColumn,
				EndLine: loc.EndLine, EndColumn: loc.EndColumn,
			})
		}
		next = nextLink(link)
	}
	return alerts, nil
}

// NoAnalysisError is code scanning answering that it holds no analysis for a repository, with
// GitHub's own reason: it says so for one never analyzed and for one with no default branch.
type NoAnalysisError struct {
	Repository string
	Reason     string
}

func (e *NoAnalysisError) Error() string {
	return fmt.Sprintf("%s: %s", e.Repository, e.Reason)
}

func (e *NoAnalysisError) Is(target error) bool { return target == ErrNoAnalysis }

// noAnalysis names a 404 from code scanning for what it is: both of GitHub's reasons for one hold
// nothing to compare with.
func noAnalysis(repository string, err error) error {
	var status *StatusError
	if !errors.As(err, &status) || status.Code != 404 {
		return err
	}
	reason := status.Message
	if reason == "" {
		reason = "not found"
	}
	return &NoAnalysisError{Repository: repository, Reason: reason}
}

// Analysis is one code scanning analysis GitHub holds.
type Analysis struct {
	CommitSHA   string
	Ref         string
	Category    string
	AnalysisKey string
	CreatedAt   string
	ToolVersion string
}

// ActionsAnalysis finds the newest CodeQL analysis of a repository's Actions at ref among the
// newest page of its analyses, which is the commit its Actions alerts describe.
//
// Recognized by a category naming `language:actions`, which is how Default Setup and the CodeQL
// Action name one. An analysis under a category of somebody's own choosing is not recognized, and
// not found is then what is said, never "none runs". Ref is the default branch the alerts were
// read at; empty reads every ref, and a pull request's analysis is still passed over, since its
// merge commit is not what any default-branch alert describes.
func (c *Client) ActionsAnalysis(ctx context.Context, repository, ref string) (Analysis, bool, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return Analysis{}, false, err
	}
	target := fmt.Sprintf("%s/repos/%s/%s/code-scanning/analyses?tool_name=CodeQL&per_page=%d",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name), reposPerPage)
	if ref != "" {
		target += "&ref=" + url.QueryEscape(ref)
	}
	resp, err := c.do(ctx, target, jsonAccept)
	if err != nil {
		return Analysis{}, false, noAnalysis(repository, err)
	}
	defer resp.Body.Close()

	var page []struct {
		Ref         string `json:"ref"`
		CommitSHA   string `json:"commit_sha"`
		Category    string `json:"category"`
		AnalysisKey string `json:"analysis_key"`
		CreatedAt   string `json:"created_at"`
		Tool        struct {
			Version string `json:"version"`
		} `json:"tool"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return Analysis{}, false, fmt.Errorf("decoding the analyses of %s: %w", repository, err)
	}
	// Newest first is GitHub's order; the page is read in it rather than re-sorted.
	for _, p := range page {
		if !strings.Contains(p.Category, "language:actions") || strings.HasPrefix(p.Ref, "refs/pull/") {
			continue
		}
		return Analysis{
			CommitSHA: p.CommitSHA, Ref: p.Ref, Category: p.Category, AnalysisKey: p.AnalysisKey,
			CreatedAt: p.CreatedAt, ToolVersion: p.Tool.Version,
		}, true, nil
	}
	return Analysis{}, false, nil
}

// Moved is how two commits of one repository relate, as GitHub counts it.
type Moved struct {
	// Status is `identical`, `ahead` (head has commits base lacks), `behind` or `diverged`.
	Status   string
	AheadBy  int
	BehindBy int
}

// CompareCommits reports how head relates to base.
func (c *Client) CompareCommits(ctx context.Context, repository, base, head string) (Moved, error) {
	owner, name, err := splitRepository(repository)
	if err != nil {
		return Moved{}, err
	}
	if base == "" || head == "" {
		return Moved{}, fmt.Errorf("comparing commits of %s: both commits are needed", repository)
	}
	// One commit per page: the answer read is the counts, and the list would be the whole history.
	resp, err := c.do(ctx, fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s?per_page=1",
		c.baseURL, url.PathEscape(owner), url.PathEscape(name),
		url.PathEscape(base), url.PathEscape(head)), jsonAccept)
	if err != nil {
		return Moved{}, err
	}
	defer resp.Body.Close()

	var out struct {
		Status   string `json:"status"`
		AheadBy  int    `json:"ahead_by"`
		BehindBy int    `json:"behind_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Moved{}, fmt.Errorf("decoding a comparison of %s: %w", repository, err)
	}
	if out.Status == "" {
		return Moved{}, fmt.Errorf("comparing %s...%s in %s returned no status", base, head, repository)
	}
	return Moved{Status: out.Status, AheadBy: out.AheadBy, BehindBy: out.BehindBy}, nil
}
