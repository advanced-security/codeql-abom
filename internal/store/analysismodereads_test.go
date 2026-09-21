package store

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

func TestPolicyCoverageNamesOnlyUnattributedFindingsInItsMode(t *testing.T) {
	database := newTestStore(t)
	repository, err := database.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, mode := range AnalysisModes() {
		run, err := database.RecordQueryRun(QueryRun{
			QueryID: "actions/unpinned-tag", Repository: "octo/app", AnalysisMode: mode,
		})
		if err != nil {
			t.Fatal(err)
		}
		for line := 0; line <= index; line++ {
			if _, err := database.AddFindings([]Finding{{
				QueryRunID: run, RepositoryID: repository, RuleID: "actions/unpinned-tag",
				FilePath: mode + ".yml", StartLine: line + 1,
			}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for index, mode := range AnalysisModes() {
		for _, rules := range [][]string{{"actions/unpinned-tag"}, nil} {
			count, repositories, err := database.unattributedFor(
				viewspec.Spec{Mode: mode, Rules: rules}, func(string) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			if count != index+1 || len(repositories) != 1 || repositories[0] != "octo/app" {
				t.Errorf("%s rules=%v: %d findings in %v, want %d in octo/app",
					mode, rules, count, repositories, index+1)
			}
		}
	}
}

// twoShapesOverOneDefect records one defect seen by a regular run and by an extended run, in the
// order given, and returns the finding's row id.
//
// The order is a parameter because it is the whole point: `abom_finding.query_run_id` holds the
// run that most recently reproduced a finding, so whichever lane writes last owns that column.
func twoShapesOverOneDefect(t *testing.T, s *Store, extendedLast bool) int64 {
	t.Helper()

	repo, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	regular, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", Repository: "octo/app", DBCommit: "cafe1234",
		AnalysisMode: AnalysisModeRegular,
	})
	if err != nil {
		t.Fatalf("recording the regular run: %v", err)
	}
	extended, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", Repository: "octo/app", DBCommit: "cafe1234",
		AnalysisMode: AnalysisModeExtended,
	})
	if err != nil {
		t.Fatalf("recording the extended run: %v", err)
	}

	order := []int64{regular, extended}
	if !extendedLast {
		order = []int64{extended, regular}
	}
	for _, run := range order {
		if _, err := s.AddFindings([]Finding{{
			Provider: "codeql", QueryRunID: run, RuleID: "actions/unpinned-tag",
			RepositoryID: repo, FilePath: ".github/workflows/ci.yml", StartLine: 12,
			StartColumn: 9, Fingerprint: "fp-1",
		}}); err != nil {
			t.Fatalf("AddFindings: %v", err)
		}
	}

	var rowID int64
	var findings int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_finding`).Scan(&findings); err != nil {
		t.Fatalf("counting findings: %v", err)
	}
	if findings != 1 {
		t.Fatalf("one defect seen in two shapes became %d findings; a mode is not a new defect",
			findings)
	}
	if err := s.db.QueryRow(`SELECT row_id FROM abom_finding`).Scan(&rowID); err != nil {
		t.Fatalf("reading the finding: %v", err)
	}
	return rowID
}

// The shape a finding was seen in is a property of the sighting, not of the finding. Reading the
// mode off `abom_finding.query_run_id` makes the answer depend on which lane wrote last: the
// defect the regular analysis reports would leave the regular totals the moment an extended run
// reproduced it, which is precisely the silent interchange the mode exists to prevent.
func TestAModeReadDoesNotDependOnWhichLaneWroteLast(t *testing.T) {
	for _, extendedLast := range []bool{false, true} {
		s := newTestStore(t)
		twoShapesOverOneDefect(t, s, extendedLast)

		for _, want := range []struct {
			mode string
			view string
		}{
			{AnalysisModeRegular, "abom_finding_regular"},
			{AnalysisModeExtended, "abom_finding_extended"},
		} {
			var n int
			if err := s.db.QueryRow(
				`SELECT COUNT(*) FROM ` + want.view).Scan(&n); err != nil {
				t.Fatalf("%s: %v", want.view, err)
			}
			if n != 1 {
				t.Errorf("extendedLast=%v: %s holds %d findings, want the one defect both "+
					"shapes saw", extendedLast, want.view, n)
			}
		}
	}
}

// A defect only the extended shape can see must not reach a regular total, whatever the regular
// lane did to the same repository.
func TestADefectOnlyTheClosureSeesStaysOutOfTheRegularTotals(t *testing.T) {
	s := newTestStore(t)
	repo, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	extended, err := s.RecordQueryRun(QueryRun{
		QueryID: "actions/unpinned-tag", Repository: "octo/app", DBCommit: "cafe1234",
		AnalysisMode: AnalysisModeExtended,
	})
	if err != nil {
		t.Fatalf("recording the extended run: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		Provider: "codeql", QueryRunID: extended, RuleID: "actions/unpinned-tag",
		RepositoryID: repo, FilePath: "vendor/toolkit/action.yml", StartLine: 4,
		StartColumn: 1, Fingerprint: "fp-closure",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	var regular, extendedCount, any int
	if err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM abom_finding_regular),
		(SELECT COUNT(*) FROM abom_finding_extended),
		(SELECT COUNT(*) FROM abom_finding_any_mode)`).
		Scan(&regular, &extendedCount, &any); err != nil {
		t.Fatalf("counting per mode: %v", err)
	}
	if regular != 0 {
		t.Errorf("a closure-only defect reached the regular total (%d); extended rows are "+
			"inert for existing aggregates", regular)
	}
	if extendedCount != 1 {
		t.Errorf("the extended total holds %d, want the closure-only defect", extendedCount)
	}
	if any != 1 {
		t.Errorf("the mixed total holds %d, want the one defect on record", any)
	}
}

