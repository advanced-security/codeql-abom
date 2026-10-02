package expand

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/closure"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// IndexingBuilder builds a database that also indexes paths the extractor's defaults leave out,
// which a staged reusable workflow needs: workflows are indexed only at the source root.
type IndexingBuilder interface {
	CreateDatabaseIndexing(ctx context.Context, srcDir, dbDir, language string, includes []string) error
}

// ClosureOptions configures one closure database.
type ClosureOptions struct {
	WorkDir       string
	KeepArtifacts bool
}

// ClosureReport says what one closure database was built from and what it found.
type ClosureReport struct {
	Root     string
	Commit   string
	Digest   string
	Staged   []closure.Staged
	Skipped  []closure.Skipped
	Unstaged []ClosureGap
	Anchored bool
	// Omitted are files of the root left out because they lead outside its checkout.
	Omitted []string
	// AlreadyAnalyzed is a closure whose identity is on record; nothing was built.
	AlreadyAnalyzed bool
	QueryRunID      int64

	Extract time.Duration
	Analyze time.Duration
	Tally   store.ClosureTally
}

// ClosureGap is a member of the closure that could not be considered for staging at all.
type ClosureGap struct {
	Uses   string
	Ref    string
	Reason string
}

// BuildClosure builds one owned repository's closure database, analyzes it, and records what it
// found as extended evidence.
//
// It reads the crawl and adds nothing to it: what to stage comes from the submission closure the
// regular graph describes, at the commits the crawl analyzed or a lock selects, and every row it
// writes is a finding seen through an extended run. Nothing is queued, and no edge, node or
// repository is written: every existing total reads regular evidence and must not move.
func (e *Expander) BuildClosure(ctx context.Context, root string, opts ClosureOptions) (ClosureReport, error) {
	report := ClosureReport{Root: root}
	builder, ok := e.Builder.(IndexingBuilder)
	if !ok {
		return report, errors.New("expand: a closure database needs a builder that indexes staged workflows")
	}
	if opts.WorkDir == "" {
		return report, errors.New("expand: a closure needs a work directory")
	}
	name, commit, err := e.closureRoot(root)
	if err != nil {
		return report, err
	}
	report.Root, report.Commit = name, commit

	plans, err := e.resolveTiers(ctx, Options{}, false)
	if err != nil {
		return report, err
	}
	planned := plans[store.TierOwned]
	if len(planned.findings) == 0 {
		return report, fmt.Errorf("the owned tier binds no findings suite, so a closure of %s "+
			"would be built to answer nothing", name)
	}

	dir := filepath.Join(opts.WorkDir, "closure-"+cacheName(name))
	if err := os.RemoveAll(dir); err != nil {
		return report, err
	}
	if !opts.KeepArtifacts {
		defer os.RemoveAll(dir)
	}

	trees := &closureTrees{e: e, dir: filepath.Join(dir, "trees")}
	defer trees.release()
	layout, err := e.stageClosure(ctx, &report, trees)
	if err != nil {
		return report, err
	}

	run := planned.identifyFindings(store.QueryRun{
		QueryID: strings.Join(planned.findings, " "), Repository: name, DBCommit: commit,
		SourceFilter: e.sourceFilter(),
		// The one shape that is not a single repository's checkout.
		AnalysisMode: store.AnalysisModeExtended,
		Closure:      report.Digest,
		RecordedBy:   store.RecordedByClosure,
	})
	// The manifest is recorded last, once the findings are in, so a closure whose ingest failed
	// halfway is built again rather than skipped with half its results.
	done, err := e.Store.QueryRunExists(run)
	if err != nil {
		return report, err
	}
	if _, recorded, err := e.Store.ClosureManifestOf(report.Digest); err != nil {
		return report, err
	} else if done && recorded {
		report.AlreadyAnalyzed = true
		return report, nil
	}

	src, db := filepath.Join(dir, "src"), filepath.Join(dir, "db")
	if err := layout.Write(src); err != nil {
		return report, err
	}
	report.Omitted = layout.Omitted
	started := time.Now()
	if err := builder.CreateDatabaseIndexing(ctx, src, db, "actions", closure.IndexIncludes); err != nil {
		return report, fmt.Errorf("building the closure of %s: %w", name, err)
	}
	report.Extract = time.Since(started)

	sarif := filepath.Join(dir, "findings.sarif")
	started = time.Now()
	if err := e.Analyzer.AnalyzeQueries(ctx, db, planned.findings, sarif); err != nil {
		return report, fmt.Errorf("analyzing the closure of %s: %w", name, err)
	}
	report.Analyze = time.Since(started)

	return report, e.recordClosure(&report, layout, run, sarif, planned.cliVersion)
}

