package enumerate

import (
	"context"
	"slices"
	"sync"
)

// ReadSetup reads and records one owned repository's code scanning setup: default setup, who runs
// its CodeQL analyses, and whether code scanning holds its Actions database.
type ReadSetup func(ctx context.Context, repository string) error

// ReadConfigurations reads and records an account's code security configurations and where each
// is applied. An account's whole answer, so only a full listing asks it.
type ReadConfigurations func(ctx context.Context, account string) error

// rememberSetup reads each owned repository a listing named, the way rememberBranches does: a full
// listing asks everything once a period and a sweep asks only what moved. A read that fails leaves
// the last reading standing, because not asked is not switched off.
func (o Options) rememberSetup(ctx context.Context, listed []string, one *AccountEnumeration) error {
	if o.Setup == nil || len(listed) == 0 {
		return nil
	}
	owned, err := o.Store.OwnedRepositories()
	if err != nil {
		return err
	}
	ours := make(map[string]bool, len(owned))
	for _, name := range owned {
		ours[name] = true
	}
	todo := slices.DeleteFunc(slices.Clone(listed), func(r string) bool { return !ours[r] })
	failed := make([]error, len(todo))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(setupWorkers, len(todo)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				failed[i] = o.Setup(ctx, todo[i])
			}
		}()
	}
	for i := range todo {
		next <- i
	}
	close(next)
	wg.Wait()
	for i, err := range failed {
		if err == nil {
			one.SetupRead++
			continue
		}
		one.SetupUnread++
		if one.SetupUnreadWhy == "" {
			one.SetupUnreadWhy = todo[i] + ": " + err.Error()
		}
	}
	return nil
}

// setupWorkers is how many repositories are read at once. Each read is round trips the pool paces,
// so overlapping them costs no more quota, and a large organization read one at a time would hold
// the crawl for hours.
const setupWorkers = 8

// rememberConfigurations reads an account's configurations, reporting a failure rather than
// returning it: the listing it arrived with is worth more than the configurations.
func (o Options) rememberConfigurations(ctx context.Context, account string, one *AccountEnumeration) {
	if o.Configurations == nil {
		return
	}
	if err := o.Configurations(ctx, account); err != nil {
		one.ConfigurationsUnread = err.Error()
		return
	}
	one.ConfigurationsRead = true
}
