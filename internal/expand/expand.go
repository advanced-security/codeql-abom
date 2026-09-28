package expand

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Fetcher materializes a repository's source tree.
type Fetcher interface {
	Fetch(ctx context.Context, dest, owner, repo, ref string) (Tree, error)
}

// SparseFilter reports the paths a fetcher takes, for a fetcher that takes only some of them.
//
// Part of a cached tree's identity, because a tree fetched under one filter cannot answer a
// request under a wider one: the files are simply not there, and nothing downstream can tell
// that apart from a repository that does not contain them.
type SparseFilter interface {
	SparsePatterns() []string
}

// Builder creates a CodeQL database from a source tree.
type Builder interface {
	CreateDatabase(ctx context.Context, srcDir, dbDir, language string) error
}

// QueryRunner evaluates queries against a database.
//
// The whole set at once rather than one at a time: the cost of a query is dominated by the
// process around it, not by the evaluation, so a lane of three costs three times the setup and
// once the answer.
type QueryRunner interface {
	// RunQueries returns the BQRS each query wrote, keyed by the query path asked for.
	RunQueries(ctx context.Context, dbPath string, queryPaths []string) (map[string]string, error)
}

// Analyzer evaluates findings-shaped queries against a database, writing SARIF.
//
// This is the second lane. `database analyze` is the only way to run a `@kind problem` query,
// and the rule id it reports is the only thing in ABOM that carries a security verdict.
type Analyzer interface {
	AnalyzeQueries(ctx context.Context, dbPath string, targets []string, sarifPath string) error
}

// Decoder converts BQRS into the JSON the ingester consumes.
type Decoder func(ctx context.Context, bqrsPath, jsonPath string) error

// Options configures one expansion round.
type Options struct {
	QueryPath string
	QueryID   string
	// Suite names a query suite to run instead of QueryPath. The set of queries ABOM runs is
	// meant to grow, so what runs is read from the suite at run time rather than compiled in.
	Suite   string
	WorkDir string
	// Limit caps how many queued dependencies a round claims.
	Limit       int
	Concurrency int
	// KeepArtifacts retains checkouts and databases for debugging.
	KeepArtifacts bool
}

// Stats summarizes a round.
type Stats struct {
	// Processed counts analyses, not repositories: a repository reached at three references is
	// three databases and three sets of results.
	Processed int
	// Skipped counts repositories the ledger already had on record, settled without analysis.
	Skipped     int
	Items       int
	Failed      int
	Unavailable int
	Discovered  int
	Edges       int
	// Reused counts trees served from the checkout cache: a clone this round did not pay for.
	// Not a skip — every one of these was analyzed or settled on the ledger's say-so.
	Reused int
	// Fetched counts the clones this round did pay for. Measured at the clone rather than
	// inferred from Processed, which counts neither the fetch a settled unit still made nor the
	// one a cache hit avoided.
	Fetched int
}

// Expander turns queued dependencies into graph data and queues what they reveal.
//
// Work is claimed and settled per repository rather than per dependency, because the clone and
// the database build are the expensive steps and several actions often share one repository.
type Expander struct {
	Queue    *frontier.Queue
	Store    *store.Store
	Ingester *ingest.Ingester
	Writer   *store.Writer
	Fetcher  Fetcher
	// Probe resolves a reference without fetching it, so the commit that decides whether any
	// work is outstanding is known before a clone is paid for. Nil fetches first, as before.
	Probe   RefProbe
	Builder Builder
	CodeQL  QueryRunner
	Decode  Decoder
	// Cache retains fetched trees by the commit they came from, across references and rounds.
	// Nil fetches every reference separately and deletes the tree after each unit, as before.
	Cache *CheckoutCache
	// Databases retains the CodeQL databases this crawl builds, so a query can later be run
	// across a corpus that already exists. Nil deletes each database after its unit, as before.
	Databases *DatabaseCache
	// Resolver expands Options.Suite. Only needed when a suite is used.
	Resolver Resolver
	// Analyzer runs the findings lane. Only needed when a suite holds findings-shaped queries.
	Analyzer Analyzer
	// Pace caps the rate of outbound requests across workers; nil means unlimited.
	Pace    *work.Limiter
	OnEvent func(work.Event)
	// Install installs what the operator's pack depends on wherever it has no lock, for a round
	// over a suite drawn from it: a tier can be bound to that suite before anybody has pressed
	// Test. Nil installs nothing.
	Install func(context.Context) error

	// digests remembers each pack's content hash for the life of a round. A pack cannot change
	// while the round that resolved it is running.
	digests runner.DigestCache

	// fetches counts clones across every worker, so a round can report what it spent.
	fetches atomic.Int64
}

// batch is the set of queued dependencies answered by analyzing one repository at one reference.
type batch struct {
	owner string
	repo  string
	// ref is what gets checked out. Two references of one repository are two batches: they are
	// different trees, and analyzing them together would produce one answer for both.
	ref   string
	items []frontier.Item
	// depth is the shallowest depth among the items, and what discoveries are measured from.
	depth int
}

// revision is the exact tree a round's results describe, and what every ledger row is attributed
// to. The requested ref and the one actually analyzed are both kept: a moving ref resolves to a
// commit, and losing which ref was asked for makes a re-analysis look like a different dependency.
type revision struct {
	repository string
	commit     string
	ref        string
	analyzed   string
	// filter is the checkout filter the tree was fetched under. A commit names a repository's
	// state; only the commit and the filter together name the files the extractor saw.
	filter string
}

