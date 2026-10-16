package main

import (
	"context"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// wireCodeScanning lets the crawl take code scanning's databases and analyses wherever they are
// ABOM's own result, unless the operator turned that off, which the crawl then says.
func wireCodeScanning(ctx context.Context, loop *supervisor.Supervisor, s store.Backend, cfg serveConfig) {
	if !cfg.discovery.codeScanningReuse {
		loop.Expander.CodeScanningOff = "this server was started with --code-scanning-reuse=false"
		return
	}
	client := newReader(ctx, cfg.apiURL, cfg.tokens)
	client.Cache = s.ResponseCache()
	// The build a refused read falls back to costs less than waiting out a rate limit.
	client.MaxRetries = 0
	client.REST = processPools.Pool(work.PoolFetching, work.UnitREST)
	if policy := loop.Expander.SourcePolicy; policy != nil {
		client.SourcePolicy = policy.Check
	}
	loop.Expander.CodeScanning = client
}
