package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/publish"
	"github.com/advanced-security/codeql-abom/internal/serve"
)

// publisherFor is the App query exports open pull requests with, nil where none is configured.
// One installed beyond a single repository, or holding more than it may, stops serve: ABOM would
// hold a write it must not. A listing that cannot be read is asked again at each export.
func publisherFor(ctx context.Context, dir, apiURL string, log io.Writer) (serve.Publisher, error) {
	if !ghapp.Configured(dir) {
		return nil, nil
	}
	creds, err := ghapp.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("serve: loading the publisher App in %s: %w", dir, err)
	}
	p := &publish.Publisher{App: &ghapp.App{Credentials: creds, API: apiURL}}
	target, err := p.Target(ctx)
	var misinstalled *publish.Misinstalled
	switch {
	case errors.As(err, &misinstalled):
		return nil, fmt.Errorf("serve: the publisher App in %s: %w", dir, err)
	case err != nil:
		fmt.Fprintf(log, "serve: the publisher App's installation could not be read (%v); each export asks again\n", err)
	default:
		fmt.Fprintf(log, "serve: query exports open pull requests in %s as %s\n", target, creds.Slug)
	}
	return p, nil
}