func (b batch) revision(p work.Prepared, filter string) revision {
	return revision{
		repository: b.full(), commit: p.Commit, ref: b.ref, analyzed: p.AnalyzedRef,
		filter: filter,
	}
}

func (b batch) full() string { return b.owner + "/" + b.repo }

// database names the CodeQL database a round's results came from.
//
// The same four fields the run key hashes, deliberately: one definition of "which database", or
// the cache and the ledger drift into disagreeing about what has already been analyzed.
func (at revision) database() store.DatabaseIdentity {
	return store.DatabaseIdentity{
		Repository:   at.repository,
		Commit:       at.commit,
		SourceFilter: at.filter,
		Mode:         store.AnalysisModeRegular,
	}
}

// key distinguishes work; full() names the repository. A batch is claimed, directoried and
// consumed by key, while the ledger and the graph are keyed by the repository.
func (b batch) key() string {
	if b.ref == "" {
		return b.full()
	}
	return b.full() + "@" + b.ref
}

// Run claims a round of work, analyzes it, and enqueues what it discovers.
func (e *Expander) Run(ctx context.Context, opts Options) (Stats, error) {
	if opts.WorkDir == "" {
		return Stats{}, fmt.Errorf("expand: WorkDir is required")
	}

	e.trimBetweenRounds()
	before := e.roundCounts()

	plans, err := e.checkedPlans(ctx, opts)
	if err != nil {
		return Stats{}, err
	}

	claimed, err := e.Queue.Claim(opts.Limit)
	if err != nil {
		return Stats{}, err
	}
	if len(claimed) == 0 {
		return Stats{}, nil
	}
	batches := groupByRepository(claimed)

	// Read once for the round: ownership decides a batch's tier and cannot change mid-round.
	owned, err := e.Store.OwnedSet()
	if err != nil {
		return Stats{}, err
	}

	var stats Stats
	jobs, byRepo := jobsForBatches(batches)
	pool := e.pool(opts, plans, owned, byRepo, &stats)

	poolStats, err := pool.Run(ctx, jobs)
	if err != nil {
		return stats, err
	}
	stats.Processed = poolStats.Processed - stats.Skipped
	stats.Edges = poolStats.Rows
	stats.Reused = int(e.Cache.Hits() - before.reused)
	stats.Fetched = int(e.fetches.Load() - before.fetched)
	return stats, nil
}

type expanderCounts struct {
	reused  int64
	fetched int64
}

func (e *Expander) trimBetweenRounds() {
	// Between rounds, where nothing is reading a cached tree. A round evicting an entry a unit
	// is analyzing would take the source out from under a database build.
	if _, err := e.Cache.Trim(); err != nil {
		// The crawl is what matters; a cache that cannot be tidied costs disk, not results.
		e.report("checkout-cache", "trimming the checkout cache", err)
	}
	if _, err := e.trimDatabases(); err != nil {
		e.report("database-cache", "trimming the database cache", err)
	}
}

func (e *Expander) roundCounts() expanderCounts {
	return expanderCounts{reused: e.Cache.Hits(), fetched: e.fetches.Load()}
}

func (e *Expander) checkedPlans(ctx context.Context, opts Options) (map[string]tierPlan, error) {
	plans, err := e.plansByTier(ctx, opts)
	if err != nil {
		return nil, err
	}
	// A binding that names findings queries with nothing able to run them is the defect this
	// lane exists to fix, so it fails loudly here rather than quietly producing no findings.
	for tier, p := range plans {
		if len(p.findings) > 0 && e.Analyzer == nil {
			return nil, fmt.Errorf(
				"expand: %s is bound to %d findings queries but no Analyzer is configured",
				tier, len(p.findings))
		}
	}
	return plans, nil
}

func jobsForBatches(batches []batch) ([]work.Job, map[string]batch) {
	jobs := make([]work.Job, 0, len(batches))
	byRepo := make(map[string]batch, len(batches))
	for _, b := range batches {
		jobs = append(jobs, work.Job{Repository: b.key()})
		byRepo[b.key()] = b
	}
	return jobs, byRepo
}

func (e *Expander) pool(
	opts Options, plans map[string]tierPlan, owned map[string]bool, byRepo map[string]batch, stats *Stats,
) *work.Pool {
	return &work.Pool{
		Concurrency: opts.Concurrency,
		Prepare: func(ctx context.Context, job work.Job) (work.Prepared, error) {
			b, err := batchFor(byRepo, job.Repository)
			if err != nil {
				return work.Prepared{}, err
			}
			return e.prepare(ctx, b, opts, plans[tierOf(b, owned)])
		},
		Consume: func(ctx context.Context, p work.Prepared) (int, error) {
			b, err := batchFor(byRepo, p.Job.Repository)
			if err != nil {
				return 0, err
			}
			return e.consume(ctx, b, p, opts, plans[tierOf(b, owned)], stats)
		},
		OnEvent: func(ev work.Event) {
			if ev.Kind == work.EventFailed {
				e.settleFailure(byRepo[ev.Job.Repository], ev.Err, stats)
			}
			if e.OnEvent != nil {
				e.OnEvent(ev)
			}
		},
	}
}

