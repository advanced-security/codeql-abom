// Package checks compares what ABOM claims with independent readings, writing only its own tables.
package checks

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Check is one of the comparisons ABOM makes between what it stored and an independent reading.
type Check struct {
	Number  int    `json:"number"`
	Claim   string `json:"claim"`
	Reading string `json:"reading"`
	// BuiltBy names where a check this build does not run is planned: the plan, under plans/, and
	// the title of its item. Never an item number: a plan's items are renumbered as they land.
	BuiltBy string `json:"builtBy,omitempty"`
}

// Catalog lists every check there is, so one that is not running says so.
var Catalog = []Check{
	{1, "What a commit declared", "The same commit's files, read by an independent parser", ""},
	{2, "An owned repository's direct dependencies", "GitHub's dependency graph at the same commit",
		"api/dependency-graph-crosscheck.md: check 2, at a commit both name"},
	{3, "What a reference resolves to", "The reference now", ""},
	{4, "Which commits of an owned repository are live", "Its default and protected branch heads now", ""},
	{5, "A workflow's closure at a commit", "The actions that commit's run downloaded, with their SHAs",
		"api/dependency-graph-crosscheck.md: measure what run logs record, then check 5"},
	{6, "An owned repository's reach", "A CodeQL walk over its closure database",
		"ql/reachability-crosscheck.md: run it continuously, as check 6"},
	{7, "Incrementally maintained reach, exposure and advisory routes", "The wholesale recompute", ""},
	{8, "Exposure, closure and reach", "Each other, on every owned repository", ""},
	{9, "Graph-wide invariants", "None: each invariant holds two readings ABOM makes to each other", ""},
	{10, "The freshness each pool's estimate shows", "The lag checks 3 and 4 measured",
		"ui/crawl-visibility.md: check 10, from how long checks 3 and 4 stay open"},
	{11, "Work put back is done when claimed", "The ledger after the cycle that claimed it",
		"db/analysis-ledger.md: check 11, from the queue and the ledger alone"},
}

// External names the checks that read GitHub, which a run without a reader leaves off.
var External = []int{1, 3, 4}

// class is assigned by rule: 7 to 9 compare ABOM with itself, so only derivation can be at fault.
// The checks that read GitHub class each disagreement by how the queue holds what moved.
var class = map[int]string{7: "derivation", 8: "derivation", 9: "derivation"}

// GitHub is what the checks that read it ask: where references point now, what a repository
// protects, and what a commit's files say.
type GitHub interface {
	ResolveRefs(ctx context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error)
	ProtectedBranches(ctx context.Context, repository string) ([]string, error)
	Tree(ctx context.Context, repository, commit string) (catalog.Tree, error)
	CommitFiles(ctx context.Context, repository, commit string, paths []string) (catalog.FileBatch, error)
}

// Checker runs every built check once on copies of Store, filing each run into it.
type Checker struct {
	Store store.Backend
	Build string
	// Dir is where the copies are made.
	Dir string
	// Capacity is the checker's pool in worker-seconds, which every check draws on before it runs.
	Capacity *work.TokenPool
	// GitHub answers checks 1, 3 and 4; nil leaves them off. Its REST reads are charged by the client.
	GitHub GitHub
	// GraphQL is the checker's pool in GraphQL points, charged for every question put to GitHub.
	GraphQL *work.TokenPool
	// Recent is how many subjects that changed most recently a sampled check reads first, and
	// Sample how many it draws at random from each stratum.
	Recent, Sample int
	// Rand draws the random baselines; nil seeds one from the clock.
	Rand *rand.Rand
}

func (c *Checker) rng() *rand.Rand {
	if c.Rand == nil {
		c.Rand = rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), rand.Uint64()))
	}
	return c.Rand
}

