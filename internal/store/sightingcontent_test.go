package store

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// sightingContent is what one run's sighting of one edge says that run's commit declared.
type sightingContent struct {
	dst                       sql.NullString
	refRaw, refValue, comment sql.NullString
}

func sightingsByRun(t *testing.T, s *Store, edgeKey string) map[int64]sightingContent {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT sg.query_run_id, n.node_key, sg.ref_raw, sg.ref_value, sg.pin_comment
		  FROM abom_edge_sighting sg
		  JOIN abom_edge e ON e.row_id = sg.edge_row_id
		  LEFT JOIN abom_node n ON n.row_id = sg.dst_node_row_id
		 WHERE e.edge_key = ?`, edgeKey)
	if err != nil {
		t.Fatalf("reading sightings: %v", err)
	}
	defer rows.Close()
	out := map[int64]sightingContent{}
	for rows.Next() {
		var run int64
		var c sightingContent
		if err := rows.Scan(&run, &c.dst, &c.refRaw, &c.refValue, &c.comment); err != nil {
			t.Fatal(err)
		}
		out[run] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// observeAt writes what one commit of vendor/comp declares at one location, as an edges run does.
func observeAt(t *testing.T, s *Store, commit, edgeKey, dst, ref, comment string) int64 {
	t.Helper()
	repo, err := s.InsertUpstreamRepository("vendor/comp")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: "vendor/comp", DBCommit: commit})
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "vendor/comp", Kind: "composite_action", RepositoryID: repo, Path: "action.yml"},
		{Key: dst, Kind: "action", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddEdge(Edge{Key: edgeKey, SrcKey: "vendor/comp", DstKey: dst,
		Kind: EdgeKindCompositeStepUsesAction, RefRaw: dst + "@" + ref, RefValue: ref, PinComment: comment,
		RepositoryID: repo, QueryRunID: run, FilePath: "action.yml", StartLine: 15, StartColumn: 11,
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return run
}

const sharedLocation = "vendor/comp:action.yml:15:11"

// An edge is keyed by where it is declared, so two commits declaring different things there share
// one row, and the row holds whichever was written last. The sighting is the only place the other
// commit's own `uses:` can live; without it a walk standing at that commit names the wrong target.
func TestASightingKeepsTheUsesItsOwnCommitDeclared(t *testing.T) {
	s := newTestStore(t)
	older := observeAt(t, s, "c1", sharedLocation, "actions/cache", "v3", "")
	newer := observeAt(t, s, "c2", sharedLocation, "actions/setup-go", "v6", "# v6.0.0")

	got := sightingsByRun(t, s, sharedLocation)
	if c := got[older]; c.dst.String != "actions/cache" || c.refRaw.String != "actions/cache@v3" ||
		c.refValue.String != "v3" || c.comment.Valid {
		t.Errorf("the older commit's sighting says %+v, want actions/cache@v3 with no comment", c)
	}
	if c := got[newer]; c.dst.String != "actions/setup-go" || c.refValue.String != "v6" ||
		c.comment.String != "# v6.0.0" {
		t.Errorf("the newer commit's sighting says %+v, want actions/setup-go@v6 # v6.0.0", c)
	}
	var rowRef string
	if err := s.DB().QueryRow(`SELECT ref_raw FROM abom_edge WHERE edge_key = ?`,
		sharedLocation).Scan(&rowRef); err != nil {
		t.Fatal(err)
	}
	if rowRef != "actions/setup-go@v6" {
		t.Errorf("the row holds %q, want the last writer's actions/setup-go@v6", rowRef)
	}
}

// Before sightings kept content, the row was the only record, and it holds the last writer's
// `uses:`. That is exactly what the run that wrote the row saw, and nothing about any other run:
// copying it onto every sighting would make each older commit declare the newest one's target.
func TestASightingRecordedBeforeContentWasKeptIsKnownOnlyForTheRunThatWroteTheRow(t *testing.T) {
	s := newTestStore(t)
	older := observeAt(t, s, "c1", sharedLocation, "actions/cache", "v3", "")
	newer := observeAt(t, s, "c2", sharedLocation, "actions/cache", "v4", "")
	if _, err := s.DB().Exec(`UPDATE abom_edge_sighting
		SET dst_node_row_id = NULL, ref_raw = NULL, ref_value = NULL, pin_comment = NULL`); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	got := sightingsByRun(t, s, sharedLocation)
	if c := got[newer]; c.dst.String != "actions/cache" || c.refValue.String != "v4" {
		t.Errorf("the run that wrote the row sighted %+v, want the row's actions/cache@v4", c)
	}
	if c := got[older]; c.dst.Valid || c.refRaw.Valid || c.refValue.Valid {
		t.Errorf("an older run's sighting was given %+v, which only the newer commit declared", c)
	}
}

// The skip trusts a run to stand for its commit. An edges run recorded before its sightings kept
// content cannot: re-observing it is the only way to learn what its commit declared.
func TestAnEdgesRunAnswersTheSkipOnlyOnceItsSightingsCarryContent(t *testing.T) {
	s := newTestStore(t)
	q := QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges", QueryVersion: "e1",
		Pack: "advanced-security/abom-actions-graph-extraction-src", PackVersion: "2.27.1",
		PackDigest: "d1", CLIVersion: "2.27.1", Repository: "vendor/comp", DBCommit: "c1"}
	run, err := s.RecordQueryRun(q)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCommitContent("vendor/comp", "c1", "content-1"); err != nil {
		t.Fatal(err)
	}
	moved := q
	moved.DBCommit = "c1-moved"

	check := func(stage string, want bool) {
		t.Helper()
		if ran, err := s.QueryRunExists(q); err != nil || !ran {
			t.Errorf("%s: QueryRunExists = %v, %v; the evaluation is on record either way", stage, ran, err)
		}
		if got, err := s.EdgeRunExists(q); err != nil || got != want {
			t.Errorf("%s: EdgeRunExists = %v, %v; want %v", stage, got, err, want)
		}
		if got, err := s.EdgeRunExistsForContent(moved, "content-1"); err != nil || got != want {
			t.Errorf("%s: EdgeRunExistsForContent = %v, %v; want %v", stage, got, err, want)
		}
	}
	check("before its sightings are marked", false)
	if err := s.MarkSightedContent(run); err != nil {
		t.Fatal(err)
	}
	check("once they are", true)
}

// pinnedDependencies is octo/app naming three composite actions at v1: vendor/comp, analyzed at two
// commits; vendor/solo, analyzed at one; and vendor/never, which nothing has analyzed.
func pinnedDependencies(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	w := NewWriter(s, 0)
	for _, n := range []Node{
		{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app},
		{Key: "app-step", Kind: "step", RepositoryID: app, ContainerKey: "octo/app/.github/workflows/ci.yml"},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for i, dep := range []string{"vendor/comp", "vendor/solo", "vendor/never"} {
		owner, repo, _ := strings.Cut(dep, "/")
		if err := w.AddNode(Node{Key: dep, Kind: "action", Owner: owner, Repo: repo, IsExternal: true}); err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(Edge{Key: fmt.Sprintf("octo/app:.github/workflows/ci.yml:%d:9", 10+i),
			SrcKey: "app-step", DstKey: dep, Kind: EdgeKindStepUsesAction, RefRaw: dep + "@v1", RefValue: "v1",
			RepositoryID: app, FilePath: ".github/workflows/ci.yml", StartLine: 10 + i, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	runs := map[string]int64{}
	for repository, commits := range map[string][]string{"vendor/comp": {"c1", "c2"}, "vendor/solo": {"s1"}} {
		for commit, run := range analyzedBeforeContent(t, s, repository, 1, 0, commits...) {
			runs[repository+"@"+commit] = run
		}
	}
	return runs
}

// A snapshot replaces what GitHub found, so one built from a commit whose declarations ABOM has no
// certain record of deletes dependencies the repository really has. The closure names each such
// commit it read: one analyzed before sightings kept content, in a repository analyzed at another
// commit too. A repository analyzed at one commit shares no row, and a pin nothing analyzed is
// unanalyzed rather than unknown, which the preview already reports.
func TestAClosureNamesTheCommitsItReadsWhoseDeclarationsAreNotKnown(t *testing.T) {
	s := newTestStore(t)
	runs := pinnedDependencies(t, s)

	got, err := s.UnobservedClosureCommits("octo/app")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"vendor/comp@c1"}; !slices.Equal(got, want) {
		t.Errorf("unobserved = %q, want %q", got, want)
	}

	for _, run := range runs {
		if err := s.MarkSightedContent(run); err != nil {
			t.Fatal(err)
		}
	}
	got, err = s.UnobservedClosureCommits("octo/app")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("once re-observed the closure still names %#v, want an empty list", got)
	}
}
