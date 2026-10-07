package main

import (
	"context"
	"io"

	"github.com/advanced-security/codeql-abom/internal/poll"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// pollExternalReferences polls only while discovery runs, since nothing claims a requeued reference while it is paused.
func pollExternalReferences(ctx context.Context, cfg serveConfig, s *store.Store, loop *supervisor.Supervisor,
	stdout io.Writer) {
	p := &poll.Poller{
		Store: s, Queue: loop.Queue, Resolve: githubClient(ctx, cfg.apiURL, s, cfg.tokens),
		Pool: processPools.Pool(work.PoolPolling, work.UnitGraphQL),
		Active: func() bool {
			state := loop.Status().State
			return state == supervisor.StateWorking || state == supervisor.StateIdle
		},
		Log: stdout,
	}
	go p.Run(ctx)
}
