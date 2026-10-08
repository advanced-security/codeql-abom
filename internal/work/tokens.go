package work

import (
	"context"
	"sync"
	"time"
)

// Pools: one per kind of work, each in the unit its provider charges.
const (
	PoolEnumeration = "enumeration"
	PoolEvents      = "event follow-up"
	PoolPolling     = "reference polling"
	PoolFetching    = "fetching"
	PoolProbing     = "reference probing"
	PoolAdvisories  = "advisory reads"
	PoolLocks       = "lock verification"
	PoolConsistency = "consistency checker"
	PoolAnalysis    = "analysis"
	PoolCopilot     = "Copilot agents"
	// PoolDefaultSetup paces ABOM's one write to code scanning: adding actions to default setup.
	PoolDefaultSetup = "default setup"
)

// Units, in what each provider charges.
const (
	UnitREST          = "REST requests"
	UnitGraphQL       = "GraphQL points"
	UnitGit           = "git requests"
	UnitWorkerSeconds = "worker-seconds"
	UnitCopilot       = "Copilot credits"
)

// Allowances GitHub reports, named as its `x-ratelimit-resource` header names them.
const (
	ResourceCore    = "core"
	ResourceGraphQL = "graphql"
)

// PoolKind is one entry of the catalog.
type PoolKind struct {
	Pool string `json:"pool"`
	Unit string `json:"unit"`
	// Resource names the provider allowance it spends, empty where the provider reports none.
	Resource string `json:"resource,omitempty"`
	Work     string `json:"work"`
	// Unmetered says why nothing in this build draws on the pool; empty where something does.
	Unmetered string `json:"unmetered,omitempty"`
}

// PoolCatalog lists every pool, metered or not, in the order a reader meets them.
var PoolCatalog = []PoolKind{
	{Pool: PoolEnumeration, Unit: UnitREST, Resource: ResourceCore,
		Work: "Reading the accounts you declared: repositories, properties and protected branches."},
	{Pool: PoolEnumeration, Unit: UnitGraphQL, Resource: ResourceGraphQL,
		Work: "Reading the accounts you declared, a page of repositories per query."},
	{Pool: PoolEvents, Unit: UnitREST, Resource: ResourceCore,
		Work: "Following up an event from a repository you own: reading its protected branches, its code " +
			"scanning setup or database, and comparing a push's dependencies."},
	{Pool: PoolEvents, Unit: UnitGit,
		Work: "Asking where a branch an event names points now, before acting on it."},
	{Pool: PoolPolling, Unit: UnitGraphQL, Resource: ResourceGraphQL,
		Work: "Asking whether an external reference moved, a hundred to a query, most used first."},
	{Pool: PoolFetching, Unit: UnitGit, Work: "Looking up and cloning the reference a dependency names."},
	{Pool: PoolFetching, Unit: UnitREST, Resource: ResourceCore,
		Work: "Reading a commit's file listing instead of cloning it."},
	{Pool: PoolProbing, Unit: UnitGit,
		Work: "The recheck sweep: asking whether an analyzed reference has moved."},
	{Pool: PoolAdvisories, Unit: UnitREST, Resource: ResourceCore,
		Work: "Reading the security advisories for the actions the graph names."},
	{Pool: PoolLocks, Unit: UnitGit,
		Work: "Fetching the commit a lock verification runs on. The verification tool's own requests are not counted."},
	{Pool: PoolConsistency, Unit: UnitWorkerSeconds,
		Work: "Running the checks: recomputing what the graph claims, and comparing it with what it holds and with GitHub."},
	{Pool: PoolConsistency, Unit: UnitREST, Resource: ResourceCore,
		Work: "Reading which branches the owned repositories a check samples protect."},
	{Pool: PoolConsistency, Unit: UnitGraphQL, Resource: ResourceGraphQL,
		Work: "Asking where the references and branches a check samples point now, a hundred to a query."},
	{Pool: PoolAnalysis, Unit: UnitWorkerSeconds,
		Work: "A worker preparing one repository: fetching, building its database and running queries."},
	{Pool: PoolDefaultSetup, Unit: UnitREST, Resource: ResourceCore,
		Work: "Adding actions to default setup where an approved preview listed the repository: one write " +
			"each, with the operator's own token. The reads that check each write go through the App and enumeration."},
	{Pool: PoolCopilot, Unit: UnitCopilot,
		Work:      "Agents writing queries for you.",
		Unmetered: "Not metered yet: Copilot does not report to this build what a session charged."},
}

// Reading is what a provider last reported was left of an allowance.
type Reading struct {
	// Known separates an allowance nobody reported from one reported spent.
	Known     bool
	Remaining int
	Limit     int
	ResetAt   time.Time
}

// Spend is what one pool spent in one minute, with what the provider last reported in it.
type Spend struct {
	Pool   string
	Unit   string
	Bucket time.Time
	// Requests counts round trips; Charged is what the provider charged for them, which a
	// conditional read answered unchanged makes smaller and a GraphQL query makes larger.
	Requests int
	Charged  int
	Reading  Reading
}

// Ceiling is a provider's allowance, shared by every pool that spends it and by anybody else
// holding the same token, so it is read from answers and never assumed to be ours alone.
type Ceiling struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error

	mu      sync.Mutex
	reading Reading
}