// batchFor resolves the batch a job names.
//
// A miss used to yield the zero batch, which holds no items and calls itself "/": analysis ran,
// was filed under that name, and the dependencies it answered were never taken out of the queue.
// Failing here instead means the round reports the problem rather than silently losing work.
func batchFor(byRepo map[string]batch, key string) (batch, error) {
	b, ok := byRepo[key]
	if !ok {
		return batch{}, fmt.Errorf("expand: no batch claimed for %q", key)
	}
	return b, nil
}

// groupByRepository collapses claimed dependencies onto the repositories that answer them.
func groupByRepository(items []frontier.Item) []batch {
	order := []string{}
	seen := map[string]*batch{}
	for _, it := range items {
		if it.Owner == "" || it.Repo == "" {
			continue
		}
		key := it.Owner + "/" + it.Repo + "@" + it.Ref
		b, ok := seen[key]
		if !ok {
			b = &batch{owner: it.Owner, repo: it.Repo, ref: it.Ref, depth: it.Depth}
			seen[key] = b
			order = append(order, key)
		}
		if it.Depth < b.depth {
			b.depth = it.Depth
		}
		b.items = append(b.items, it)
	}

	out := make([]batch, 0, len(order))
	for _, key := range order {
		out = append(out, *seen[key])
	}
	return out
}

// pending is the part of a plan that is not yet on record for one database.
type pending struct {
	bqrs     []plannedQuery
	findings bool
}

func (p pending) empty() bool { return len(p.bqrs) == 0 && !p.findings }

// pendingWork reports which lanes still have something to prove against this exact database.
//
// Per lane rather than per repository, because the lanes come from different packs with different
// digests and nothing about running one requires running the other. On a real repository the
// database build is 13% of the cost and the two lanes are 87%, so re-running the lane that did
// not change is most of the bill for a re-analysis.
//
// An unknown commit leaves everything outstanding: an empty one would collide with every other
// unknown, so it can never be treated as a match.
//
// The digest is the same argument one level down. A release moves a tag over a rebuilt `dist/`
// and touches nothing the extractor reads, so the commit is new but the database is not; where
// the content is known to match, an earlier commit's run answers for this one. Every other part
// of the run identity still has to match, and nothing is written for the new commit — the run
// stays recorded where it happened.
func (e *Expander) pendingWork(
	planned tierPlan, repository, commit, digest string,
) (pending, error) {
	if commit == "" {
		return pending{bqrs: planned.bqrs, findings: len(planned.findings) > 0}, nil
	}

	done := func(q store.QueryRun) (bool, error) {
		ran, err := e.Store.QueryRunExists(q)
		if err != nil || ran {
			return ran, err
		}
		return e.Store.QueryRunExistsForContent(q, digest)
	}

	var out pending
	// No ref: this asks whether the evaluation is on record, and the ref is not part of a run's
	// identity. Putting one here would make the skip miss whenever a second reference reached
	// content already analyzed, which is the case it exists for.
	at := revision{repository: repository, commit: commit, filter: e.sourceFilter()}
	for _, q := range planned.bqrs {
		ran, err := done(planned.queryRun(q.id, at))
		if err != nil {
			return pending{}, err
		}
		if !ran {
			out.bqrs = append(out.bqrs, q)
		}
	}
	if len(planned.findings) > 0 {
		ran, err := done(planned.findingsRun(at))
		if err != nil {
			return pending{}, err
		}
		out.findings = !ran
	}
	return out, nil
}

// analyzedRef names the reference a checkout produced. Shared with the skip path, which settles
// without a checkout and still has to describe the reference the same way.
func analyzedRef(ref string) string {
	if ref == "" {
		return "HEAD"
	}
	return ref
}