// Run runs every built check once on copies of the store made under Dir, filing each run into it.
func (c *Checker) Run(ctx context.Context) (runs []store.CheckRun, result error) {
	src := c.Store
	if err := src.MigrateChecks(); err != nil {
		return nil, err
	}
	if err := c.Capacity.Wait(ctx); err != nil {
		return nil, err
	}
	started := time.Now()
	incremental, wholesale, cleanup, err := copies(src, c.Dir)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, cleanup()) }()

	file := func(check int, at time.Time, parts []store.CheckPart, err error) error {
		if err != nil {
			parts = []store.CheckPart{{Part: fmt.Sprintf("check %d", check),
				Skipped: []store.CheckSkip{{Reason: "could not be read: " + err.Error(), Count: 1}}}}
		}
		took := time.Since(at)
		c.Capacity.Spend(1, int(took.Round(time.Second)/time.Second))
		run := store.CheckRun{Check: check, Build: c.Build, Parts: settle(check, parts),
			StartedAt: at.UTC().Format(time.RFC3339), WallMillis: took.Milliseconds()}
		id, err := src.RecordCheckRun(run)
		if err != nil {
			return err
		}
		run.ID = id
		runs = append(runs, run)
		return nil
	}
	next := func() (time.Time, error) {
		if err := c.Capacity.Wait(ctx); err != nil {
			return time.Time{}, err
		}
		return time.Now(), nil
	}

	parts, derived := derivations(incremental, wholesale)
	if err := file(7, started, parts, derived); err != nil {
		return runs, err
	}
	for _, check := range []struct {
		number int
		read   func() ([]store.CheckPart, error)
	}{
		{8, func() ([]store.CheckPart, error) { return agreement(wholesale) }},
		{9, func() ([]store.CheckPart, error) { return invariants(wholesale) }},
	} {
		at, err := next()
		if err != nil {
			return runs, err
		}
		// 8 and 9 read the recompute, and a partial one would confound them.
		parts, err := []store.CheckPart(nil), derived
		if derived == nil {
			parts, err = check.read()
		}
		if err := file(check.number, at, parts, err); err != nil {
			return runs, err
		}
	}
	if c.GitHub == nil {
		return runs, nil
	}
	for _, check := range []struct {
		number int
		read   func(context.Context, store.Backend) ([]store.CheckPart, error)
	}{{1, c.declarations}, {3, c.references}, {4, c.liveCommits}} {
		at, err := next()
		if err != nil {
			return runs, err
		}
		// The queue is read from the copy, so the claims compared are the state the run began from.
		parts, err := check.read(ctx, incremental)
		if ctx.Err() != nil {
			return runs, ctx.Err()
		}
		if err := file(check.number, at, parts, err); err != nil {
			return runs, err
		}
	}
	return runs, nil
}

// copies takes the second copy from the first, so both start from one state whatever src commits.
func copies(src store.Backend, dir string) (store.Backend, store.Backend, func() error, error) {
	incremental, first, err := src.SnapshotStore(dir)
	if err != nil {
		return nil, nil, nil, err
	}
	wholesale, second, err := incremental.SnapshotStore(dir)
	if err != nil {
		return nil, nil, nil, errors.Join(err, first())
	}
	return incremental, wholesale, func() error { return errors.Join(second(), first()) }, nil
}

// settle counts and classes disagreements as the store will; nothing compared leaves no count.
func settle(check int, parts []store.CheckPart) []store.CheckPart {
	for i := range parts {
		p := &parts[i]
		if p.Skipped == nil {
			p.Skipped = []store.CheckSkip{}
		}
		if p.Compared > 0 || len(p.Disagreements) > 0 {
			n := len(p.Disagreements)
			p.Disagreed = &n
		}
		for j := range p.Disagreements {
			p.Disagreements[j].Check, p.Disagreements[j].Part = check, p.Part
			if c, ok := class[check]; ok {
				p.Disagreements[j].Class = c
			}
		}
	}
	return parts
}

// derivations is check 7: production's incremental path on one copy, its rebuild on the other.
func derivations(incremental, wholesale store.Backend) ([]store.CheckPart, error) {
	_, refused := (&graph.Builder{Store: incremental}).Derive()
	if _, err := (&graph.Builder{Store: wholesale}).Rebuild(); err != nil {
		return nil, fmt.Errorf("the wholesale recompute failed: %w", err)
	}
	oracle, err := wholesale.DerivedRows()
	if err != nil {
		return nil, err
	}
	if refused != nil {
		parts := make([]store.CheckPart, 0, len(oracle))
		for _, table := range oracle {
			subjects := map[string]bool{}
			for _, r := range table.Rows {
				subjects[r.Subject] = true
			}
			parts = append(parts, store.CheckPart{Part: table.Name, Skipped: []store.CheckSkip{{
				Reason: "the incremental path refused: " + refused.Error(), Count: len(subjects)}}})
		}
		return parts, nil
	}
	derived, err := incremental.DerivedRows()
	if err != nil {
		return nil, err
	}
	return graph.CompareDerived(derived, oracle), nil
}

// agreement is check 8: reach against the exposure walk, and against each owned closure.
func agreement(s store.Backend) ([]store.CheckPart, error) {
	walk, err := (&graph.Builder{Store: s}).ExposureWalkAgreement()
	if err != nil {
		return nil, err
	}
	owned, err := s.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	closure, err := graph.ClosureAgreement(s, owned)
	if err != nil {
		return nil, err
	}
	if len(owned) == 0 {
		for i := range closure {
			closure[i].Skipped = append(closure[i].Skipped, store.CheckSkip{
				Reason: "no repository is owned, so no closure was read", Count: 1})
		}
	}
	return append(walk, closure...), nil
}
