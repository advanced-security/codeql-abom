package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/advanced-security/codeql-abom/internal/closure"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/store"
)

type closureConfig struct {
	dbPath, codeqlPath, workDir, cacheDir string
	cacheMaxMB                            int64
	roots                                 repeated
	keepArtifacts                         bool
}

// runClosure builds one closure database per owned repository and records what each found as
// extended evidence, which no existing total reads.
func runClosure(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseClosureArgs(args, stderr)
	if err != nil {
		return err
	}
	dir, cleanup, err := workingDirectory(cfg.workDir, "abom-closure-*")
	if err != nil {
		return err
	}
	defer cleanup()

	s, err := store.Open(cfg.dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", cfg.dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", cfg.dbPath, err)
	}

	roots := []string(cfg.roots)
	if len(roots) == 0 {
		if roots, err = s.OwnedRepositories(); err != nil {
			return fmt.Errorf("closure: %w", err)
		}
	}
	if len(roots) == 0 {
		return errors.New("closure: nothing is owned, and a closure is rooted at an owned repository")
	}

	tokens, err := dbReaderTokens(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("closure: %w", err)
	}
	e, cache, release, err := newExpander(s, nil, expandAllConfig{
		codeqlPath: cfg.codeqlPath, cacheDir: cfg.cacheDir, cacheMaxMB: cfg.cacheMaxMB, tokens: tokens,
	}, stdout)
	if err != nil {
		return fmt.Errorf("closure: %w", err)
	}
	defer release()
	defer cache.Release()

	opts := expand.ClosureOptions{WorkDir: dir, KeepArtifacts: cfg.keepArtifacts}
	failed := 0
	for _, root := range roots {
		report, err := e.BuildClosure(ctx, root, opts)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// One closure that cannot be built says why and the rest carry on, so a refusal reads
			// as a refusal rather than as an organization with nothing in it.
			failed++
			fmt.Fprintf(stdout, "closure: %s refused: %v\n", root, err)
			continue
		}
		printClosureReport(stdout, report)
	}
	if failed > 0 {
		return fmt.Errorf("closure: %d of %d closures were not built", failed, len(roots))
	}
	return nil
}

func parseClosureArgs(args []string, stderr io.Writer) (closureConfig, error) {
	var cfg closureConfig
	fs := flag.NewFlagSet("closure", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.dbPath, "db", "", "path to the ABOM SQLite database (required)")
	fs.StringVar(&cfg.codeqlPath, "codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	fs.StringVar(&cfg.workDir, "work-dir", "", "scratch directory for closure trees (default: a temp dir)")
	fs.StringVar(&cfg.cacheDir, "cache-dir", "",
		"reuse and retain fetched checkouts here, keyed by commit (default: fetch each one)")
	fs.Int64Var(&cfg.cacheMaxMB, "cache-max-mb", defaultCacheMaxMB,
		"ceiling for retained checkouts, least recently used evicted first (0: unbounded)")
	fs.Var(&cfg.roots, "repo",
		"an owned repository to root a closure at; repeatable, defaults to every owned repository")
	fs.BoolVar(&cfg.keepArtifacts, "keep-artifacts", false, "keep each closure's tree and database")
	if err := fs.Parse(args); err != nil {
		return closureConfig{}, err
	}
	if cfg.dbPath == "" {
		fs.Usage()
		return closureConfig{}, errors.New("closure: --db is required")
	}
	return cfg, nil
}

// printClosureReport says what one closure held, what it cost and what it found, naming every
// member it left out and why.
func printClosureReport(w io.Writer, r expand.ClosureReport) {
	fmt.Fprintf(w, "closure: %s@%s %s\n", r.Root, short(r.Commit), short(r.Digest))
	kinds := map[closure.Kind]int{}
	for _, s := range r.Staged {
		kinds[s.Kind]++
	}
	fmt.Fprintf(w, "  staged %d (%d composite, %d reusable workflow), skipped %d, unstaged %d%s\n",
		len(r.Staged), kinds[closure.Composite], kinds[closure.ReusableWorkflow], len(r.Skipped),
		len(r.Unstaged), anchoredNote(r.Anchored))
	for _, s := range r.Staged {
		fmt.Fprintf(w, "    staged %s@%s as %s\n", s.Uses, short(s.Commit), s.StagedPath)
	}
	for _, reason := range tallyReasons(r) {
		fmt.Fprintf(w, "    %s\n", reason)
	}
	if r.AlreadyAnalyzed {
		fmt.Fprintln(w, "  already analyzed with these inputs, packs and CodeQL; nothing was built")
		return
	}
	for _, p := range r.Omitted {
		fmt.Fprintf(w, "    left out %s, which leads outside the checkout\n", p)
	}
	fmt.Fprintf(w, "  built in %s, analyzed in %s\n", seconds(r.Extract), seconds(r.Analyze))
	t := r.Tally
	fmt.Fprintf(w, "  findings: %d (%d already reported regularly, %d only through the closure), "+
		"%d attached to no node\n", t.Findings, t.Shared, t.ExtendedOnly(), t.Unattached)
}

// tallyReasons groups why members were left out, so a closure of forty actions prints a handful
// of lines rather than forty.
func tallyReasons(r expand.ClosureReport) []string {
	counts := map[string]int{}
	for _, s := range r.Skipped {
		counts["skipped: "+s.Reason]++
	}
	for _, g := range r.Unstaged {
		counts["unstaged: "+g.Reason]++
	}
	out := make([]string, 0, len(counts))
	for reason, n := range counts {
		out = append(out, fmt.Sprintf("%d %s", n, reason))
	}
	sort.Strings(out)
	return out
}

func anchoredNote(anchored bool) string {
	if anchored {
		return ", anchored with a synthesized workflow"
	}
	return ""
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }
