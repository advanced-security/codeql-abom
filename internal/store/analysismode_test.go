package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A run that does not say how its database was built cannot be told apart from one that does, and
// the same rule id means opposite things in the two shapes: a defect in code the repository holds,
// or exposure reached through a `uses:` boundary that nothing native reports. Absent is refused
// rather than defaulted, for the same reason a NULL provider is never relabelled CodeQL.
func TestARunMustSayHowItsDatabaseWasBuilt(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom/edges", Repository: "octo/app",
	}); err == nil {
		t.Fatal("RecordQueryRun accepted a run stating no analysis mode; absent is not a mode")
	}
}

// And a shape ABOM does not build is refused by name, so a misspelling cannot open a third
// vocabulary that every aggregate then silently fails to read.
func TestARunCannotClaimAShapeABOMDoesNotBuild(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom/edges", Repository: "octo/app", AnalysisMode: "closure",
	}); err == nil {
		t.Fatal("RecordQueryRun accepted an analysis mode outside the vocabulary")
	}
}

// The two shapes are two databases, so they are two runs. One row would mean the second shape's
// results arrived attributed to the first, which is exactly the silent mixing the column exists to
// stop.
func TestTheSameQueryInTwoShapesIsTwoRuns(t *testing.T) {
	s := newTestStore(t)

	base := QueryRun{QueryID: "abom/edges", Repository: "octo/app", DBCommit: "deadbeef"}
	regular, extended := base, base
	regular.AnalysisMode, extended.AnalysisMode = AnalysisModeRegular, AnalysisModeExtended

	first, err := s.RecordQueryRun(regular)
	if err != nil {
		t.Fatalf("recording the regular run: %v", err)
	}
	second, err := s.RecordQueryRun(extended)
	if err != nil {
		t.Fatalf("recording the extended run: %v", err)
	}
	if first == second {
		t.Fatalf("both shapes recorded as run %d; one database's results would be "+
			"attributed to the other", first)
	}
}

// Recording the shape must not re-analyze existing regular runs. A regular run keeps the key it
// already had; only another shape has anything to add to the identity.
func TestARegularRunKeepsTheKeyItHadBeforeTheColumnExisted(t *testing.T) {
	run := QueryRun{
		QueryID: "abom/edges", QueryVersion: "1.0.0",
		Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.26.4",
		PackDigest: "ab12cd34", CLIVersion: "2.26.4",
		Repository: "octo/app", DBCommit: "deadbeef", SourceFilter: ".github",
		AnalysisMode: AnalysisModeRegular,
	}
	if got, want := run.key(), keyBeforeAnalysisMode(run); got != want {
		t.Errorf("a regular run's key moved to %s; every analysis on record would re-run", got)
	}

	run.AnalysisMode = AnalysisModeExtended
	if run.key() == keyBeforeAnalysisMode(run) {
		t.Error("an extended run shares the regular run's key, so one would overwrite the other")
	}
}

