package expand

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/sarifin"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Executor carries out a run somebody asked for: an explicit list of queries against the
// databases a scope resolved to.
//
// Apart from the crawl, which decides for itself what to analyze from the frontier. This runs
// what it was told to, over a set that was resolved and shown to a reader before anything
// started. It shares the CodeQL lock with the crawl rather than racing it: every `codeql`
// invocation on this machine contends for one compilation cache.
type Executor struct {
	Store    *store.Store
	Resolver Resolver
	Analyzer Analyzer
	// Ingester is what makes a run's results mean anything to the rest of ABOM. Without it the
	// SARIF is a file on disk and the graph, the Inventory and the gate know nothing about it.
	Ingester *ingest.Ingester
	// Crawl builds a member's missing database through the crawl's own fetch, caches and lock.
	Crawl *Expander
	// Suites are where a query id is resolved to a path. A run names ids because that is what a
	// catalog holds; only the CLI can say which file an id came from.
	Suites []string
	// WorkDir is where each member's raw output is written, outside any scratch directory that a
	// round would remove.
	WorkDir string
	OnEvent func(repository, stage string, err error)
	// Install installs what the operator's pack depends on wherever it has no lock. The CLI compiles
	// nothing in a pack whose dependencies were never installed, and ABOM scaffolded that pack. Nil
	// installs nothing.
	Install func(context.Context) error
}

// Run analyzes every member, building the database a member lacks, and says what became of each.
//
// A member that fails does not stop the others: stopping would discard the work already done and
// leave a reader unable to tell a broken query from a broken repository. One whose database cannot
// be built says why, so a complete run never reads as a clean result for a repository nothing ran
// against.
func (e *Executor) Run(ctx context.Context, runID int64) error {
	run, err := e.Store.AnalysisRun(runID)
	if err != nil {
		return err
	}

	// Resolved before anything runs. A run that analyzed the queries it could find and quietly
	// dropped the rest would report a result for a request that was not carried out.
	planned, err := e.resolve(ctx, run.Queries)
	dir := filepath.Join(e.WorkDir, fmt.Sprintf("run-%d", runID))
	if err == nil {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			err = fmt.Errorf("preparing %s: %w", dir, err)
		}
	}
	if err != nil {
		return e.refuse(ctx, runID, err)
	}

	if err := e.Store.SetAnalysisRunCodeQL(runID, planned.cliVersion); err != nil {
		return err
	}
	if err := e.Store.RecordQueryMetadata(planned.metadata); err != nil {
		return err
	}
	if err := e.Store.SetAnalysisRunState(runID, store.RunRunning); err != nil {
		return err
	}

	for _, member := range run.Members {
		if err := ctx.Err(); err != nil {
			// Recorded as canceled rather than left running: a run nobody stopped and nobody
			// finished is a state a reader cannot act on.
			_ = e.Store.SetAnalysisRunState(runID, store.RunCanceled)
			return err
		}
		if member.Status == store.AnalysisNoDatabase {
			continue
		}
		if err := e.carryOut(ctx, runID, dir, member, run.Queries, planned, run.Held); err != nil {
			if ctx.Err() != nil {
				_ = e.Store.SetAnalysisRunState(runID, store.RunCanceled)
			}
			return err
		}
	}

	// Attribution is what makes a finding visible to anything built on the graph, and the crawl
	// does it once at the end of a cycle for the same reason: the join is over the whole table,
	// so doing it per member would repeat the same work for every repository in the run.
	if !run.Held {
		if _, err := e.Store.LinkFindingsToNodes(); err != nil {
			return fmt.Errorf("attributing what run %d found: %w", runID, err)
		}
	}
	return e.Store.SetAnalysisRunState(runID, store.RunComplete)
}

// refuse records on the run why it could not start: left pending, it read as a run about to start,
// and the reason reached only the server's log. A stop while it prepared is a cancellation.
func (e *Executor) refuse(ctx context.Context, runID int64, cause error) error {
	if ctx.Err() != nil {
		_ = e.Store.SetAnalysisRunState(runID, store.RunCanceled)
		return cause
	}
	if err := e.Store.RefuseAnalysisRun(runID, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// carryOut analyzes one member, building its database first when none is on disk, unless its
// evaluation is already stored.
func (e *Executor) carryOut(
	ctx context.Context, runID int64, dir string, member store.AnalysisMember,
	want []store.AnalysisQuery, planned tierPlan, hold bool,
) error {
	held := onDisk(member.DatabasePath)
	building := "building its database"
	// A result names the CodeQL that ran it, extraction included.
	if by := BuiltBy(member.DatabasePath); held && by != planned.cliVersion {
		held, building = false, fmt.Sprintf("rebuilding its database, which CodeQL %s built", by)
		if by == "" {
			building = "rebuilding its database, which records no CodeQL that built it"
		}
	}
	// A stored answer lists findings, and a held run keeps its own list of what was reported.
	if !hold {
		if skipped, err := e.skipAnswered(runID, member, held, want, planned); err != nil || skipped {
			return err
		}
	}
	if !held {
		if err := e.Store.SetAnalysisMemberStatus(
			runID, member.DatabaseIdentity, store.AnalysisRunning, building); err != nil {
			return err
		}
		built, err := e.Crawl.buildDatabase(ctx, member.DatabaseIdentity,
			filepath.Join(dir, cacheName(member.Repository)), planned.cliVersion)
		e.report(member.Repository, "build", err)
		if err != nil {
			return e.unbuilt(ctx, runID, member.DatabaseIdentity, err)
		}
		defer built.release()
		if err := e.Store.SetAnalysisMemberDatabase(
			runID, member.DatabaseIdentity, built.identity, built.kept); err != nil {
			return err
		}
		member.DatabaseIdentity, member.DatabasePath = built.identity, built.path
	}
	if err := e.analyze(ctx, runID, dir, member, want, planned, hold); err != nil {
		if ctx.Err() != nil {
			return e.interrupted(runID, member.DatabaseIdentity, err)
		}
		return err
	}
	return nil
}

// skipAnswered records a member whose evaluation is already stored as skipped, carrying that
// answer. The run key covers the query's version over its whole import closure, so an edit to the
// query or anything it imports is never skipped.
func (e *Executor) skipAnswered(
	runID int64, member store.AnalysisMember, held bool,
	want []store.AnalysisQuery, planned tierPlan,
) (bool, error) {
	at := member.DatabaseIdentity
	if !held {
		// A repository with no commit on record names no evaluation until it is fetched.
		if at.Commit == "" {
			return false, nil
		}
		at.SourceFilter = e.Crawl.sourceFilter()
	}
	from, answer, found, err := e.Store.AnsweredBy(memberRun(store.AnalysisMember{
		DatabaseIdentity: at,
	}, want, planned))
	if err != nil || !found {
		return false, err
	}
	if at != member.DatabaseIdentity {
		if err := e.Store.SetAnalysisMemberDatabase(runID, member.DatabaseIdentity, at, ""); err != nil {
			return false, err
		}
	}
	answer.Status = store.AnalysisSkipped
	answer.Detail = fmt.Sprintf(
		"already analyzed at this commit with these query versions, pack and CLI, in run %d", from)
	return true, e.Store.RecordAnalysisMemberResult(runID, at, answer)
}

// unbuilt records why a member's database could not be built, unless the run was stopped.
func (e *Executor) unbuilt(
	ctx context.Context, runID int64, id store.DatabaseIdentity, cause error,
) error {
	if ctx.Err() != nil {
		return e.interrupted(runID, id, ctx.Err())
	}
	return e.Store.RecordAnalysisMemberResult(runID, id, store.MemberResult{
		Status: store.AnalysisFailed, Detail: "building its database: " + cause.Error(),
	})
}

// interrupted puts a member the run was stopped during back to pending, which can be started again.
func (e *Executor) interrupted(runID int64, id store.DatabaseIdentity, cause error) error {
	if err := e.Store.SetAnalysisMemberStatus(
		runID, id, store.AnalysisPending, "stopped before it finished"); err != nil {
		return err
	}
	return cause
}

// onDisk reports whether a recorded database is still there; eviction can take one at any time.
func onDisk(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// builtDatabase is a database built for one run member.
type builtDatabase struct {
	identity store.DatabaseIdentity
	path     string
	// kept is where a later run finds it, and empty when nothing retains it.
	kept    string
	release func()
}

// buildDatabase builds a regular database the way a round does: through the checkout cache, under
// the runner's lock, and retained when a cache is configured. What is built can differ from what
// was asked in its commit, when none is on record, and in its filter, which is the one fetched now.
func (e *Expander) buildDatabase(
	ctx context.Context, want store.DatabaseIdentity, dir, cliVersion string,
) (builtDatabase, error) {
	if want.Mode != store.AnalysisModeRegular {
		return builtDatabase{}, fmt.Errorf("a run builds only regular databases, not %s ones", want.Mode)
	}
	owner, repo, ok := splitRepository(want.Repository)
	if !ok {
		return builtDatabase{}, fmt.Errorf("%q does not name a repository", want.Repository)
	}
	built := builtDatabase{release: func() { os.RemoveAll(dir) }}
	fail := func(err error) (builtDatabase, error) {
		built.release()
		return builtDatabase{}, err
	}

	b := batch{owner: owner, repo: repo, ref: want.Commit}
	tree, retained, err := e.materialize(
		ctx, b, filepath.Join(dir, "src"), want.Commit, want.Commit != "")
	if err != nil {
		return fail(err)
	}
	built.identity = store.DatabaseIdentity{
		Repository: want.Repository, Commit: tree.SHA, SourceFilter: e.sourceFilter(), Mode: want.Mode,
	}
	db, staged, reused, err := e.databaseFor(ctx, built.identity, filepath.Join(dir, "db"), tree, cliVersion)
	discardTree(Options{}, tree, retained)
	if err != nil {
		return fail(err)
	}
	built.path = db
	switch {
	case e.Databases == nil:
		return built, nil
	case reused:
		if _, err := e.Store.UseDatabase(built.identity); err != nil {
			return fail(err)
		}
		built.kept = db
		return built, nil
	}
	held, bytes, err := e.Databases.Retain(staged, built.identity)
	if err != nil {
		e.Databases.DiscardStaged(staged)
		return fail(err)
	}
	if _, err := e.Store.RetainDatabase(store.RetainedDatabase{
		DatabaseIdentity: built.identity, Path: held, Bytes: bytes, CLIVersion: cliVersion,
	}); err != nil {
		return fail(err)
	}
	built.path, built.kept = held, held
	return built, nil
}

// analyze runs one member and records what became of it.
func (e *Executor) analyze(
	ctx context.Context, runID int64, dir string, member store.AnalysisMember,
	want []store.AnalysisQuery, planned tierPlan, hold bool,
) error {
	if err := e.Store.SetAnalysisMemberStatus(
		runID, member.DatabaseIdentity, store.AnalysisRunning, ""); err != nil {
		return err
	}

	// One file per member, named by the repository rather than by an index, so a directory found
	// on its own says what is in it.
	out := filepath.Join(dir, cacheName(member.Repository)+".sarif")
	err := e.Analyzer.AnalyzeQueries(ctx, member.DatabasePath, planned.findings, out)
	e.report(member.Repository, "analyze", err)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return e.Store.RecordAnalysisMemberResult(runID, member.DatabaseIdentity,
			store.MemberResult{Status: store.AnalysisFailed, Detail: err.Error()})
	}
	if hold {
		return e.hold(runID, out, member, want, planned)
	}

	// Ingested into the same tables the crawl writes, under a ledger row built the same way. A
	// result that landed anywhere else would need every reader taught about a second source.
	found, byQuery, queryRunID, ingestErr := e.ingest(out, member, want, planned)
	e.report(member.Repository, "ingest", ingestErr)
	result := store.MemberResult{
		Status:     store.AnalysisSucceeded,
		Detail:     detailOf(ingestErr),
		ResultPath: out,
		Results:    found,
		Counted:    ingestErr == nil,
		ByQuery:    byQuery,
	}
	// Linked only once every finding is stored, so a listing is never of half an ingest.
	if ingestErr == nil {
		result.QueryRunID = queryRunID
	}
	return e.Store.RecordAnalysisMemberResult(runID, member.DatabaseIdentity, result)
}

// ingest records the evaluation and stores what it found, returning the count in the SARIF and
// the ledger row it was stored under.
//
// The count is what the document holds, not what the write returned: findings are unique on their
// location and fingerprint, so a rule that has fired here before stores nothing and a member that
// reported two results would otherwise read as having found none.
func (e *Executor) ingest(
	path string, member store.AnalysisMember, want []store.AnalysisQuery, planned tierPlan,
) (int, map[string]int, int64, error) {
	runID, err := e.Store.RecordQueryRun(memberRun(member, want, planned))
	if err != nil {
		return 0, nil, 0, fmt.Errorf("recording the evaluation of %s: %w", member.Repository, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, nil, 0, fmt.Errorf("reading the results at %s: %w", path, err)
	}
	defer f.Close()

	found := 0
	// Every query asked for starts at zero, so one that looked and found nothing says so.
	byQuery := make(map[string]int, len(want))
	for _, q := range want {
		byQuery[q.QueryID] = 0
	}
	if err := sarifin.Stream(f, sarifin.Handler{
		OnResult: func(r sarifin.Result) error {
			found++
			byQuery[r.RuleID]++
			return nil
		},
	}); err != nil {
		return 0, nil, 0, fmt.Errorf("reading the results at %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, nil, 0, fmt.Errorf("re-reading the results at %s: %w", path, err)
	}
	if _, err := e.Ingester.FindingsFromSARIF(f, ingest.Options{
		Repository: member.Repository, QueryRunID: runID, CLIVersion: planned.cliVersion,
	}); err != nil {
		return 0, nil, 0, fmt.Errorf("storing the results of %s: %w", member.Repository, err)
	}
	return found, byQuery, runID, nil
}

// hold keeps what a member's document reports as the run's held results, under an evaluation
// marked as held: it answers no skip, and only what somebody converts is ever sighted under it.
func (e *Executor) hold(
	runID int64, path string, member store.AnalysisMember, want []store.AnalysisQuery, planned tierPlan,
) error {
	evaluation := memberRun(member, want, planned)
	evaluation.Held = true
	queryRunID, err := e.Store.RecordQueryRun(evaluation)
	var held []store.Finding
	if err == nil {
		held, err = readHeld(path, member, planned)
	}
	e.report(member.Repository, "hold", err)
	result := store.MemberResult{Status: store.AnalysisSucceeded, Detail: detailOf(err), ResultPath: path}
	if err == nil {
		result.Counted, result.Results, result.QueryRunID = true, len(held), queryRunID
		// Every query asked for starts at zero, so one that reported nothing says so.
		result.ByQuery = make(map[string]int, len(want))
		for _, q := range want {
			result.ByQuery[q.QueryID] = 0
		}
		for _, f := range held {
			result.ByQuery[f.RuleID]++
		}
	}
	return e.Store.RecordHeldMemberResult(runID, member.DatabaseIdentity, result, held)
}

func readHeld(path string, member store.AnalysisMember, planned tierPlan) ([]store.Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading the results at %s: %w", path, err)
	}
	defer f.Close()
	return ingest.HeldFromSARIF(f, ingest.Options{
		Repository: member.Repository, CLIVersion: planned.cliVersion,
	})
}

// memberRun is the ledger row for analyzing one member.
//
// Keyed on the query *ids* the request named rather than on the paths the crawl's lane joins: the
// catalog and `abom_analysis_query` both speak ids, and a row keyed on a path could not be
// attributed to the version somebody asked to run. Identity comes from the crawl's own
// `identifyFindings`, because a second implementation of it would drift.
func memberRun(
	member store.AnalysisMember, want []store.AnalysisQuery, planned tierPlan,
) store.QueryRun {
	ids := make([]string, 0, len(want))
	for _, q := range want {
		ids = append(ids, q.QueryID)
	}
	return planned.identifyFindings(store.QueryRun{
		QueryID:      strings.Join(ids, " "),
		Repository:   member.Repository,
		DBCommit:     member.Commit,
		SourceFilter: member.SourceFilter,
		AnalysisMode: member.Mode,
		RecordedBy:   store.RecordedByRun,
	})
}

// detailOf reports why a member that ran carries no count, and says nothing when it does.
func detailOf(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// resolve turns the query ids a run names into a plan that can be executed and attributed.
//
// Refused rather than partially satisfied: a run is a request for a specific set of queries, and
// carrying out most of it while reporting success would attribute a result to something that
// never ran.
func (e *Executor) resolve(ctx context.Context, want []store.AnalysisQuery) (tierPlan, error) {
	byID := map[string]string{}
	declared := map[string]map[string]string{}
	for _, suite := range e.Suites {
		resolved, err := e.Resolver.ResolveQueries(ctx, suite)
		if err != nil {
			return tierPlan{}, fmt.Errorf("resolving %s: %w", suite, err)
		}
		for _, path := range resolved {
			md, err := e.Resolver.ResolveMetadata(ctx, path)
			if err != nil {
				return tierPlan{}, fmt.Errorf("reading %s: %w", path, err)
			}
			if _, seen := byID[md.ID]; !seen {
				byID[md.ID] = path
				declared[md.ID] = md.Pairs
			}
		}
	}

	var planned tierPlan
	// Refused rather than recorded with no CLI version, for the reason the crawl refuses: a
	// ledger row that omits what evaluated it cannot be told from one a different CodeQL wrote,
	// and a later skip would trust it.
	cli, err := e.Resolver.Version(ctx)
	if err != nil {
		return tierPlan{}, err
	}
	planned.cliVersion = cli

	paths := make([]string, len(want))
	for i, q := range want {
		path, ok := byID[q.QueryID]
		if !ok {
			return tierPlan{}, fmt.Errorf(
				"no suite this run can see offers %q; it is in %d queries across %v",
				q.QueryID, len(byID), e.Suites)
		}
		paths[i] = path
	}
	installed := false
	// This run's own: a pack edited since an earlier run is digested again, as its run key needs.
	var digests runner.DigestCache
	for i, q := range want {
		path := paths[i]
		pack, packVersion, packDir := runner.PackOf(path)
		if err := installBeforeDigest(ctx, e.Install, pack, &installed); err != nil {
			return tierPlan{}, err
		}
		digest, err := digests.Of(packDir)
		if err != nil {
			return tierPlan{}, fmt.Errorf("identifying %s: %w", q.QueryID, err)
		}
		// A path that is not there leaves the version empty rather than failing the run, the
		// same way a round's does: the version is provenance, not a precondition.
		version, err := runner.QueryVersion(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return tierPlan{}, fmt.Errorf("versioning %s: %w", q.QueryID, err)
		}
		planned.findings = append(planned.findings, path)
		planned.findingsVersions = append(planned.findingsVersions, version)
		planned.findingsRules = append(planned.findingsRules, store.QueryRunRule{
			RuleID: q.QueryID, QueryVersion: version, Pack: pack, PackVersion: packVersion, PackDigest: digest,
		})
		planned.notePack(pack, packVersion, digest)
		planned.metadata = append(planned.metadata, store.QueryMetadata{
			QueryID: q.QueryID, QueryVersion: version, Pairs: declared[q.QueryID],
		})
	}
	return planned, nil
}

func (e *Executor) report(repository, stage string, err error) {
	if e.OnEvent != nil {
		e.OnEvent(repository, stage, err)
	}
}

// cacheName makes a repository safe to use as a file name, refusing nothing: unlike a cache key
// this only has to be unique and readable, and the separator is the one thing that must not
// survive.
func cacheName(repository string) string {
	name := make([]rune, 0, len(repository))
	for _, r := range repository {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			name = append(name, r)
		default:
			name = append(name, '_')
		}
	}
	return string(name)
}
