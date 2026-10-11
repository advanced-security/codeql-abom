package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// runBOM writes a bill of materials.
//
// The same document the interface serves, from the same read, so a document produced here and one
// downloaded from the page cannot disagree.
func runBOM(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bom", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dbPath := databaseFlag(fs)
	format := fs.String("format", "native",
		"native, cyclonedx, spdx or summary; native is the only one that can carry provenance")
	out := fs.String("out", "", "write to this file instead of standard output")
	var roots repeated
	fs.Var(&roots, "root",
		"a repository to root the document at; repeatable, defaults to the declared owned scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		fs.Usage()
		return errors.New("bom: --db is required")
	}

	s, err := store.OpenBackend(*dbPath, store.Options{})
	if err != nil {
		return fmt.Errorf("opening %s: %w", store.DatabaseLabel(*dbPath), err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		return fmt.Errorf("migrating %s: %w", store.DatabaseLabel(*dbPath), err)
	}

	// Boundaries are filled by a derivation the serving path runs and this one otherwise never
	// would: on a database whose aggregates have not been rebuilt since the column was added,
	// every relationship in the document would say nothing. Idempotent and settles to zero, so
	// running it here costs nothing once it agrees with the graph.
	if _, err := s.ClassifyEdgeBoundaries(); err != nil {
		return fmt.Errorf("bom: %w", err)
	}

	doc, err := s.BillOfMaterials(store.BOMOptions{Roots: roots})
	if err != nil {
		return fmt.Errorf("bom: %w", err)
	}
	body, _, _, err := serve.SerializeBOM(doc, *format)
	if err != nil {
		return fmt.Errorf("bom: %w", err)
	}

	if *out == "" {
		_, err = stdout.Write(body)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return fmt.Errorf("bom: %w", err)
	}
	if err := os.WriteFile(*out, body, 0o644); err != nil {
		return fmt.Errorf("bom: %w", err)
	}
	// To stderr, so `--out` plus a redirect does not mix the note into the document.
	fmt.Fprintf(stderr, "wrote %d components to %s; %d of %d analyzed, %d findings (%d untraced)\n",
		len(doc.Components), *out,
		doc.Coverage.ComponentsAnalyzed, doc.Coverage.ComponentsInUse,
		doc.Coverage.Findings, doc.Coverage.FindingsUntraced)
	return nil
}