func TestAnExtendedEdgeStaysOutOfTheRegularGraph(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}

	observe := func(mode, key, path string, line int) {
		t.Helper()
		run, err := s.RecordQueryRun(QueryRun{
			QueryID: "abom/edges", Repository: "octo/app", DBCommit: "cafe1234",
			AnalysisMode: mode,
		})
		if err != nil {
			t.Fatalf("recording the %s run: %v", mode, err)
		}
		w := NewWriter(s, 0)
		for _, n := range []Node{
			{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app,
				Path: ".github/workflows/ci.yml"},
			{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
				IsExternal: true},
		} {
			if err := w.AddNode(n); err != nil {
				t.Fatalf("AddNode: %v", err)
			}
		}
		if err := w.AddEdge(Edge{
			Key: key, SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
			Kind: "step-uses-action", RefRaw: "actions/checkout@v4", RefValue: "v4",
			RepositoryID: app, QueryRunID: run, FilePath: path, StartLine: line,
		}); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if err := w.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
	observe(AnalysisModeRegular, "e-own", ".github/workflows/ci.yml", 12)
	observe(AnalysisModeExtended, "e-closure", "closure/vendor/toolkit/action.yml", 4)
	observe(AnalysisModeExtended, "e-own", ".github/workflows/ci.yml", 12)

	var live, regular, extended int
	if err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM abom_edge_live),
		(SELECT COUNT(*) FROM abom_edge_regular),
		(SELECT COUNT(*) FROM abom_edge_extended)`).Scan(&live, &regular, &extended); err != nil {
		t.Fatalf("counting edges: %v", err)
	}
	if live != 2 {
		t.Fatalf("both shapes wrote %d edges, want 2", live)
	}
	if regular != 1 {
		t.Errorf("the regular graph holds %d edges, want only the one its own checkout saw",
			regular)
	}
	if extended != 2 {
		t.Errorf("the extended graph holds %d edges, want the closure and shared edges", extended)
	}
}

// Reading the mode off a run means a lookup, and a lookup that scans is one nobody will keep. The
// column looked up is the run's INTEGER PRIMARY KEY, so SQLite resolves it as a rowid seek —
// which is why denormalizing the mode onto every edge and finding buys nothing, and would cost
// the correctness the tests above protect.
//
// Measured 2026-09-20 on a fresh database: `abom_edge_regular` plans as
// `SCAN abom_edge | CORRELATED SCALAR SUBQUERY | SEARCH q USING INTEGER PRIMARY KEY (rowid=?)`,
// and `abom_finding_regular` reaches the run through the sighting relation's own covering index.
// This asserts the plan rather than the clock: on a corpus this size both forms answer quickly,
// so a stopwatch would pass on the shape that does not survive an enterprise estate.
func TestModeCostsARowidLookupRatherThanAScan(t *testing.T) {
	s := newTestStore(t)

	for _, view := range []string{"abom_edge_regular", "abom_finding_regular"} {
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT COUNT(*) FROM ` + view)
		if err != nil {
			t.Fatalf("explaining %s: %v", view, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, notUsed int
			var detail string
			if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()

		joined := strings.Join(plan, " | ")
		if strings.Contains(joined, "SCAN abom_query_run") || strings.Contains(joined, "SCAN q") {
			t.Errorf("%s scans the run table: %s", view, joined)
		}
		if !strings.Contains(joined, "SEARCH q USING INTEGER PRIMARY KEY") {
			t.Errorf("%s does not reach the run by rowid, so the mode is not the cheap fact "+
				"the design assumes: %s", view, joined)
		}
	}
}