// refSuffix names a reference for a directory. Refs contain slashes and dots — `refs/heads/main`,
// `v4.2.2` — so they cannot go into a path unaltered without escaping the work directory.
func refSuffix(ref string) string {
	if ref == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("@")
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func (e *Expander) prepare(
	ctx context.Context, b batch, opts Options, planned tierPlan,
) (work.Prepared, error) {
	// One directory per reference: two refs of a repository are two trees, and sharing a path
	// would have the second overwrite the first mid-round.
	dir := filepath.Join(opts.WorkDir, b.owner+"_"+b.repo+refSuffix(b.ref))
	src := filepath.Join(dir, "src")

	// Resolved once and carried, because the commit decides both whether anything is
	// outstanding and which tree answers the fetch.
	sha, resolved := e.commitWithoutFetching(ctx, b)
	settled, ok, err := e.settleWithoutFetching(b, planned, sha, resolved)
	if err != nil {
		return work.Prepared{}, err
	}
	if ok {
		return settled, nil
	}

	tree, retained, err := e.materialize(ctx, b, src, sha, resolved)
	if err != nil {
		return work.Prepared{}, err
	}

	// What the extractor would read, recorded whether or not this round goes on to analyze it:
	// the value of knowing it is that a later commit can be recognized as the same database.
	digest, err := IndexedDigest(tree.Dir)
	if err != nil {
		return work.Prepared{}, err
	}
	digest = scopedDigest(e.sourceFilter(), digest)
	if digest != "" && tree.SHA != "" {
		if err := e.Store.RecordCommitContent(b.full(), tree.SHA, digest); err != nil {
			return work.Prepared{}, err
		}
	}

	// Read from the tree at the commit being analyzed, never from the repository's current state:
	// a lock describes the revision it was committed with. Discovery reads what it selects when
	// this batch settles, so it has to be on record before then.
	if tree.SHA != "" {
		if err := e.recordLockCoverage(b.full(), tree); err != nil {
			return work.Prepared{}, err
		}
	}

	// Asked of every batch that reaches here, including one whose tree came from the cache: the
	// bytes being on disk already says nothing about whether this pair has been analyzed.
	outstanding, err := e.pendingWork(planned, b.full(), tree.SHA, digest)
	if err != nil {
		return work.Prepared{}, err
	}
	if outstanding.empty() {
		discardTree(opts, tree, retained)
		return work.Prepared{
			Job:             work.Job{Repository: b.key()},
			Commit:          tree.SHA,
			AnalyzedRef:     tree.Ref,
			AlreadyRecorded: true,
		}, nil
	}

	return e.analyzeTree(ctx, b, opts, planned, dir, tree, retained, outstanding)
}

// materialize puts the batch's source on disk, reusing the tree already held for its commit.
//
// The commit is the identity, so a reference the cache has never seen still hits when it
// resolves to a commit that is held; and a reference that has moved misses, because it now
// names a different commit.
func (e *Expander) materialize(
	ctx context.Context, b batch, src, sha string, resolved bool,
) (Tree, bool, error) {
	if e.Cache == nil {
		tree, err := e.fetch(ctx, b, src)
		return tree, false, err
	}
	patterns := sparsePatternsOf(e.Fetcher)
	if resolved {
		// One clone per commit rather than one per reference: two references that resolve to
		// the same commit arrive here together, and the second waits for the first's fetch
		// instead of racing it.
		release := e.Cache.Hold(b.owner, b.repo, sha)
		if held, ok := e.Cache.Reuse(b.owner, b.repo, sha, patterns); ok {
			release()
			return Tree{Dir: held, Ref: analyzedRef(b.ref), SHA: sha}, true, nil
		}
		// The lock is carried into the fetch rather than released here, because promoting what
		// it clones is the other half of the work this commit is held for.
		return e.fetchAndRetain(ctx, b, src, patterns, sha, release)
	}
	return e.fetchAndRetain(ctx, b, src, patterns, "", nil)
}

// fetchAndRetain clones into staging and promotes the tree once its commit is known.
//
// Staged first because the fetch is what answers "which commit is this" for a reference nothing
// could resolve, and a tree under a commit's name has to be the whole tree. holding names the
// commit whose lock the caller already took, if any, and release frees it.
func (e *Expander) fetchAndRetain(
	ctx context.Context, b batch, src string, patterns []string, holding string, release func(),
) (Tree, bool, error) {
	// Whatever lock is held when this returns is the one released: the commit it is taken for
	// can change once the clone says which commit it actually is.
	defer func() {
		if release != nil {
			release()
		}
	}()
	staged, err := e.Cache.Stage()
	if err != nil {
		// A cache that cannot stage costs a re-clone next round, not this round's work.
		tree, err := e.fetch(ctx, b, src)
		return tree, false, err
	}
	// Owned from here: promotion renames this directory away, and every other way out has to
	// leave nothing behind. A staged fetch nothing promoted keeps a whole checkout, `.git` and
	// all, until the abandonment sweep a day later.
	defer os.RemoveAll(staged)

	tree, err := e.fetch(ctx, b, stagedTree(staged))
	if err != nil {
		return Tree{}, false, err
	}

	// Which commit this is may only be known now: a reference nothing could resolve has no lock
	// yet, and one that moved between the probe and the clone holds the wrong commit's. Two
	// promotions of one commit running together can each find no entry, and the second replaces
	// the tree the first is already analyzing.
	if !strings.EqualFold(tree.SHA, holding) {
		if release != nil {
			// Released before the next is taken: a unit waiting on two commit locks at once can
			// wait on one another unit already holds while holding the one it wants.
			release()
		}
		release = e.Cache.Hold(b.owner, b.repo, tree.SHA)
		if held, ok := e.Cache.Reuse(b.owner, b.repo, tree.SHA, patterns); ok {
			tree.Dir = held
			return tree, true, nil
		}
	}

	held, err := e.Cache.Retain(staged, b.owner, b.repo, tree.SHA, patterns)
	if err != nil {
		// The clone succeeded, so the round proceeds from its own directory rather than paying
		// for the tree again; staging is not somewhere a unit can be left reading from.
		e.report(b.key(), "retaining a fetched tree", err)
		return e.moveOutOfStaging(ctx, b, staged, src, tree)
	}
	tree.Dir = held
	return tree, true, nil
}

// moveOutOfStaging puts a tree a promotion refused into the round's own directory, so the unit
// analyzes it where the round's cleanup can find it.
func (e *Expander) moveOutOfStaging(
	ctx context.Context, b batch, staged, src string, tree Tree,
) (Tree, bool, error) {
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		return Tree{}, false, fmt.Errorf("preparing %s: %w", filepath.Dir(src), err)
	}
	if err := os.RemoveAll(src); err != nil {
		return Tree{}, false, fmt.Errorf("clearing %s: %w", src, err)
	}
	if err := os.Rename(stagedTree(staged), src); err != nil {
		// A cache on another filesystem cannot be renamed out of, and a copy of a checkout is
		// no cheaper than the clone that produced it.
		fetched, err := e.fetch(ctx, b, src)
		return fetched, false, err
	}
	tree.Dir = src
	return tree, false, nil
}

