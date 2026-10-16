package main

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

func protectSources(s store.Backend, reader *catalog.Client, log io.Writer) *sourcepolicy.Policy {
	p := &sourcepolicy.Policy{
		Read: reader, Owned: s.RepositoryIsOwned,
		Evidence: func(repository string) (string, error) {
			rows, err := s.SetupClasses(repository)
			if err != nil {
				return "", err
			}
			for _, r := range rows {
				switch {
				case r.Unreadable == "" && r.State == "configured":
					return "default setup is configured", nil
				case r.ConfigurationDefaultSet == "enabled" &&
					(r.AttachmentStatus == "attached" || r.AttachmentStatus == "enforced"):
					return "an applied security configuration enables scanning", nil
				case r.AnalysesUnreadable == "" && (r.AdvancedKey != "" || r.ActionsDefaultAt != ""):
					return "code scanning has reported analyses", nil
				}
			}
			return "", nil
		},
		Warn: func(repository, reason string) {
			fmt.Fprintf(log, "source policy: %s: %s\n", repository, reason)
		},
	}
	reader.SourcePolicy = p.Check
	return p
}

func wireSourcePolicy(
	ctx context.Context, e *expand.Expander, s store.Backend, apiURL string, tokens ghapp.TokenSource, log io.Writer,
) {
	reader := newReader(ctx, apiURL, tokens)
	reader.Pace = githubPace
	reader.REST = processPools.Pool(work.PoolFetching, work.UnitREST)
	reader.MaxRetries = 0
	p := protectSources(s, reader, log)
	e.SourcePolicy = p
	if checkout, ok := e.Fetcher.(*expand.Checkout); ok {
		bindSourcePolicy(checkout, apiURL, p.Check)
	}
}

func bindSourcePolicy(checkout *expand.Checkout, apiURL string, check func(context.Context, string) error) {
	if apiURL == "" {
		apiURL = ghapp.DefaultAPIURL
	}
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		checkout.SourcePolicy = func(context.Context, string) error {
			return fmt.Errorf("source policy: configure an HTTP(S) GitHub API URL without embedded credentials")
		}
		return
	}
	checkout.BaseURL = u.Scheme + "://" + u.Host
	if strings.EqualFold(u.Host, "api.github.com") {
		checkout.BaseURL = "https://github.com"
	}
	checkout.SourcePolicy = check
}

func sourceSetupReader(s store.Backend, policy *sourcepolicy.Policy,
	read func(context.Context, string) error,
) func(context.Context, string) error {
	return func(ctx context.Context, repository string) error {
		if err := read(ctx, repository); err != nil {
			return err
		}
		if policy == nil {
			return nil
		}
		policy.Invalidate(repository)
		skipped, err := s.SourcePolicySkipped(repository)
		if err != nil || !skipped {
			return err
		}
		if err := policy.Check(ctx, repository); err != nil {
			if sourcepolicy.IsDenied(err) {
				return nil
			}
			return err
		}
		_, err = s.RetrySourcePolicy(repository)
		return err
	}
}