// An aggregate that does not name a mode is the defect this whole workstream exists to prevent:
// it reads whatever happens to be in the table, so an extended row joins a regular total without
// anything failing. Enumerating the source is the only check that covers reads no test reaches.
func TestEveryEdgeAndFindingReadNamesItsMode(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolving repository root: %v", err)
	}

	// The writer inserts, the prune marks, and the two classification passes fill columns on the
	// physical rows — none of them aggregate, and all of them must see every row whatever shape
	// produced it. analysismode.go declares the views. Each is named rather than pattern-matched
	// so adding one is a decision.
	allowed := map[string]bool{
		"internal/store/analysismode.go": true,
		"internal/store/edgeboundary.go": true,
		"internal/store/findings.go":     true,
		"internal/store/prune.go":        true,
	}
	// `_regular`, `_extended` or `_any_mode` after the table name states a mode; `_live` or a
	// bare name does not.
	unnamed := regexp.MustCompile(
		`(?:FROM|JOIN)\s+(abom_edge|abom_finding)(_live|_regular|_extended|_any_mode)?\b`)
	unscopedCount := regexp.MustCompile(`(?i)COUNT\([^)]*\)\s+FROM\s+abom_finding_node(?:\s+fn)?(?:\s+WHERE\b|\s*[` + "`" + `)])`)

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
		for _, count := range unscopedCount.FindAllString(string(body), -1) {
			t.Errorf("%s counts finding attributions without joining a declared mode: %s", rel, count)
		}
		for _, m := range unnamed.FindAllStringSubmatch(string(body), -1) {
			switch m[2] {
			case "_regular", "_extended", "_any_mode":
				continue
			}
			t.Errorf("%s aggregates over %q without naming a mode; use %s_regular, "+
				"%s_extended or %s_any_mode", rel, strings.TrimSpace(m[0]), m[1], m[1], m[1])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
}

// Isolation, stated as a mutation: change one run's shape and exactly the totals that declare
// that shape may move. This is the check a view definition cannot pass by accident — a read that
// forgot its mode keeps answering the same number, and a read that named the wrong one moves when
// it should not.
func TestFlippingARunsModeMovesOnlyTheTotalsThatDeclareIt(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	run, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "cafe1234",
		AnalysisMode: AnalysisModeRegular,
	})
	if err != nil {
		t.Fatalf("RecordQueryRun: %v", err)
	}

	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app,
			Path: ".github/workflows/ci.yml"},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: "e1", SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefRaw: "actions/checkout@v4", RefValue: "v4",
		RepositoryID: app, QueryRunID: run, FilePath: ".github/workflows/ci.yml", StartLine: 12,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, err := s.AddFindings([]Finding{{
		Provider: "codeql", QueryRunID: run, RuleID: "actions/unpinned-tag", RepositoryID: app,
		FilePath: ".github/workflows/ci.yml", StartLine: 12, Fingerprint: "fp-1",
	}}); err != nil {
		t.Fatalf("AddFindings: %v", err)
	}

	// Nodes carry no mode: they are what the graph names, not what an analysis observed, so the
	// mixed totals and the node count must be the fixed points of the mutation.
	counts := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		for _, q := range []string{
			"abom_edge_regular", "abom_edge_extended", "abom_edge_any_mode",
			"abom_finding_regular", "abom_finding_extended", "abom_finding_any_mode",
			"abom_node_live",
		} {
			var n int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + q).Scan(&n); err != nil {
				t.Fatalf("counting %s: %v", q, err)
			}
			out[q] = n
		}
		before, err := s.Summary()
		if err != nil {
			t.Fatalf("Summary: %v", err)
		}
		out["Summary.Edges"] = before.Edges
		out["Summary.Nodes"] = before.Nodes
		return out
	}

	before := counts()
	if _, err := s.db.Exec(
		`UPDATE abom_query_run SET analysis_mode = ? WHERE row_id = ?`,
		AnalysisModeExtended, run); err != nil {
		t.Fatalf("flipping the run's mode: %v", err)
	}
	after := counts()

	want := map[string]int{
		"abom_edge_regular": 0, "abom_edge_extended": 1, "abom_edge_any_mode": 1,
		"abom_finding_regular": 0, "abom_finding_extended": 1, "abom_finding_any_mode": 1,
		"abom_node_live": before["abom_node_live"],
		"Summary.Edges":  0, "Summary.Nodes": before["Summary.Nodes"],
	}
	for key, wantN := range want {
		if after[key] != wantN {
			t.Errorf("%s went %d -> %d, want %d", key, before[key], after[key], wantN)
		}
	}
}