// closureRoot is the owned repository as the corpus spells it, at the commit its regular analysis
// describes. The closure is computed from that analysis, so any other commit would stage the
// callees of workflows the database does not hold.
func (e *Expander) closureRoot(root string) (string, string, error) {
	owned, err := e.Store.RepositoryIsOwned(root)
	if err != nil {
		return "", "", err
	}
	if !owned {
		return "", "", fmt.Errorf("%s is not inside the declared scope; a closure is rooted at a "+
			"repository somebody owns", root)
	}
	_, name, ok, err := e.Store.RepositoryNamed(root)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("%s is not a repository the corpus holds", root)
	}
	commit, ok, err := e.Store.AnalyzedCommit(name, "")
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("%s has no analyzed commit, and a closure is rooted at the one "+
			"its regular analysis describes", name)
	}
	return name, commit, nil
}

// stageClosure decides what the closure holds and fetches it, naming everything left out.
func (e *Expander) stageClosure(
	ctx context.Context, report *ClosureReport, trees *closureTrees,
) (closure.Layout, error) {
	members, gaps, err := e.closureMembers(report.Root)
	if err != nil {
		return closure.Layout{}, err
	}
	rootTree, err := trees.at(ctx, report.Root, report.Commit)
	if err != nil {
		return closure.Layout{}, fmt.Errorf("fetching %s at %s: %w", report.Root, report.Commit, err)
	}
	fetched := make([]closure.Member, 0, len(members))
	for _, m := range members {
		tree, err := trees.at(ctx, m.Repository, m.Commit)
		if err != nil {
			gaps = append(gaps, ClosureGap{Uses: m.Uses, Ref: m.Commit, Reason: err.Error()})
			continue
		}
		m.Tree = tree
		fetched = append(fetched, m)
	}
	report.Unstaged = gaps
	layout, skipped, err := closure.Plan(
		closure.Root{Repository: report.Root, Commit: report.Commit, Tree: rootTree}, fetched)
	if err != nil {
		return closure.Layout{}, fmt.Errorf("staging the closure of %s: %w", report.Root, err)
	}
	report.Staged, report.Skipped, report.Anchored = layout.Staged, skipped, layout.Anchored
	report.Digest = layout.Digest()
	return layout, nil
}

// closureMembers is every dependency the owned repository's submission closure reaches, at the
// commit the closure selects: the one its lock chose, a commit the reference already is, or the
// one the crawl analyzed the reference at. One with none is named, never guessed.
func (e *Expander) closureMembers(root string) ([]closure.Member, []ClosureGap, error) {
	entries, err := e.Store.SubmissionClosure(root)
	if err != nil {
		return nil, nil, err
	}
	members := []closure.Member{}
	gaps := []ClosureGap{}
	seen := map[string]bool{}
	for _, entry := range entries {
		uses := entry.Owner + "/" + entry.Repo
		if entry.Subpath != "" {
			uses += "/" + entry.Subpath
		}
		key := strings.ToLower(uses + "@" + entry.Ref + "@" + entry.LockedCommit)
		if seen[key] {
			continue
		}
		seen[key] = true
		if entry.Owner == "" || entry.Repo == "" {
			gaps = append(gaps, ClosureGap{Uses: entry.Subpath, Ref: entry.Ref,
				Reason: "names no repository: a container image, or a path inside a dependency, " +
					"which nothing staged can resolve"})
			continue
		}
		commit, err := e.selectedCommit(entry, uses)
		if err != nil {
			return nil, nil, err
		}
		if commit == "" {
			gaps = append(gaps, ClosureGap{Uses: uses, Ref: entry.Ref,
				Reason: "no lock selects a commit and the crawl has not analyzed this reference"})
			continue
		}
		_, name, ok, err := e.Store.RepositoryNamed(entry.Owner + "/" + entry.Repo)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			gaps = append(gaps, ClosureGap{Uses: uses, Ref: entry.Ref,
				Reason: "not a repository the corpus holds"})
			continue
		}
		members = append(members, closure.Member{Repository: name, Uses: uses, Commit: commit})
	}
	return members, gaps, nil
}

