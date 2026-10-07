package main

import (
	"context"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// wireCodeScanning lets the crawl take code scanning's databases and analyses wherever they are ABOM's own result.
func wireCodeScanning(
	ctx context.Context, loop *supervisor.Supervisor, s *store.Store, apiURL string, tokens ghapp.TokenSource,
) {
	client := newReader(ctx, apiURL, tokens)
	client.Cache = s.ResponseCache()
	// The build a refused read falls back to costs less than waiting out a rate limit.
	client.MaxRetries = 0
	client.REST = processPools.Pool(work.PoolFetching, work.UnitREST)
	loop.Expander.CodeScanning = client
}