// observeEdge writes one `uses:` line as a run of the given shape saw it.
func observeEdge(t *testing.T, s *Store, repository int64, mode, key string) {
	t.Helper()
	run, err := s.RecordQueryRun(QueryRun{
		QueryID: "abom/edges", Repository: "octo/app", DBCommit: "cafe1234", AnalysisMode: mode,
	})
	if err != nil {
		t.Fatalf("recording the %s run: %v", mode, err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: repository,
			Path: ".github/workflows/ci.yml"},
		{Key: "actions/checkout", Kind: "action", Owner: "actions", Repo: "checkout",
			IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	if err := w.AddEdge(Edge{
		Key: key, SrcKey: "octo/app/.github/workflows/ci.yml", DstKey: "actions/checkout",
		Kind: "step-uses-action", RefRaw: "actions/checkout@v4", RefValue: "v4",
		RepositoryID: repository, QueryRunID: run, FilePath: ".github/workflows/ci.yml",
		StartLine: 12,
	}); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// An edge both shapes observe enters the second shape's view by gaining a sighting, not by
// gaining a row. A delta hands out arrivals by row id, so an extended graph left open across the
// closure's first look at a line the repository's own checkout already reported would never hear
// about it, and would keep drawing a picture a reload contradicts.
func TestAnEdgeSharedWithASecondShapeReachesAnAlreadyOpenGraph(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	observeEdge(t, s, app, AnalysisModeRegular, "e-own")
	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	observeEdge(t, s, app, AnalysisModeExtended, "e-own")

	delta, err := s.GraphDelta(generation, FullGraphOptions{Mode: AnalysisModeExtended})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		return
	}
	for _, e := range delta.Edges {
		if e.Target == "actions/checkout" {
			return
		}
	}
	t.Errorf("an edge the closure has just started sharing reached an open extended graph "+
		"neither as an arrival nor as a reset; delta = %+v", delta)
}

// The other half of the same rule: an ordinary crawl re-observing what it already saw broadens
// nothing, and invalidating every open cursor for it would cost the whole payload every cycle.
func TestReObservingAnEdgeInTheSameShapeLeavesOpenCursorsAlone(t *testing.T) {
	s := newTestStore(t)
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatalf("UpsertRepository: %v", err)
	}
	observeEdge(t, s, app, AnalysisModeRegular, "e-own")
	generation, err := s.RecordGraphGeneration(GraphRebuild{})
	if err != nil {
		t.Fatalf("RecordGraphGeneration: %v", err)
	}

	observeEdge(t, s, app, AnalysisModeRegular, "e-own")

	delta, err := s.GraphDelta(generation, FullGraphOptions{})
	if err != nil {
		t.Fatalf("GraphDelta: %v", err)
	}
	if delta.Reset {
		t.Error("a repeat observation in the shape already on record reset an open cursor; " +
			"every following page would reload the whole graph every crawl")
	}
}
