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
	CodeQL   QueryRunner
	// Ingester is what makes a run's results mean anything to the rest of ABOM. Without it the
	// SARIF is a file on disk and the graph, the Inventory and the gate know nothing about it.
	Ingester *ingest.Ingester
	// Suites are where a query id is resolved to a path. A run names ids because that is what a
	// catalog holds; only the CLI can say which file an id came from.
	Suites []string
	// WorkDir is where each member's raw output is written, outside any scratch directory that a
	// round would remove.
	WorkDir string
	OnEvent func(repository, stage string, err error)

	digests runner.DigestCache
}

// Run analyzes every member whose database is on disk, and says what became of each.
//
// A member that fails does not stop the others: stopping would discard the work already done and
// leave a reader unable to tell a broken query from a broken repository. A member with nothing on
// disk is reported rather than left pending, so a complete run never reads as a clean result for
// a repository nothing ran against.
func (e *Executor) Run(ctx context.Context, runID int64) error {
	run, err := e.Store.AnalysisRun(runID)
	if err != nil {
		return err
	}

	// Resolved before anything runs. A run that analyzed the queries it could find and quietly
	// dropped the rest would report a result for a request that was not carried out.
	planned, err := e.resolve(ctx, run.Queries)
	if err != nil {
		return err
	}

	if err := e.Store.SetAnalysisRunState(runID, store.RunRunning); err != nil {
		return err
	}
	dir := filepath.Join(e.WorkDir, fmt.Sprintf("run-%d", runID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("preparing %s: %w", dir, err)
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
		if member.DatabasePath == "" {
			detail := member.Detail
			if detail == "" {
				detail = "no database on disk when the run reached it"
			}
			if err := e.Store.SetAnalysisMemberStatus(
				runID, member.DatabaseIdentity, store.AnalysisNoDatabase, detail); err != nil {
				return err
			}
			continue
		}
		if err := e.analyze(ctx, runID, dir, member, run.Queries, planned); err != nil {
			return err
		}
	}

	// Attribution is what makes a finding visible to anything built on the graph, and the crawl
	// does it once at the end of a cycle for the same reason: the join is over the whole table,
	// so doing it per member would repeat the same work for every repository in the run.
	if _, err := e.Store.LinkFindingsToNodes(); err != nil {
		return fmt.Errorf("attributing what run %d found: %w", runID, err)
	}
	return e.Store.SetAnalysisRunState(runID, store.RunComplete)
}

// analyze runs one member and records what became of it.
func (e *Executor) analyze(
	ctx context.Context, runID int64, dir string, member store.AnalysisMember,
	want []store.AnalysisQuery, planned tierPlan,
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

	// Ingested into the same tables the crawl writes, under a ledger row built the same way. A
	// result that landed anywhere else would need every reader taught about a second source.
	found, ingestErr := e.ingest(out, member, want, planned)
	e.report(member.Repository, "ingest", ingestErr)
	return e.Store.RecordAnalysisMemberResult(runID, member.DatabaseIdentity, store.MemberResult{
		Status:     store.AnalysisSucceeded,
		Detail:     detailOf(ingestErr),
		ResultPath: out,
		Results:    found,
		Counted:    ingestErr == nil,
	})
}

// ingest records the evaluation and stores what it found, returning the count in the SARIF.
//
// The count is what the document holds, not what the write returned: findings are unique on their
// location and fingerprint, so a rule that has fired here before stores nothing and a member that
// reported two results would otherwise read as having found none.
func (e *Executor) ingest(
	path string, member store.AnalysisMember, want []store.AnalysisQuery, planned tierPlan,
) (int, error) {
	runID, err := e.Store.RecordQueryRun(memberRun(member, want, planned))
	if err != nil {
		return 0, fmt.Errorf("recording the evaluation of %s: %w", member.Repository, err)
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("reading the results at %s: %w", path, err)
	}
	defer f.Close()

	found := 0
	if err := sarifin.Stream(f, sarifin.Handler{
		OnResult: func(sarifin.Result) error { found++; return nil },
	}); err != nil {
		return 0, fmt.Errorf("reading the results at %s: %w", path, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return 0, fmt.Errorf("re-reading the results at %s: %w", path, err)
	}
	if _, err := e.Ingester.FindingsFromSARIF(f, ingest.Options{
		Repository: member.Repository, QueryRunID: runID, CLIVersion: planned.cliVersion,
	}); err != nil {
		return 0, fmt.Errorf("storing the results of %s: %w", member.Repository, err)
	}
	return found, nil
}

// memberRun is the ledger row for analyzing one member.
//
// Keyed on the query *ids* the request named rather than on the paths the crawl's own lane joins:
// the catalog and `abom_analysis_query` both speak ids, and a row keyed on a path could not be
// attributed to the version somebody asked to run. Identity comes from `identifyFindings`, which
// is the crawl's, because `database analyze` is one invocation over many queries here too and two
// implementations of that would drift.
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

	for _, q := range want {
		path, ok := byID[q.QueryID]
		if !ok {
			return tierPlan{}, fmt.Errorf(
				"no suite this run can see offers %q; it is in %d queries across %v",
				q.QueryID, len(byID), e.Suites)
		}
		pack, packVersion, packDir := runner.PackOf(path)
		digest, err := e.digests.Of(packDir)
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
		planned.notePack(pack, packVersion, digest)
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
