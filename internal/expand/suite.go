package expand

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Resolver expands a suite into queries and reads what each declares about itself.
//
// An interface so the expansion loop can be tested without a CodeQL CLI, and so a caller that
// still names a single query needs no resolver at all.
type Resolver interface {
	ResolveQueries(ctx context.Context, suite string) ([]string, error)
	ResolveMetadata(ctx context.Context, queryPath string) (runner.Metadata, error)
	// Version is part of the identity of every result: extraction and evaluation both change
	// between CodeQL releases, so the same query on the same commit may legitimately differ.
	Version(ctx context.Context) (string, error)
}

// tierPlan is what one tier runs, split by the two lanes that can read it.
//
// They are separate because `database analyze` cannot execute a `@kind graph` or table query,
// and `query run` cannot produce SARIF. A suite may legitimately hold both.
type tierPlan struct {
	bqrs []plannedQuery
	// findings holds query paths rather than a suite, so a mixed suite contributes only the
	// queries this lane can actually run.
	findings []string
	// contents is every query the suite resolved to, in both lanes, with the pack that supplied
	// it. Recorded because a suite has no version of its own; the packs behind it do.
	contents []store.SuiteQuery
	// cliVersion is the CodeQL that ran, which is part of what makes a result reproducible.
	cliVersion string
	// findingsPacks are the packs the findings lane draws rules from. One invocation covers all
	// of them, so no single pack identifies that run and the set has to stand in for it.
	findingsPacks []store.SuiteQuery
}

func (p tierPlan) empty() bool { return len(p.bqrs) == 0 && len(p.findings) == 0 }

// absorb folds one suite's plan into a tier's.
//
// Every field has to be named here. Appending only the two query slices lost the CodeQL version
// and the resolved contents on the way into the tier, which showed up as a NULL cli_version on
// rows whose pack and digest looked correct, because those travel inside plannedQuery.
func (p *tierPlan) absorb(from tierPlan) {
	p.bqrs = append(p.bqrs, from.bqrs...)
	p.findings = append(p.findings, from.findings...)
	p.contents = append(p.contents, from.contents...)
	for _, pk := range from.findingsPacks {
		p.notePack(pk.Pack, pk.PackVersion, pk.PackDigest)
	}
	// One CLI evaluates every suite in a round, so the last writer agrees with the first.
	if from.cliVersion != "" {
		p.cliVersion = from.cliVersion
	}
}

// notePack records a pack the findings lane draws from, once. Resolution order is stable, so the
// set stays in a fixed order and hashes the same way across runs.
func (p *tierPlan) notePack(pack, version, digest string) {
	for _, seen := range p.findingsPacks {
		if seen.Pack == pack && seen.PackDigest == digest {
			return
		}
	}
	p.findingsPacks = append(p.findingsPacks, store.SuiteQuery{
		Pack: pack, PackVersion: version, PackDigest: digest,
	})
}

// identify fills in what makes a run reproducible: the pack that supplied the query, that pack's
// content hash, and the CodeQL that evaluated it.
func (p tierPlan) identify(queryID string, run store.QueryRun) store.QueryRun {
	run.CLIVersion = p.cliVersion
	for _, q := range p.bqrs {
		if q.id == queryID {
			run.Pack, run.PackVersion, run.PackDigest = q.pack, q.packVersion, q.packDigest
			return run
		}
	}
	for _, c := range p.contents {
		if c.QueryID == queryID {
			run.Pack, run.PackVersion, run.PackDigest = c.Pack, c.PackVersion, c.PackDigest
			return run
		}
	}
	return run
}

// identifyFindings names the whole findings lane, because `database analyze` runs it as one
// invocation and no single query owns the result.
//
// The digest has to move when any contributing pack moves. Were it left empty, upgrading
// codeql/actions-queries would leave the run key unchanged and a later skip would decline to
// re-analyze against rules that had in fact changed.
func (p tierPlan) identifyFindings(run store.QueryRun) store.QueryRun {
	run.CLIVersion = p.cliVersion
	switch len(p.findingsPacks) {
	case 0:
		return run
	case 1:
		only := p.findingsPacks[0]
		run.Pack, run.PackVersion, run.PackDigest = only.Pack, only.PackVersion, only.PackDigest
		return run
	}

	names := make([]string, 0, len(p.findingsPacks))
	h := sha256.New()
	for _, pk := range p.findingsPacks {
		names = append(names, pk.Pack)
		fmt.Fprintf(h, "%s@%s:%s\x00", pk.Pack, pk.PackVersion, pk.PackDigest)
	}
	run.Pack = strings.Join(names, " ")
	run.PackDigest = hex.EncodeToString(h.Sum(nil))
	return run
}

