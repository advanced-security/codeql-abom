package expand

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Fetcher materializes a repository's source tree.
type Fetcher interface {
	Fetch(ctx context.Context, dest, owner, repo, ref string) (Tree, error)
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
	// Processed counts repositories analyzed; Items counts the dependencies they settled.
	Processed int
	// Skipped counts repositories the ledger already had on record, settled without analysis.
	Skipped     int
	Items       int
	Failed      int
	Unavailable int
	Discovered  int
	Edges       int
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
	// Resolver expands Options.Suite. Only needed when a suite is used.
	Resolver Resolver
	// Analyzer runs the findings lane. Only needed when a suite holds findings-shaped queries.
	Analyzer Analyzer
	// Pace caps the rate of outbound requests across workers; nil means unlimited.
	Pace    *work.Limiter
	OnEvent func(work.Event)

	// digests remembers each pack's content hash for the life of a round. A pack cannot change
	// while the round that resolved it is running.
	digests runner.DigestCache
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

func (b batch) full() string { return b.owner + "/" + b.repo }

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

	plans, err := e.plansByTier(ctx, opts)
	if err != nil {
		return Stats{}, err
	}
	// A binding that names findings queries with nothing able to run them is the defect this
	// lane exists to fix, so it fails loudly here rather than quietly producing no findings.
	for tier, p := range plans {
		if len(p.findings) > 0 && e.Analyzer == nil {
			return Stats{}, fmt.Errorf(
				"expand: %s is bound to %d findings queries but no Analyzer is configured",
				tier, len(p.findings))
		}
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
	jobs := make([]work.Job, 0, len(batches))
	byRepo := make(map[string]batch, len(batches))
	for _, b := range batches {
		jobs = append(jobs, work.Job{Repository: b.key()})
		byRepo[b.key()] = b
	}

	pool := &work.Pool{
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
			return e.consume(ctx, b, p, opts, plans[tierOf(b, owned)], &stats)
		},
		OnEvent: func(ev work.Event) {
			if ev.Kind == work.EventFailed {
				e.settleFailure(byRepo[ev.Job.Repository], ev.Err, &stats)
			}
			if e.OnEvent != nil {
				e.OnEvent(ev)
			}
		},
	}

	poolStats, err := pool.Run(ctx, jobs)
	if err != nil {
		return stats, err
	}
	stats.Processed = poolStats.Processed - stats.Skipped
	stats.Edges = poolStats.Rows
	return stats, nil
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
	for _, q := range planned.bqrs {
		ran, err := done(planned.queryRun(q.id, repository, commit))
		if err != nil {
			return pending{}, err
		}
		if !ran {
			out.bqrs = append(out.bqrs, q)
		}
	}
	if len(planned.findings) > 0 {
		ran, err := done(planned.findingsRun(repository, commit))
		if err != nil {
			return pending{}, err
		}
		out.findings = !ran
	}
	return out, nil
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

	// Before the clone, not after: the ledger only needs the commit, and a reference resolves
	// remotely for a fraction of what fetching it costs. A tier requeue puts back everything
	// including what is already current, and those are settled here without a checkout.
	if sha, ok := e.commitWithoutFetching(ctx, b); ok {
		// A commit seen before has its content on record, so the equivalence is available here
		// too. A commit never seen has none, and an absent digest matches nothing.
		digest, _, err := e.Store.CommitContentDigest(b.full(), sha)
		if err != nil {
			return work.Prepared{}, err
		}
		outstanding, err := e.pendingWork(planned, b.full(), sha, digest)
		if err != nil {
			return work.Prepared{}, err
		}
		if outstanding.empty() {
			return work.Prepared{
				Job:             work.Job{Repository: b.key()},
				Commit:          sha,
				AlreadyRecorded: true,
			}, nil
		}
	}

	if err := e.Pace.Wait(ctx); err != nil {
		return work.Prepared{}, err
	}
	tree, err := e.Fetcher.Fetch(ctx, src, b.owner, b.repo, b.ref)
	if err != nil {
		return work.Prepared{}, err
	}

	// What the extractor would read, recorded whether or not this round goes on to analyze it:
	// the value of knowing it is that a later commit can be recognized as the same database.
	digest, err := IndexedDigest(tree.Dir)
	if err != nil {
		return work.Prepared{}, err
	}
	if digest != "" && tree.SHA != "" {
		if err := e.Store.RecordCommitContent(b.full(), tree.SHA, digest); err != nil {
			return work.Prepared{}, err
		}
	}

	// The commit is only knowable once the source is in hand, so the checkout is not avoidable
	// here. Deciding from abom_frontier.resolved_sha instead would skip precisely when a ref had
	// moved, which is the case re-analysis exists for.
	outstanding, err := e.pendingWork(planned, b.full(), tree.SHA, digest)
	if err != nil {
		return work.Prepared{}, err
	}
	if outstanding.empty() {
		if !opts.KeepArtifacts {
			os.RemoveAll(src)
		}
		return work.Prepared{
			Job:             work.Job{Repository: b.key()},
			Commit:          tree.SHA,
			AlreadyRecorded: true,
		}, nil
	}

	dbDir := filepath.Join(dir, "db")
	if err := e.Builder.CreateDatabase(ctx, tree.Dir, dbDir, "actions"); err != nil {
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
	if !opts.KeepArtifacts {
		os.RemoveAll(src)
		os.RemoveAll(dbDir)
	}
	return work.Prepared{
		Job:           work.Job{Repository: b.key()},
		ResultPath:    jsonPath,
		ResultQueryID: edgesID,
		SarifPath:     sarifPath,
		Extra:         extra,
		Commit:        tree.SHA,
	}, nil
}

func (e *Expander) consume(
	_ context.Context, b batch, p work.Prepared, opts Options, planned tierPlan, stats *Stats,
) (int, error) {
	if p.AlreadyRecorded {
		stats.Skipped++
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
	rows := 0
	if p.ResultPath != "" {
		queryID := p.ResultQueryID
		if queryID == "" {
			queryID = opts.QueryID
		}
		runID, err := e.Store.RecordQueryRun(planned.queryRun(queryID, b.full(), p.Commit))
		if err != nil {
			return 0, err
		}

		f, err := os.Open(p.ResultPath)
		if err != nil {
			return 0, fmt.Errorf("opening results: %w", err)
		}
		rows, err = e.Ingester.EdgesFromBQRS(f, ingest.Options{
			Repository: b.full(), QueryRunID: runID,
		})
		f.Close()
		if err != nil {
			return 0, err
		}
	}

	// Everything else the suite produced. A query that fails to ingest must not lose the
	// dependency edges already written for this repository, so these are reported and skipped
	// rather than aborting the round.
	for queryID, path := range p.Extra {
		if err := e.ingestExtra(queryID, path, b.full(), p.Commit, planned); err != nil &&
			e.OnEvent != nil {
			e.OnEvent(work.Event{
				Kind: work.EventFailed,
				Job:  work.Job{Repository: b.full()},
				Err:  fmt.Errorf("%s: %w", queryID, err),
			})
		}
	}

	if p.SarifPath != "" {
		err := e.ingestFindings(p.SarifPath, b.full(), p.Commit, planned)
		if err != nil && e.OnEvent != nil {
			e.OnEvent(work.Event{
				Kind: work.EventFailed,
				Job:  work.Job{Repository: b.full()},
				Err:  fmt.Errorf("findings: %w", err),
			})
		}
	}

	if err := e.Writer.Flush(); err != nil {
		return 0, err
	}
	e.Writer.ResetLocal()

	return rows, e.settle(b, p, opts, stats)
}

// settle enqueues what the repository revealed and takes its dependencies out of the queue.
//
// A skipped repository goes through this too. The ledger row is written before discoveries are
// enqueued, so a run interrupted between the two would otherwise skip on the next pass and never
// discover what it had already paid to find.
func (e *Expander) settle(b batch, p work.Prepared, opts Options, stats *Stats) error {
	discovered, err := e.enqueueDiscoveries(b)
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

func (e *Expander) enqueueDiscoveries(b batch) (int, error) {
	chain, err := (&graph.Builder{Store: e.Store}).ClassifyDependencies()
	if err != nil {
		return 0, err
	}
	items, err := discoveriesOf(e.Store, chain, b.full(), b.depth)
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
		items, err := discoveriesOf(s, chain, src.full, src.depth)
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
func discoveriesOf(
	s *store.Store, chain *graph.SupplyChain, full string, depth int,
) ([]frontier.Item, error) {
	rows, err := s.DB().Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       COALESCE(e.ref_value, '')
		FROM abom_edge e
		JOIN abom_node n ON n.row_id = e.dst_node_row_id
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
