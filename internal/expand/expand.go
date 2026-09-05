package expand

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Fetcher materializes a repository's source tree.
type Fetcher interface {
	Fetch(ctx context.Context, dest, owner, repo string) (Tree, error)
}

// Builder creates a CodeQL database from a source tree.
type Builder interface {
	CreateDatabase(ctx context.Context, srcDir, dbDir, language string) error
}

// QueryRunner evaluates a query against a database.
type QueryRunner interface {
	RunQuery(ctx context.Context, dbPath, queryPath, outputPath string) error
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
	Processed   int
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
	Builder  Builder
	CodeQL   QueryRunner
	Decode   Decoder
	// Resolver expands Options.Suite. Only needed when a suite is used.
	Resolver Resolver
	// Pace caps the rate of outbound requests across workers; nil means unlimited.
	Pace    *work.Limiter
	OnEvent func(work.Event)
}

// batch is the set of queued dependencies answered by analyzing one repository.
type batch struct {
	owner string
	repo  string
	items []frontier.Item
	// depth is the shallowest depth among the items, and what discoveries are measured from.
	depth int
}

func (b batch) full() string { return b.owner + "/" + b.repo }

// Run claims a round of work, analyzes it, and enqueues what it discovers.
func (e *Expander) Run(ctx context.Context, opts Options) (Stats, error) {
	if (opts.QueryPath == "" && opts.Suite == "") || opts.WorkDir == "" {
		return Stats{}, fmt.Errorf("expand: a QueryPath or Suite, and WorkDir, are required")
	}

	planned, err := e.plan(ctx, opts)
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

	var stats Stats
	jobs := make([]work.Job, 0, len(batches))
	byRepo := make(map[string]batch, len(batches))
	for _, b := range batches {
		jobs = append(jobs, work.Job{Repository: b.full()})
		byRepo[b.full()] = b
	}

	pool := &work.Pool{
		Concurrency: opts.Concurrency,
		Prepare: func(ctx context.Context, job work.Job) (work.Prepared, error) {
			return e.prepare(ctx, byRepo[job.Repository], opts, planned)
		},
		Consume: func(ctx context.Context, p work.Prepared) (int, error) {
			return e.consume(ctx, byRepo[p.Job.Repository], p, opts, &stats)
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
	stats.Processed = poolStats.Processed
	stats.Edges = poolStats.Rows
	return stats, nil
}

// groupByRepository collapses claimed dependencies onto the repositories that answer them.
func groupByRepository(items []frontier.Item) []batch {
	order := []string{}
	seen := map[string]*batch{}
	for _, it := range items {
		if it.Owner == "" || it.Repo == "" {
			continue
		}
		key := it.Owner + "/" + it.Repo
		b, ok := seen[key]
		if !ok {
			b = &batch{owner: it.Owner, repo: it.Repo, depth: it.Depth}
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

func (e *Expander) prepare(
	ctx context.Context, b batch, opts Options, planned []plannedQuery,
) (work.Prepared, error) {
	dir := filepath.Join(opts.WorkDir, b.owner+"_"+b.repo)
	src := filepath.Join(dir, "src")

	if err := e.Pace.Wait(ctx); err != nil {
		return work.Prepared{}, err
	}
	tree, err := e.Fetcher.Fetch(ctx, src, b.owner, b.repo)
	if err != nil {
		return work.Prepared{}, err
	}

	dbDir := filepath.Join(dir, "db")
	if err := e.Builder.CreateDatabase(ctx, tree.Dir, dbDir, "actions"); err != nil {
		return work.Prepared{}, err
	}

	jsonPath, extra, err := e.runQueries(ctx, dbDir, dir, planned)
	if err != nil {
		return work.Prepared{}, err
	}

	// The checkout and database are the bulk of the disk cost and are finished with here, so
	// they go before the serialized phase rather than after it.
	if !opts.KeepArtifacts {
		os.RemoveAll(src)
		os.RemoveAll(dbDir)
	}
	return work.Prepared{
		Job:        work.Job{Repository: b.full()},
		ResultPath: jsonPath,
		Extra:      extra,
		Commit:     tree.SHA,
	}, nil
}

func (e *Expander) consume(
	_ context.Context, b batch, p work.Prepared, opts Options, stats *Stats,
) (int, error) {
	// The repository is being analyzed because something depends on it, not because it is
	// under review, so its own CI must not register as dependency pressure.
	if _, err := e.Store.InsertUpstreamRepository(b.full()); err != nil {
		return 0, err
	}

	runID, err := e.Store.RecordQueryRun(store.QueryRun{
		QueryID: opts.QueryID, Repository: b.full(), DBCommit: p.Commit,
	})
	if err != nil {
		return 0, err
	}

	f, err := os.Open(p.ResultPath)
	if err != nil {
		return 0, fmt.Errorf("opening results: %w", err)
	}
	rows, err := e.Ingester.EdgesFromBQRS(f, ingest.Options{
		Repository: b.full(), QueryRunID: runID,
	})
	f.Close()
	if err != nil {
		return 0, err
	}

	// Everything else the suite produced. A query that fails to ingest must not lose the
	// dependency edges already written for this repository, so these are reported and skipped
	// rather than aborting the round.
	for queryID, path := range p.Extra {
		if err := e.ingestExtra(queryID, path, b.full(), p.Commit); err != nil && e.OnEvent != nil {
			e.OnEvent(work.Event{
				Kind: work.EventFailed,
				Job:  work.Job{Repository: b.full()},
				Err:  fmt.Errorf("%s: %w", queryID, err),
			})
		}
	}

	if err := e.Writer.Flush(); err != nil {
		return 0, err
	}
	e.Writer.ResetLocal()

	discovered, err := e.enqueueDiscoveries(b)
	if err != nil {
		return 0, err
	}
	stats.Discovered += discovered

	res := frontier.Resolution{Ref: "HEAD", SHA: p.Commit}
	for _, it := range b.items {
		if err := e.Queue.Complete(it.NodeKey, res); err != nil {
			return 0, err
		}
		stats.Items++
	}

	if !opts.KeepArtifacts {
		os.RemoveAll(filepath.Dir(p.ResultPath))
	}
	return rows, nil
}

func (e *Expander) enqueueDiscoveries(b batch) (int, error) {
	items, err := discoveriesOf(e.Store, b.full(), b.depth)
	if err != nil {
		return 0, err
	}
	return e.Queue.Enqueue(items)
}

// SweepDiscoveries re-applies the discovery rule to every repository already analyzed.
//
// enqueueDiscoveries runs only at the moment a repository is analyzed, so a change to what
// counts as a dependency reaches an existing database only when something is re-analyzed — and
// on a drained queue nothing ever is. Enqueue ignores what it already holds, so this settles to
// zero once the rule and the stored graph agree.
func SweepDiscoveries(s *store.Store, q *frontier.Queue) (int, error) {
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
		items, err := discoveriesOf(s, src.full, src.depth)
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
// whether anything executes them.
//
// What contains a `uses:` is decided by CodeQL, not here: `AbomEdges.qll` emits
// `composite-step-uses-action` for a step inside a `runs:` block and `step-uses-action` for one
// inside a workflow job. Re-deriving that from file paths would reinvent the parser and get it
// wrong — an earlier gate required the container to be referenced from *another* repository,
// which dropped every composite action called only by its own repository's workflows, and with
// it dependencies that run whenever the organization uses the parent action.
func discoveriesOf(s *store.Store, full string, depth int) ([]frontier.Item, error) {
	rows, err := s.DB().Query(`
		SELECT n.node_key, COALESCE(n.owner,''), COALESCE(n.repo,''), COALESCE(n.path,''),
		       MAX(CASE
		             WHEN e.edge_kind = 'composite-step-uses-action' THEN 1
		             WHEN EXISTS (
		                    SELECT 1 FROM abom_edge ref
		                    WHERE ref.dst_node_row_id = src.container_node_row_id) THEN 1
		             ELSE 0
		           END) AS runtime
		FROM abom_edge e
		JOIN abom_node n ON n.row_id = e.dst_node_row_id
		JOIN abom_node src ON src.row_id = e.src_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE r.repository_full_name = ?
		  AND n.is_external = 1 AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		GROUP BY n.node_key`, full)
	if err != nil {
		return nil, fmt.Errorf("reading discoveries for %s: %w", full, err)
	}
	defer rows.Close()

	var items []frontier.Item
	for rows.Next() {
		var it frontier.Item
		var runtime int
		if err := rows.Scan(&it.NodeKey, &it.Owner, &it.Repo, &it.Subpath, &runtime); err != nil {
			return nil, err
		}
		it.Depth = depth + 1
		it.DiscoveredFrom = full
		it.SupplyChain = frontier.SupplyChainBuild
		if runtime == 1 {
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
			_ = e.Queue.Unavailable(it.NodeKey, reasonOf(cause))
			continue
		}
		_ = e.Queue.Fail(it.NodeKey, reasonOf(cause))
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