// runs is every ledger row this plan will write for one database.
//
// The skip and the record are built from this same list on purpose. If they were assembled
// separately the skip would silently stop firing the moment one of them gained a field.
func (p tierPlan) runs(repository, commit string) []store.QueryRun {
	out := make([]store.QueryRun, 0, len(p.bqrs)+1)
	for _, q := range p.bqrs {
		out = append(out, p.identify(q.id, store.QueryRun{
			QueryID: q.id, Repository: repository, DBCommit: commit,
		}))
	}
	if len(p.findings) > 0 {
		out = append(out, p.identifyFindings(store.QueryRun{
			QueryID: strings.Join(p.findings, " "), Repository: repository, DBCommit: commit,
		}))
	}
	return out
}

// plannedQuery is one query to run against a repository's database, with the shape its results
// must be read as.
type plannedQuery struct {
	path  string
	id    string
	shape ingest.Shape
	// The pack that supplied it, and that pack's content hash. Together with the database and
	// the CLI version these decide whether this evaluation has already happened.
	pack        string
	packVersion string
	packDigest  string
}

// tierOf decides which pass a batch earns, from the shallowest dependency it holds.
//
// Shallowest rather than deepest: a repository reached both directly and through an intermediary
// is one a repository under review names itself, and the broader pass is the one it has earned.
func tierOf(b batch) string {
	if b.depth <= 1 {
		return store.TierDirect
	}
	return store.TierTransitive
}

