package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/ingest"
	"github.com/advanced-security/codeql-abom/internal/pipeline"
	"github.com/advanced-security/codeql-abom/internal/runner"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// defaultEdgesQuery is the query that turns a database into ABOM dependency edges. It is a
// @kind table query, not a @kind problem one, so it has to be run locally rather than through
// the SARIF-only MRVA backend.
const defaultEdgesQuery = "ql/actions/abom/src/AbomEdges.ql"

// defaultEdgesQueryID identifies the query in the ingest ledger, matching its own @id.
const defaultEdgesQueryID = "abom/edges"

// runBuild fetches a database for every cataloged repository, runs the edges query against it
// and ingests the result.
func runBuild(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := fs.String("db", "", "path to the ABOM SQLite database (required)")
	language := fs.String("language", "actions", "CodeQL language of the databases to fetch")
	queryPath := fs.String("query", defaultEdgesQuery, "edges query to run against each database")
	queryID := fs.String("query-id", defaultEdgesQueryID, "ledger identity for the query")
	apiURL := fs.String("api-url", defaultAPIURL(), "GitHub API base URL")
	codeqlPath := fs.String("codeql", "", "codeql CLI to invoke (default: codeql on PATH)")
	workDir := fs.String("work-dir", "", "scratch directory for downloads (default: a temp dir)")
	concurrency := fs.Int("concurrency", defaultConcurrency, "repositories processed at once")
	extractorVersion := fs.String("extractor-version", "",
		"invalidates the ingest ledger when result interpretation changes")
	keepArtifacts := fs.Bool("keep-artifacts", false, "keep downloaded databases and results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("build: --db is required")
	}

	dir, cleanup, err := workingDirectory(*workDir, "abom-build-*")
	if err != nil {
		return err
	}
	defer cleanup()

	s, err := store.Open(*dbPath, store.Options{BulkLoad: true})
	if err != nil {
		return fmt.Errorf("opening %s: %w", *dbPath, err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", *dbPath, err)
	}

	repos, err := s.CatalogRepositories(*language)
	if err != nil {
		return fmt.Errorf("build: reading catalog: %w", err)
	}
	if len(repos) == 0 {
		fmt.Fprintln(stdout, "build: catalog has no repositories; run discover first")
		return nil
	}

	w := store.NewWriter(s, 0)
	p := &pipeline.Pipeline{
		Source:   catalog.New(*apiURL, githubToken(), nil),
		Runner:   &runner.Runner{CodeQLPath: *codeqlPath},
		Decode:   pipeline.DefaultDecoder(*codeqlPath),
		Store:    s,
		Ingester: ingest.New(s, w),
		Writer:   w,
		Config: pipeline.Config{
			Language:         *language,
			QueryPath:        *queryPath,
			QueryID:          *queryID,
			WorkDir:          dir,
			Concurrency:      *concurrency,
			ExtractorVersion: *extractorVersion,
			KeepArtifacts:    *keepArtifacts,
		},
		OnEvent: logFailures(stdout),
	}

	stats, err := p.Run(ctx, repos)
	if err != nil {
		return fmt.Errorf("build: %w", err)
	}
	fmt.Fprintf(stdout, "build: %d processed, %d skipped, %d failed, %d edges ingested\n",
		stats.Processed, stats.Skipped, stats.Failed, stats.Rows)

	if err := finalizeStore(s); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	return nil
}

// workingDirectory returns dir if set, otherwise a fresh temporary directory, along with a
// cleanup function that removes it only when this call created it.
func workingDirectory(dir, pattern string) (string, func(), error) {
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", nil, fmt.Errorf("creating %s: %w", dir, err)
		}
		return dir, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", pattern)
	if err != nil {
		return "", nil, fmt.Errorf("creating a scratch directory: %w", err)
	}
	return tmp, func() { os.RemoveAll(tmp) }, nil
}

// finalizeStore builds indexes and refreshes planner statistics after bulk loading, per
// Store.CreateIndexes' own contract: call once loading has finished, not while it is ongoing.
func finalizeStore(s *store.Store) error {
	if err := s.CreateIndexes(); err != nil {
		return fmt.Errorf("creating indexes: %w", err)
	}
	if err := s.Optimize(); err != nil {
		return fmt.Errorf("optimizing: %w", err)
	}
	return nil
}