// Observe keeps the latest reading.
func (c *Ceiling) Observe(r Reading) {
	if c == nil || !r.Known {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reading = r
}

func (c *Ceiling) last() Reading {
	if c == nil {
		return Reading{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reading
}

// exhausted reports the reset of an allowance reported spent. One nobody reported is not spent,
// and one whose reset has passed is assumed refilled until an answer says otherwise.
func (c *Ceiling) exhausted() (time.Time, bool) {
	r := c.last()
	if !r.Known || r.Remaining > 0 || r.ResetAt.IsZero() || !r.ResetAt.After(c.now()) {
		return time.Time{}, false
	}
	return r.ResetAt, true
}

// TokenPool is what one kind of work may spend of one unit: the operator's rate and burst, the
// provider's allowance above it, and what was spent.
type TokenPool struct {
	Kind    PoolKind
	limiter *Limiter
	ceiling *Ceiling
	now     func() time.Time

	mu      sync.Mutex
	perHour int
	burst   int
	pending map[time.Time]*Spend
}

// Configure sets the operator's rate per hour and burst. A rate of zero or less removes it.
func (p *TokenPool) Configure(perHour, burst int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.perHour, p.burst = max(perHour, 0), max(burst, 1)
	p.mu.Unlock()
	p.limiter.Configure(perHour, burst)
}

// Rate reports the operator's rate and burst; a rate of zero means none is set.
func (p *TokenPool) Rate() (perHour, burst int) {
	if p == nil {
		return 0, 1
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.perHour, max(p.burst, 1)
}

// Wait holds a caller while the provider's allowance is reported spent, then for the pool's rate.
// A pool with room, and an allowance nobody reported, hold nobody.
func (p *TokenPool) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil || p == nil {
		return err
	}
	if reset, spent := p.ceiling.exhausted(); spent {
		if err := p.ceiling.sleep(ctx, reset.Sub(p.ceiling.now())); err != nil {
			return err
		}
	}
	return p.limiter.Wait(ctx)
}

// Take waits for one unit and spends it, for a provider that charges one per request and reports
// nothing.
func (p *TokenPool) Take(ctx context.Context) error {
	if err := p.Wait(ctx); err != nil {
		return err
	}
	p.Spend(1, 1)
	return nil
}

// Spend records what the provider charged for requests. A charge above the one unit each request's
// wait reserved is owed by the requests after it.
func (p *TokenPool) Spend(requests, charged int) {
	if p == nil || (requests <= 0 && charged <= 0) {
		return
	}
	p.limiter.Charge(charged - requests)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.bucket()
	s.Requests += requests
	s.Charged += charged
}

// Observe records what the provider reported on an answer this pool received.
func (p *TokenPool) Observe(r Reading) {
	if p == nil || !r.Known {
		return
	}
	p.ceiling.Observe(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bucket().Reading = r
}

// Reading is what the provider last reported to any pool spending the same allowance.
func (p *TokenPool) Reading() Reading {
	if p == nil {
		return Reading{}
	}
	return p.ceiling.last()
}

// Exhausted reports when the allowance resets, if it is reported spent.
func (p *TokenPool) Exhausted() (time.Time, bool) {
	if p == nil || p.ceiling == nil {
		return time.Time{}, false
	}
	return p.ceiling.exhausted()
}

// bucket is the current minute's row; the caller holds p.mu.
func (p *TokenPool) bucket() *Spend {
	at := p.now().UTC().Truncate(time.Minute)
	if p.pending == nil {
		p.pending = map[time.Time]*Spend{}
	}
	s, ok := p.pending[at]
	if !ok {
		s = &Spend{Pool: p.Kind.Pool, Unit: p.Kind.Unit, Bucket: at}
		p.pending[at] = s
	}
	return s
}

func (p *TokenPool) drain() []Spend {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Spend, 0, len(p.pending))
	for _, s := range p.pending {
		out = append(out, *s)
	}
	p.pending = nil
	return out
}

// TokenPools holds every pool one process draws on, and the provider allowances they share.
type TokenPools struct {
	pools []*TokenPool
}

// NewTokenPools builds a pool for every catalog entry, none with a rate.
func NewTokenPools() *TokenPools {
	ceilings := map[string]*Ceiling{}
	ps := &TokenPools{}
	for _, k := range PoolCatalog {
		p := &TokenPool{Kind: k, limiter: NewLimiter(0), now: time.Now}
		if k.Resource != "" {
			if ceilings[k.Resource] == nil {
				ceilings[k.Resource] = &Ceiling{now: time.Now, sleep: sleepContext}
			}
			p.ceiling = ceilings[k.Resource]
		}
		ps.pools = append(ps.pools, p)
	}
	return ps
}

// Pool returns the pool for a kind and unit, or nil, which spends without limit or record.
func (ps *TokenPools) Pool(pool, unit string) *TokenPool {
	if ps == nil {
		return nil
	}
	for _, p := range ps.pools {
		if p.Kind.Pool == pool && p.Kind.Unit == unit {
			return p
		}
	}
	return nil
}

// All lists the pools in catalog order.
func (ps *TokenPools) All() []*TokenPool {
	if ps == nil {
		return nil
	}
	return ps.pools
}

// Drain hands over what was spent since the last drain, so it is recorded exactly once.
func (ps *TokenPools) Drain() []Spend {
	out := []Spend{}
	for _, p := range ps.All() {
		out = append(out, p.drain()...)
	}
	return out
}

// Restore puts back spend a recorder could not write, so a failed write delays it instead of
// losing it.
func (ps *TokenPools) Restore(rows []Spend) {
	for _, r := range rows {
		p := ps.Pool(r.Pool, r.Unit)
		if p == nil {
			continue
		}
		p.mu.Lock()
		if p.pending == nil {
			p.pending = map[time.Time]*Spend{}
		}
		s, ok := p.pending[r.Bucket]
		if !ok {
			s = &Spend{Pool: r.Pool, Unit: r.Unit, Bucket: r.Bucket}
			p.pending[r.Bucket] = s
		}
		s.Requests += r.Requests
		s.Charged += r.Charged
		if !s.Reading.Known {
			s.Reading = r.Reading
		}
		p.mu.Unlock()
	}
}