// keyBeforeAnalysisMode is the run key exactly as it was hashed before the column existed, written
// out rather than derived so the two can actually disagree.
func keyBeforeAnalysisMode(q QueryRun) string {
	h := sha256.New()
	for _, part := range []string{
		q.QueryID, q.QueryVersion, q.Pack, q.PackVersion, q.PackDigest,
		q.CLIVersion, q.Repository, q.DBCommit, q.SourceFilter,
	} {
		fmt.Fprintf(h, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The backfill is a fact rather than a guess: only one writer has ever existed and it builds one
// repository's own sparse checkout, so every row already on a database is regular. This is the
// deliberate exception to not back-filling provenance, and it is safe only while no second
// database shape can have written anything.
func TestAnUpgradedDatabaseCallsEveryExistingRunRegular(t *testing.T) {
	s := migratedLegacyStoreWith(t, func(s *Store) {
		if _, err := s.db.Exec(`
			INSERT INTO abom_query_run (run_key, query_id, repository, ran_at)
			VALUES ('legacy-key', 'abom/edges', 'octo/app', '2026-01-01T00:00:00Z')`,
		); err != nil {
			t.Fatalf("seeding a run recorded before the column existed: %v", err)
		}
	})

	var mode string
	if err := s.db.QueryRow(
		`SELECT analysis_mode FROM abom_query_run WHERE run_key = 'legacy-key'`,
	).Scan(&mode); err != nil {
		t.Fatalf("reading the backfilled mode: %v", err)
	}
	if mode != AnalysisModeRegular {
		t.Errorf("an existing run reports mode %q, want %q", mode, AnalysisModeRegular)
	}
}

// Content equivalence stands in for the commit, never for the shape. A database holding a staged
// closure sees files the repository's own checkout never contained, so a regular run over the same
// content has not answered the extended question and must not skip it.
func TestAnExtendedRunIsNotSkippedByARegularRunOverTheSameContent(t *testing.T) {
	s := newTestStore(t)

	regular := QueryRun{
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "deadbeef",
		AnalysisMode: AnalysisModeRegular,
	}
	if _, err := s.RecordQueryRun(regular); err != nil {
		t.Fatalf("recording the regular run: %v", err)
	}
	if err := s.RecordCommitContent("octo/app", "deadbeef", "tree-digest"); err != nil {
		t.Fatalf("recording commit content: %v", err)
	}

	extended := regular
	extended.AnalysisMode = AnalysisModeExtended
	ran, err := s.QueryRunExistsForContent(extended, "tree-digest")
	if err != nil {
		t.Fatalf("QueryRunExistsForContent: %v", err)
	}
	if ran {
		t.Error("an extended run was reported as already done by a regular run over the " +
			"same content; the closure is not in that content")
	}
}

// The backfill is a documented exception, not a general permission, and it holds only while every
// row on record really was one repository's own checkout. A writer that recorded `extended` before
// the mode column shipped would leave rows the upgrade then relabels `regular` — the exception
// would have quietly become a guess, and the extended results would have joined the regular totals.
//
// Enumerating the source is the only check that survives the ordering being wrong: a unit test can
// only prove the writers it happens to know about.
//
// Writing the mode onto a run, not naming it. A reader has to be able to ask for the shape and a
// read has to be able to pass it along, so the vocabulary is spoken all over the language; what no
// code may do yet is record a run as having been built that way.
func TestNoWriterRecordsAnExtendedRunYet(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}

	// analysismode.go declares the vocabulary the guard is written in.
	allowed := map[string]bool{"internal/store/analysismode.go": true}
	claims := regexp.MustCompile(`AnalysisMode\s*[:=]+\s*(\w+\.)?(AnalysisModeExtended|"extended")`)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if claims.Match(body) {
			t.Errorf("%s names the extended mode; no writer may record one until the "+
				"closure-database work lands, or the regular backfill stops being a fact", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// Every production writer must name its shape, which the refusal only proves for writers a test
// reaches. Enumerating the source proves it for the ones no test covers.
func TestEveryWriterStatesTheShapeItBuilt(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}

	// findings.go is the writer itself; the struct literal in question is its own signature.
	allowed := map[string]bool{"internal/store/findings.go": true}
	builds := regexp.MustCompile(`(?s)QueryRun\{[^}]*\}`)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allowed[filepath.ToSlash(rel)] {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, literal := range builds.FindAllString(string(body), -1) {
			if strings.Contains(literal, "AnalysisMode") {
				continue
			}
			t.Errorf("%s builds a QueryRun without stating its analysis mode:\n%s", rel, literal)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// The filter language decides which values exist; the store decides what they do. Those are two
// files, and a value renamed in one is a filter that silently stops narrowing in the other.
func TestTheStoreSpellsEveryModeTheLanguageAccepts(t *testing.T) {
	for _, c := range []struct{ store, language string }{
		{AnalysisModeRegular, viewspec.ModeRegular},
		{AnalysisModeExtended, viewspec.ModeExtended},
	} {
		if c.store != c.language {
			t.Errorf("the store says %q and the language says %q", c.store, c.language)
		}
	}
	// The union is the store's own, not the language's: asking for it has to be a decision inside
	// ABOM rather than something a shared link can carry.
	if ValidAnalysisMode(AnalysisModeAnyShape) {
		t.Error("a run can be recorded as mixed, which is not a shape any database is built in")
	}
}

// These two functions are the only reads allowed to interpolate a table name, which means they are
// also the only way past the source-enumerating guard. What they can return is therefore pinned.
func TestTheModeSourcesNameOnlyDeclaredViews(t *testing.T) {
	declared := map[string]bool{
		"abom_edge_regular": true, "abom_edge_extended": true, "abom_edge_any_mode": true,
		"abom_finding_regular": true, "abom_finding_extended": true,
		"abom_finding_any_mode": true,
	}
	// The empty string is a caller that did not ask about the shape, which must answer regular.
	for _, mode := range []string{"", AnalysisModeRegular, AnalysisModeExtended,
		AnalysisModeAnyShape, "closure", "'; DROP TABLE abom_edge; --"} {
		for _, got := range []string{edgeSource(mode), findingSource(mode)} {
			if !declared[got] {
				t.Errorf("mode %q produced %q, which is not a view this package declares",
					mode, got)
			}
		}
	}
	if edgeSource("") != "abom_edge_regular" || findingSource("") != "abom_finding_regular" {
		t.Error("not asking about the shape stopped meaning regular; every existing aggregate " +
			"would change what it counts")
	}

	s := newTestStore(t)
	for _, view := range []string{
		edgeSource(AnalysisModeRegular), edgeSource(AnalysisModeExtended),
		edgeSource(AnalysisModeAnyShape), findingSource(AnalysisModeRegular),
		findingSource(AnalysisModeExtended), findingSource(AnalysisModeAnyShape),
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + view).Scan(&n); err != nil {
			t.Errorf("%s does not exist in a migrated database: %v", view, err)
		}
	}
}