// fetch clones one reference, paying the rate limit that a cache hit does not.
func (e *Expander) fetch(ctx context.Context, b batch, dest string) (Tree, error) {
	if err := e.Pace.Wait(ctx); err != nil {
		return Tree{}, err
	}
	e.fetches.Add(1)
	return e.Fetcher.Fetch(ctx, dest, b.owner, b.repo, b.ref)
}

// discardTree removes a checkout the cache is not holding.
//
// A retained tree outlives the unit that fetched it, which is the point of retaining it.
// KeepArtifacts is a separate question about the database and the results this round produced.
func discardTree(opts Options, tree Tree, retained bool) {
	if retained || opts.KeepArtifacts || tree.Dir == "" {
		return
	}
	os.RemoveAll(tree.Dir)
}

// sparsePatternsOf reports the filter a fetcher applies, or none when it does not narrow the
// tree. Part of a cached tree's identity: a tree fetched under narrower patterns is missing
// files, and downstream that is indistinguishable from a repository that does not have them.
func sparsePatternsOf(f Fetcher) []string {
	filter, ok := f.(SparseFilter)
	if !ok {
		return nil
	}
	return filter.SparsePatterns()
}

// filterID names a pattern set in something short enough to be a path segment and stable enough
// to be an identity.
//
// Fetching nothing in particular is a filter too, with an id of its own: "no patterns" is what a
// whole-repository fetch means, and it has to be told apart from a narrowed one rather than read
// as an absence that matches anything.
func filterID(patterns []string) string {
	h := sha256.New()
	for _, p := range patterns {
		// Length-prefixed, so no two different pattern sets can concatenate to the same bytes.
		fmt.Fprintf(h, "%d:%s\x00", len(p), p)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// sourceFilter names the filter this round fetches under, for the identities that have to move
// when it does.
func (e *Expander) sourceFilter() string { return filterID(sparsePatternsOf(e.Fetcher)) }

// scopedDigest labels a content hash with the filter that produced it.
//
// The hash covers the files that were fetched, and says nothing about the ones that were not, so
// two filters over one commit can hash the same tree and mean different things. Labeling makes
// the stored value self-describing: a reader asking under a different filter sees a digest that
// is not its own instead of a match it did not earn.
func scopedDigest(filter, digest string) string {
	if digest == "" {
		return ""
	}
	return filter + ":" + digest
}

// digestUnderFilter reports a recorded digest if this round's filter is the one that produced it,
// and nothing otherwise. Nothing is the truthful answer: the content under the current filter has
// not been measured at that commit, and an unmeasured tree must not settle anything.
func digestUnderFilter(recorded, filter string) string {
	if recorded == "" || !strings.HasPrefix(recorded, filter+":") {
		return ""
	}
	return recorded
}

// settleWithoutFetching answers a batch that needs no checkout, reporting whether it did.
//
// Before the clone, not after: the ledger only needs the commit, and a reference resolves
// remotely for a fraction of what fetching it costs. A tier requeue puts back everything
// including what is already current, and those are settled here without a checkout.
func (e *Expander) settleWithoutFetching(
	b batch, planned tierPlan, sha string, resolved bool,
) (work.Prepared, bool, error) {
	if !resolved {
		return work.Prepared{}, false, nil
	}
	// A commit seen before has its content on record, so the equivalence is available here
	// too. A commit never seen has none, and an absent digest matches nothing. Neither has one
	// recorded under a filter this round no longer uses: the files it did not fetch are exactly
	// what settling here would decline to look at.
	recorded, _, err := e.Store.CommitContentDigest(b.full(), sha)
	if err != nil {
		return work.Prepared{}, false, err
	}
	digest := digestUnderFilter(recorded, e.sourceFilter())
	outstanding, err := e.pendingWork(planned, b.full(), sha, digest)
	if err != nil {
		return work.Prepared{}, false, err
	}
	if !outstanding.empty() {
		return work.Prepared{}, false, nil
	}
	// Lock coverage is read from a checkout, so settling here is the one path that can skip it
	// indefinitely: a database whose analyzed refs have no pending work never fetches again, and
	// an upgrade or a tier requeue is exactly that case. Absence of rows means nobody looked,
	// which is a different fact from "no lockfile", so the commit is read once to say which.
	covered, err := e.Store.HasLockCoverage(b.full(), sha, lockfile.Reader)
	if err != nil {
		return work.Prepared{}, false, err
	}
	if !covered {
		return work.Prepared{}, false, nil
	}
	return work.Prepared{
		Job:             work.Job{Repository: b.key()},
		Commit:          sha,
		AnalyzedRef:     analyzedRef(b.ref),
		AlreadyRecorded: true,
	}, true, nil
}

// analyzeTree builds the database for a checkout and runs everything the tier still wants.
func (e *Expander) analyzeTree(
	ctx context.Context, b batch, opts Options, planned tierPlan,
	dir string, tree Tree, retained bool, outstanding pending,
) (work.Prepared, error) {
	dbDir := filepath.Join(dir, "db")
	// A tree served from the cache means nothing has created the round's directory, and the
	// results written below it are what the serialized phase reads.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return work.Prepared{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	id := store.DatabaseIdentity{
		Repository:   b.full(),
		Commit:       tree.SHA,
		SourceFilter: e.sourceFilter(),
		Mode:         store.AnalysisModeRegular,
	}
	dbDir, staged, reused, err := e.databaseFor(ctx, id, dbDir, tree, planned.cliVersion)
	if err != nil {
		return work.Prepared{}, err
	}

	jsonPath, edgesID, extra, err := e.runQueries(ctx, dbDir, dir, outstanding.bqrs)
	if err != nil {
		return work.Prepared{}, err
	}

	// The findings lane runs against the same database, before it is removed below.
	var sarifPath string
	if outstanding.findings {
		sarifPath = filepath.Join(dir, "findings.sarif")
		if err := e.Analyzer.AnalyzeQueries(ctx, dbDir, planned.findings, sarifPath); err != nil {
			return work.Prepared{}, err
		}
	}

	// The checkout and database are the bulk of the disk cost and are finished with here, so
	// they go before the serialized phase rather than after it.
	discardTree(opts, tree, retained)
	held, bytes, err := e.keepDatabase(staged, dbDir, id, opts, reused)
	if err != nil {
		return work.Prepared{}, err
	}
	return work.Prepared{
		Job:            work.Job{Repository: b.key()},
		ResultPath:     jsonPath,
		ResultQueryID:  edgesID,
		SarifPath:      sarifPath,
		Extra:          extra,
		Commit:         tree.SHA,
		AnalyzedRef:    tree.Ref,
		DatabasePath:   held,
		DatabaseBytes:  bytes,
		DatabaseReused: reused,
	}, nil
}

// databaseFor produces the database to analyze, reusing a retained one when the cache already
// holds this exact identity.
//
// Reuse skips extraction, never analysis: whether a query still has to run is the ledger's call,
// and a database being on disk says nothing about it.
func (e *Expander) databaseFor(
	ctx context.Context, id store.DatabaseIdentity, roundDB string, tree Tree, cli string,
) (string, string, bool, error) {
	if held, ok := e.Databases.Reuse(id, cli); ok {
		return held, "", true, nil
	}
	// Built inside the cache when there is one, so promoting it is a rename rather than a copy
	// across filesystems.
	staged, dbDir := "", roundDB
	if e.Databases != nil {
		var err error
		staged, dbDir, err = e.Databases.Stage()
		if err != nil {
			return "", "", false, err
		}
	}
	if err := e.Builder.CreateDatabase(ctx, tree.Dir, dbDir, "actions"); err != nil {
		// A build that refused leaves a staged directory the ceiling cannot see, because the
		// ceiling is computed from the rows naming promoted databases.
		e.Databases.DiscardStaged(staged)
		return "", "", false, err
	}
	return dbDir, staged, false, nil
}

// keepDatabase promotes a freshly built database, or removes it when nothing retains databases.
// It reports where the database was kept, which is empty when it was not kept at all.
func (e *Expander) keepDatabase(
	staged, dbDir string, id store.DatabaseIdentity, opts Options, reused bool,
) (string, int64, error) {
	if reused {
		return dbDir, 0, nil
	}
	if e.Databases == nil {
		// codeql refuses to write into an existing directory, which re-analysis would otherwise hit.
		if !opts.KeepArtifacts {
			os.RemoveAll(dbDir)
		}
		return "", 0, nil
	}
	return e.Databases.Retain(staged, id)
}

func (e *Expander) consume(
	_ context.Context, b batch, p work.Prepared, opts Options, planned tierPlan, stats *Stats,
) (int, error) {
	if p.AlreadyRecorded {
		stats.Skipped++
		if err := e.attachRefs(b, p, planned); err != nil {
			return 0, err
		}
		return 0, e.settle(b, p, opts, stats)
	}
	// The repository is being analyzed because something depends on it, not because it is
	// under review, so its own CI must not register as dependency pressure.
	if _, err := e.Store.InsertUpstreamRepository(b.full()); err != nil {
		return 0, err
	}

	// A suite decides which query supplied the edges, so the ledger records that query rather
	// than whatever --query-id happened to say. There may be none: a round that had only the
	// findings lane left to prove built a database and never ran an edges query.
	at := b.revision(p, e.sourceFilter())
	if err := e.recordDatabase(p, at, planned); err != nil {
		return 0, err
	}
	rows, err := e.ingestEdges(p, opts, planned, at)
	if err != nil {
		return 0, err
	}

	// Everything else the suite produced. A query that fails to ingest must not lose the
	// dependency edges already written for this repository, so these are reported and skipped
	// rather than aborting the round.
	for queryID, path := range p.Extra {
		e.report(at.repository, queryID, e.ingestExtra(queryID, path, at, planned))
	}

	if p.SarifPath != "" {
		e.report(at.repository, "findings", e.ingestFindings(p.SarifPath, at, planned))
	}

	if err := e.Writer.Flush(); err != nil {
		return 0, err
	}
	e.Writer.ResetLocal()

	return rows, e.settle(b, p, opts, stats)
}

// trimDatabases evicts retained databases, least recently used first, until the cache is inside
// its ceiling. It reports how many it dropped.
//
// The index goes first and the directory second. An interrupted trim then leaves a directory
// nothing refers to, which is reclaimable; the other order leaves a row naming a database that
// is gone, which is a hit nothing can serve.
func (e *Expander) trimDatabases() (int, error) {
	if e.Databases == nil {
		return 0, nil
	}
	// Abandoned builds first: they occupy disk the ceiling below cannot account for, so trimming
	// to a ceiling without reclaiming them frees less than it believes it has.
	reclaimed, err := e.Databases.ReclaimStaging()
	if err != nil {
		return reclaimed, err
	}
	if e.Databases.MaxBytes <= 0 {
		return reclaimed, nil
	}
	dropped, err := e.Store.EvictDatabasesOver(e.Databases.MaxBytes)
	if err != nil {
		return reclaimed, err
	}
	for _, d := range dropped {
		if err := e.Databases.Discard(d.Path); err != nil {
			return reclaimed + len(dropped), err
		}
	}
	return reclaimed + len(dropped), nil
}

// recordDatabase indexes the database a round's results came from, when one was retained.
//
// The directory is promoted first and indexed here, so an interrupted round leaves a directory
// nothing refers to rather than a row naming a build that never finished. Reuse only moves the
// row's recency: the size belongs to the build that measured it, and re-measuring a database on
// every hit would walk the whole corpus for nothing.
func (e *Expander) recordDatabase(p work.Prepared, at revision, planned tierPlan) error {
	if p.DatabasePath == "" {
		return nil
	}
	id := at.database()
	if p.DatabaseReused {
		_, err := e.Store.UseDatabase(id)
		return err
	}
	_, err := e.Store.RetainDatabase(store.RetainedDatabase{
		DatabaseIdentity: id,
		Path:             p.DatabasePath,
		Bytes:            p.DatabaseBytes,
		CLIVersion:       planned.cliVersion,
	})
	return err
}

// ingestEdges records the dependency edges one round produced, and how many rows they were.
func (e *Expander) ingestEdges(
	p work.Prepared, opts Options, planned tierPlan, at revision,
) (int, error) {
	if p.ResultPath == "" {
		return 0, nil
	}
	queryID := p.ResultQueryID
	if queryID == "" {
		queryID = opts.QueryID
	}
	runID, err := e.Store.RecordQueryRun(planned.queryRun(queryID, at))
	if err != nil {
		return 0, err
	}
	f, err := os.Open(p.ResultPath)
	if err != nil {
		return 0, fmt.Errorf("opening results: %w", err)
	}
	defer f.Close()
	return e.Ingester.EdgesFromBQRS(f, ingest.Options{
		Repository: at.repository, QueryRunID: runID,
	})
}

// report surfaces a failure that must not abort the round, naming what failed.
func (e *Expander) report(repository, what string, err error) {
	if err == nil || e.OnEvent == nil {
		return
	}
	e.OnEvent(work.Event{
		Kind: work.EventFailed,
		Job:  work.Job{Repository: repository},
		Err:  fmt.Errorf("%s: %w", what, err),
	})
}

// settle enqueues what the repository revealed and takes its dependencies out of the queue.
//
// A skipped repository goes through this too. The ledger row is written before discoveries are
// enqueued, so a run interrupted between the two would otherwise skip on the next pass and never
// discover what it had already paid to find.
func (e *Expander) settle(b batch, p work.Prepared, opts Options, stats *Stats) error {
	discovered, err := e.enqueueDiscoveries(b, p.Commit)
	if err != nil {
		return err
	}
	stats.Discovered += discovered

	// The reference this batch checked out, not the default branch. Recording HEAD for a row
	// pinned to a SHA would make Resolution.Mutable() call an immutable reference movable.
	ref := b.ref
	if ref == "" {
		ref = frontier.HeadRef
	}
	res := frontier.Resolution{Ref: ref, SHA: p.Commit}
	for _, it := range b.items {
		if err := e.Queue.Complete(it.Key(), res); err != nil {
			return err
		}
		stats.Items++
	}

	if !opts.KeepArtifacts && p.ResultPath != "" {
		os.RemoveAll(filepath.Dir(p.ResultPath))
	}
	return nil
}

// enqueueDiscoveries queues what the repository names. analyzed is the commit this settle is
// about to record, so the lock read at it can answer before the frontier knows the commit.
func (e *Expander) enqueueDiscoveries(b batch, analyzed string) (int, error) {
	chain, err := (&graph.Builder{Store: e.Store}).ClassifyDependencies()
	if err != nil {
		return 0, err
	}
	closure, err := (&graph.Builder{Store: e.Store}).OwnedClosure()
	if err != nil {
		return 0, err
	}
	// Nothing owned reaches this repository, so what it names is somebody else's supply chain.
	if !closure.Contains(b.full()) {
		return 0, nil
	}
	items, err := discoveriesOf(e.Store, chain, b.full(), b.depth, analyzed)
	if err != nil {
		return 0, err
	}
	return e.Queue.Enqueue(items)
}

// ReclassifySupplyChain re-derives how every queued dependency is classified from the whole
// graph, and is what lets a tightened rule reach a corpus that is already built.
func ReclassifySupplyChain(s *store.Store, q *frontier.Queue) (int, error) {
	chain, err := (&graph.Builder{Store: s}).ClassifyDependencies()
	if err != nil {
		return 0, err
	}
	return q.Reclassify(chain)
}

// SweepDiscoveries re-applies the discovery rule to every repository already analyzed.
//
// enqueueDiscoveries runs only at the moment a repository is analyzed, so a change to what
// counts as a dependency reaches an existing database only when something is re-analyzed — and
// on a drained queue nothing ever is. Enqueue ignores what it already holds, so this settles to
// zero once the rule and the stored graph agree.
func SweepDiscoveries(s *store.Store, q *frontier.Queue) (int, error) {
	chain, err := (&graph.Builder{Store: s}).ClassifyDependencies()
	if err != nil {
		return 0, err
	}
	closure, err := (&graph.Builder{Store: s}).OwnedClosure()
	if err != nil {
		return 0, err
	}
	rows, err := s.DB().Query(`
		SELECT r.repository_full_name, COALESCE(MIN(f.depth), 0)
		FROM repository r
		LEFT JOIN abom_frontier f ON f.owner || '/' || f.repo = r.repository_full_name
		GROUP BY r.repository_full_name`)
	if err != nil {
		return 0, fmt.Errorf("listing analyzed repositories: %w", err)
	}
	type source struct {
		full  string
		depth int
	}
	var sources []source
	for rows.Next() {
		var src source
		if err := rows.Scan(&src.full, &src.depth); err != nil {
			rows.Close()
			return 0, err
		}
		sources = append(sources, src)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	added := 0
	for _, src := range sources {
		// The sweep re-applies the discovery rule to everything analyzed, which before the
		// boundary meant every repository in the table — including the 895 upstream ones nothing
		// owned reaches.
		if !closure.Contains(src.full) {
			continue
		}
		items, err := discoveriesOf(s, chain, src.full, src.depth, "")
		if err != nil {
			return added, err
		}
		n, err := q.Enqueue(items)
		if err != nil {
			return added, err
		}
		added += n
	}
	return added, nil
}

// discoveriesOf lists the external dependencies one analyzed repository names, classified by
// whether anything under review executes them.
//
// What contains a `uses:` is decided by CodeQL, not here: `AbomEdges.qll` emits
// `composite-step-uses-action` for a step inside a `runs:` block and `step-uses-action` for one
// inside a workflow job. Re-deriving that from file paths would reinvent the parser and get it
// wrong — an earlier gate required the container to be referenced from *another* repository,
// which dropped every composite action called only by its own repository's workflows, and with
// it dependencies that run whenever the organization uses the parent action.
//
// Classification is a whole-graph question and is answered by graph.ClassifyDependencies, not
// from the rows below: what this repository can see is whether it names a dependency, never
// whether anyone under review reaches the file that names it.
//
// A dependency a lock selects is also queued at the commit it selects, beside its declared ref
// rather than instead of it: every reader that describes the source as written is keyed on the
// declared ref, and an unlocked consumer of the same pin still runs what the tag names.
func discoveriesOf(
	s *store.Store, chain *graph.SupplyChain, full string, depth int, analyzed string,
) ([]frontier.Item, error) {
	rows, err := s.DB().Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       COALESCE(e.ref_value, '')
		FROM abom_edge_regular e
		JOIN abom_node_live n ON n.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.repository_full_name = ?
		  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		GROUP BY n.node_key, COALESCE(e.ref_value, '')`, full)
	if err != nil {
		return nil, fmt.Errorf("reading discoveries for %s: %w", full, err)
	}
	defer rows.Close()

	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		if err := rows.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath, &it.Ref); err != nil {
			return nil, err
		}
		it.Depth = depth + 1
		it.DiscoveredFrom = full
		it.SupplyChain = frontier.SupplyChainBuild
		if runtime, _ := chain.Of(it.NodeKey); runtime {
			it.SupplyChain = frontier.SupplyChainRuntime
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(items, func(i, j int) bool { return items[i].NodeKey < items[j].NodeKey })

	selected, err := s.LockSelectedDependencies(full, analyzed)
	if err != nil {
		return nil, err
	}
	for _, sel := range selected {
		it := frontier.Item{
			NodeKey: sel.NodeKey, Owner: sel.Owner, Repo: sel.Repo, Subpath: sel.Path,
			Ref: sel.Commit, Depth: depth + 1, DiscoveredFrom: full,
			SupplyChain: frontier.SupplyChainBuild,
		}
		if runtime, _ := chain.Of(it.NodeKey); runtime {
			it.SupplyChain = frontier.SupplyChainRuntime
		}
		items = append(items, it)
	}
	return items, nil
}

// settleFailure decides whether a failed repository is worth trying again.
func (e *Expander) settleFailure(b batch, cause error, stats *Stats) {
	permanent := IsUnavailable(cause) || runner.IsNoSourceCode(cause)
	for _, it := range b.items {
		// Discarded deliberately. This runs from the failure path, where the caller is already
		// being told about `cause`; returning a second error here would replace the one that
		// explains what actually went wrong. An item whose state was not recorded stays claimed
		// and is retried on a later run.
		if permanent {
			_ = e.Queue.Unavailable(it.Key(), reasonOf(cause))
			continue
		}
		_ = e.Queue.Fail(it.Key(), reasonOf(cause))
	}
	if permanent {
		stats.Unavailable++
		return
	}
	stats.Failed++
}

func reasonOf(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(err.Error())
	const max = 300
	if len(msg) > max {
		return msg[:max]
	}
	return msg
}
