package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// pruneHistory prunes what retention no longer keeps now and then every period until ctx ends, saying what it removed.
func pruneHistory(ctx context.Context, s *store.Store, every time.Duration, now func() time.Time, log io.Writer) {
	days := int(store.HistoryRetention / (24 * time.Hour))
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		at := now().UTC()
		before := at.Add(-store.HistoryRetention)
		pruned, err := s.PruneHistory(before)
		switch {
		case err != nil:
			fmt.Fprintf(log, "%s serve: keeping %d days of history: %v\n", at.Format(time.RFC3339), days, err)
		case pruned != store.HistoryPruned{}:
			fmt.Fprintf(log, "%s serve: kept %d days of history: removed %s, %s and %s older than %s\n",
				at.Format(time.RFC3339), days,
				counted(pruned.Deliveries, "delivery", "deliveries"),
				counted(pruned.Buckets, "meter minute", "meter minutes"),
				counted(pruned.Runs, "workflow run", "workflow runs"),
				before.Format(time.RFC3339))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// counted is n with its noun, singular for exactly one.
func counted(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}