// plansByTier works out what to run for each tier, once per round.
//
// A suite two tiers share is resolved once: resolving shells out to the CodeQL CLI, and a crawl
// claims batches from both tiers every round.
func (e *Expander) plansByTier(ctx context.Context, opts Options) (map[string]tierPlan, error) {
	tiers := []string{store.TierDirect, store.TierTransitive}
	out := map[string]tierPlan{}

	// A suite named on the command line is how an operator runs something once without editing
	// stored configuration, so it wins for every tier.
	if opts.Suite != "" || opts.QueryPath != "" {
		planned, err := e.plan(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, tier := range tiers {
			out[tier] = planned
		}
		return out, nil
	}

	bound, err := e.Store.TierSuites()
	if err != nil {
		return nil, err
	}
	resolved := map[string]tierPlan{}
	for _, tier := range tiers {
		var merged tierPlan
		for _, suite := range bound[tier] {
			queries, ok := resolved[suite]
			if !ok {
				queries, err = e.plan(ctx, Options{Suite: suite})
				if err != nil {
					return nil, err
				}
				resolved[suite] = queries
				// What a suite resolves to is only knowable by resolving it, so the round that
				// just paid that cost is the only place the answer is free.
				if err := e.Store.RecordSuiteContents(suite, queries.contents); err != nil {
					return nil, err
				}
			}
			merged.absorb(queries)
		}
		out[tier] = merged
	}
	return out, nil
}

// plan works out what to run for a round, once, rather than per repository.
//
// Resolving a suite shells out to the CodeQL CLI, and doing that for every repository in a crawl
// of thousands would cost more than the queries. The suite cannot change mid-round.
func (e *Expander) plan(ctx context.Context, opts Options) (tierPlan, error) {
	if opts.Suite == "" {
		// A single query path stays supported: it is what `expand` and `build` pass, and a
		// suite is not worth resolving to run one thing.
		return tierPlan{bqrs: []plannedQuery{{
			path: opts.QueryPath, id: opts.QueryID, shape: ingest.ShapeEdges,
		}}}, nil
	}
	if e.Resolver == nil {
		return tierPlan{}, fmt.Errorf("expand: a suite needs a Resolver to expand it")
	}

	paths, err := e.Resolver.ResolveQueries(ctx, opts.Suite)
	if err != nil {
		return tierPlan{}, err
	}
	var planned tierPlan
	// Refusing here rather than recording a run with no CLI version: a ledger row that omits what
	// evaluated it cannot be told apart from one produced by a different CodeQL, and a later skip
	// would trust it.
	planned.cliVersion, err = e.Resolver.Version(ctx)
	if err != nil {
		return tierPlan{}, err
	}
	for _, path := range paths {
		md, err := e.Resolver.ResolveMetadata(ctx, path)
		if err != nil {
			return tierPlan{}, err
		}
		shape, err := ingest.ShapeOf(md)
		if err != nil {
			// Naming the suite matters: the query is in the run because a suite selected it,
			// and the suite is where someone will go to take it back out.
			return tierPlan{}, fmt.Errorf("suite %s: %w", opts.Suite, err)
		}
		pack, packVersion, packDir := runner.PackOf(path)
		digest, err := e.digests.Of(packDir)
		if err != nil {
			return tierPlan{}, fmt.Errorf("suite %s: %w", opts.Suite, err)
		}
		planned.contents = append(planned.contents, store.SuiteQuery{
			Pack: pack, PackVersion: packVersion, PackDigest: digest,
			QueryID: md.ID, Kind: md.Kind,
		})
		if shape == ingest.ShapeFindings {
			planned.findings = append(planned.findings, path)
			planned.notePack(pack, packVersion, digest)
			continue
		}
		planned.bqrs = append(planned.bqrs, plannedQuery{
			path: path, id: md.ID, shape: shape,
			pack: pack, packVersion: packVersion, packDigest: digest,
		})
	}

	// `database analyze` takes a suite, and a suite carries selection the expanded file list
	// does not. Only a suite that mixes lanes has to be named query by query.
	if len(planned.bqrs) == 0 && len(planned.findings) > 0 {
		planned.findings = []string{opts.Suite}
	}
	return planned, nil
}

// runQueries evaluates every planned query against one database, returning decoded results keyed
// by query id. The first edges query is returned separately because settling a dependency
// depends on it, while everything else only enriches.
func (e *Expander) runQueries(
	ctx context.Context, dbDir, dir string, planned []plannedQuery,
) (edges, edgesID string, extra map[string]string, err error) {
	for _, q := range planned {
		bqrsPath := filepath.Join(dir, safeName(q.id)+".bqrs")
		if err := e.CodeQL.RunQuery(ctx, dbDir, q.path, bqrsPath); err != nil {
			return "", "", nil, err
		}
		jsonPath := filepath.Join(dir, safeName(q.id)+".json")
		if err := e.Decode(ctx, bqrsPath, jsonPath); err != nil {
			return "", "", nil, err
		}
		// The BQRS is decoded and finished with; failing to clear it costs disk, not results.
		_ = os.Remove(bqrsPath)

		if q.shape == ingest.ShapeEdges && edges == "" {
			edges, edgesID = jsonPath, q.id
			continue
		}
		if extra == nil {
			extra = make(map[string]string, len(planned))
		}
		extra[q.id] = jsonPath
	}
	return edges, edgesID, extra, nil
}

// ingestExtra records one non-edge query's results against the repository just analyzed.
//
// Attribution is per query, not per round: two queries against one database are two statements
// by two authors, and a shared run id would make it impossible to say which said what.
func (e *Expander) ingestExtra(queryID, path, repository, commit string, planned tierPlan) error {
	runID, err := e.Store.RecordQueryRun(planned.identify(queryID, store.QueryRun{
		QueryID: queryID, Repository: repository, DBCommit: commit,
	}))
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening results: %w", err)
	}
	defer f.Close()

	_, err = e.Ingester.ObservationsFromBQRS(f, ingest.Options{
		Repository: repository, QueryRunID: runID,
	})
	return err
}

// ingestFindings records the findings lane's SARIF against the repository just analyzed.
//
// The run is named after what was invoked, because `database analyze` is one invocation over
// many queries. Which query made which claim is already on each result, as its rule id.
func (e *Expander) ingestFindings(path, repository, commit string, planned tierPlan) error {
	runID, err := e.Store.RecordQueryRun(planned.identifyFindings(store.QueryRun{
		QueryID: strings.Join(planned.findings, " "), Repository: repository, DBCommit: commit,
	}))
	if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening sarif: %w", err)
	}
	defer f.Close()

	_, err = e.Ingester.FindingsFromSARIF(f, ingest.Options{
		Repository: repository, QueryRunID: runID, CLIVersion: planned.cliVersion,
	})
	return err
}

// safeName turns a query id into something usable as a filename. Query ids carry slashes, and
// two queries in one suite must not write to the same path.
func safeName(id string) string {
	if id == "" {
		return "results"
	}
	out := []rune(id)
	for i, r := range out {
		switch r {
		case '/', '\\', ':', ' ':
			out[i] = '-'
		}
	}
	return string(out)
}