func (e *Expander) selectedCommit(entry store.ClosureEntry, uses string) (string, error) {
	switch {
	case entry.LockedCommit != "":
		return strings.ToLower(entry.LockedCommit), nil
	case isCommit(entry.Ref):
		return strings.ToLower(entry.Ref), nil
	}
	commit, _, err := e.Store.AnalyzedCommit(strings.ToLower(uses), entry.Ref)
	return commit, err
}

// commitID is a full commit id as the store keeps one: lower case, as git writes it.
var commitID = regexp.MustCompile(`^[0-9a-f]{40}$`)

// closureTrees fetches each repository the closure holds once per commit, through the checkout
// cache when one is configured, and releases what the cache does not keep.
type closureTrees struct {
	e        *Expander
	dir      string
	held     map[string]string
	releases []func()
}

func (t *closureTrees) at(ctx context.Context, repository, commit string) (string, error) {
	key := strings.ToLower(repository + "@" + commit)
	if dir, ok := t.held[key]; ok {
		return dir, nil
	}
	owner, repo, ok := splitRepository(repository)
	// Both are read from the store, which a malformed reference could have reached, and both become
	// directories: only names that stay where they are put, at a full commit id, are fetched.
	if !ok || !filepath.IsLocal(owner) || !filepath.IsLocal(repo) || !commitID.MatchString(commit) {
		return "", fmt.Errorf("%q at %q does not name a repository at a commit", repository, commit)
	}
	dest := filepath.Join(t.dir, cacheName(owner+"/"+repo)+"-"+commit, "src")
	tree, retained, err := t.e.materialize(ctx, batch{owner: owner, repo: repo, ref: commit}, dest,
		commit, true)
	if err != nil {
		return "", err
	}
	t.releases = append(t.releases, func() { discardTree(Options{}, tree, retained) })
	if !strings.EqualFold(tree.SHA, commit) {
		return "", fmt.Errorf("fetching %s at %s checked out %s", repository, commit, tree.SHA)
	}
	if t.held == nil {
		t.held = map[string]string{}
	}
	t.held[key] = tree.Dir
	return tree.Dir, nil
}

func (t *closureTrees) release() {
	for _, release := range t.releases {
		release()
	}
}

// attribution adapts the layout's attribution to what ingestion takes.
func attribution(layout closure.Layout) func(string) (ingest.Located, error) {
	return func(p string) (ingest.Located, error) {
		at, err := layout.Attribute(p)
		return ingest.Located{Repository: at.Repository, Path: at.Path, Excluded: at.Excluded}, err
	}
}

// recordClosure stores what the closure found as extended evidence: the run, its findings
// attributed to the repositories that hold their files, and last the manifest that marks it done.
func (e *Expander) recordClosure(
	report *ClosureReport, layout closure.Layout, run store.QueryRun, sarif, cli string,
) error {
	runID, err := e.Store.RecordQueryRun(run)
	if err != nil {
		return err
	}
	f, err := os.Open(sarif)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := e.Ingester.FindingsFromSARIF(f, ingest.Options{
		Repository: report.Root, QueryRunID: runID, CLIVersion: cli, Attribute: attribution(layout),
	}); err != nil {
		return fmt.Errorf("storing what the closure of %s found: %w", report.Root, err)
	}
	// The join is over every finding, and a finding stored under its own repository's path is
	// what lets it attach to that repository's nodes at all.
	if _, err := e.Store.LinkFindingsToNodes(); err != nil {
		return err
	}
	manifest := store.ClosureManifest{
		Digest: report.Digest, Root: report.Root, RootCommit: report.Commit,
		Anchored: layout.Anchored, Members: []store.ClosureMember{},
	}
	for _, s := range layout.Staged {
		manifest.Members = append(manifest.Members, store.ClosureMember{
			Repository: s.Repository, Commit: s.Commit, Kind: string(s.Kind), Path: s.Path,
			StagedPath: s.StagedPath,
		})
	}
	if err := e.Store.RecordClosure(manifest); err != nil {
		return err
	}
	report.QueryRunID = runID
	report.Tally, err = e.Store.ClosureTallyOf(runID)
	return err
}
