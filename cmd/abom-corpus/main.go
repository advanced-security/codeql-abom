// Command abom-corpus builds a populated ABOM database from the fixtures under testdata/corpus.
//
// A development tool: the build itself lives in internal/corpus, which Demo initialization also
// calls, so the shipped binary never needs `go run` or a second builder.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/advanced-security/codeql-abom/internal/corpus"
)

func main() {
	dbPath := flag.String("db", os.Getenv("ABOM_DB"), "SQLite path or empty PostgreSQL URL (required; defaults to ABOM_DB)")
	fixtures := flag.String("fixtures", "testdata/corpus/fixtures", "recorded CodeQL output")
	// The states a component catalog would otherwise exist to draw. A database nobody can build is
	// the only reason to keep a second rendering of every component alive.
	without := flag.String("without", "",
		"comma-separated facts to leave out: advisories, findings")
	// The operator's query pack a server is given with --custom-ql-dir, laid out beside the
	// database: without one no custom query, mark or session can be drawn.
	queries := flag.String("queries", "", "directory to lay the corpus query pack out in, replacing it")
	record := flag.String("record-queries", "",
		"run the corpus query pack's tests with the CodeQL CLI and write what it said to this file")
	flag.Parse()
	if *record != "" {
		if err := corpus.RecordQueries(*record); err != nil {
			fmt.Fprintf(os.Stderr, "abom-corpus: recording the query pack: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "abom-corpus: --db is required")
		os.Exit(2)
	}
	if err := corpus.Build(corpus.Options{
		Database: *dbPath, Fixtures: *fixtures, Queries: *queries,
		Without: corpus.Omitted(*without), Out: os.Stdout,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "abom-corpus: %v\n", err)
		os.Exit(1)
	}
}
