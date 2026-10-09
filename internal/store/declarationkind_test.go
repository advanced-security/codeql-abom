package store

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// kindChangeLine is a location in vendor/tool's release workflow that is a `needs:` at one commit
// and a `uses:` at another, as line 108 of github/codeql-action's update-release-branch.yml is.
const kindChangeLine = "vendor/tool:.github/workflows/release.yml:108:13"

const (
	toolWorkflowFile = ".github/workflows/release.yml"
	toolWorkflow     = "vendor/tool/.github/workflows/release.yml"
	toolPrepareJob   = "job:vendor/tool:.github/workflows/release.yml:prepare"
	toolBackportJob  = "job:vendor/tool:.github/workflows/release.yml:backport"
	toolBackportStep = "step:vendor/tool:.github/workflows/release.yml:backport:2"
	toolTestStep     = "step:vendor/tool:.github/workflows/release.yml:test:0"
	toolLocalAction  = "vendor/tool/.github/actions/release-setup"
	toolLocalUses    = "./.github/actions/release-setup"
	toolCallJob      = "job:vendor/tool:.github/workflows/release.yml:call"
)

// toolDeclaration is what one commit of vendor/tool declared at one location of its release workflow.
type toolDeclaration struct {
	commit string
	edge   Edge
}

var (
	// toolNeeds has the backport job run after prepare.
	toolNeeds = toolDeclaration{"n1", Edge{Key: kindChangeLine, SrcKey: toolBackportJob,
		DstKey: toolPrepareJob, Kind: EdgeKindJobNeedsJob, StartLine: 108, StartColumn: 13}}
	// toolUses has a step of the backport job use the repository's own action there instead.
	toolUses = toolDeclaration{"u2", Edge{Key: kindChangeLine, SrcKey: toolBackportStep,
		DstKey: toolLocalAction, Kind: EdgeKindStepUsesAction, RefRaw: toolLocalUses,
		StartLine: 108, StartColumn: 13}}
)

// toolFixture is vendor/tool, an upstream repository whose workflow nodes exist, written through one
// writer so each declaration can name them.
type toolFixture struct {
	*Store
	w    *Writer
	repo int64
}

func newToolFixture(t *testing.T) *toolFixture {
	t.Helper()
	s := newTestStore(t)
	repo, err := s.InsertUpstreamRepository("vendor/tool")
	if err != nil {
		t.Fatal(err)
	}
	f := &toolFixture{Store: s, w: NewWriter(s, 0), repo: repo}
	for _, n := range []Node{
		{Key: toolWorkflow, Kind: "workflow", RepositoryID: repo, Path: toolWorkflowFile},
		{Key: toolPrepareJob, Kind: "job", RepositoryID: repo, JobID: "prepare", StepIndex: -1,
			ContainerKey: toolWorkflow},
		{Key: toolBackportJob, Kind: "job", RepositoryID: repo, JobID: "backport", StepIndex: -1,
			ContainerKey: toolWorkflow},
		{Key: toolBackportStep, Kind: "step", RepositoryID: repo, JobID: "backport", StepIndex: 2,
			ContainerKey: toolWorkflow},
		{Key: toolTestStep, Kind: "step", RepositoryID: repo, JobID: "test", StepIndex: 0,
			ContainerKey: toolWorkflow},
		{Key: toolLocalAction, Kind: "composite_action", RepositoryID: repo,
			Path: ".github/actions/release-setup"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
		{Key: toolCallJob, Kind: "job", RepositoryID: repo, JobID: "call", StepIndex: -1,
			ContainerKey: toolWorkflow},
		{Key: "vendor/lib/.github/workflows/build.yml", Kind: "reusable_workflow", Owner: "vendor",
			Repo: "lib", Path: ".github/workflows/build.yml", IsExternal: true},
		{Key: "vendor/tool/.github/workflows/build.yml", Kind: "workflow", RepositoryID: repo,
			Path: ".github/workflows/build.yml"},
		{Key: "docker://alpine:3", Kind: "docker_image", IsExternal: true},
		{Key: "vendor/tool", Kind: "composite_action", RepositoryID: repo, Path: "action.yml"},
		{Key: "step:vendor/tool:action.yml::0", Kind: "step", RepositoryID: repo,
			ContainerKey: "vendor/tool"},
		{Key: "vendor/tool/nested", Kind: "composite_action", RepositoryID: repo,
			Path: "nested/action.yaml"},
		{Key: "step:vendor/tool:nested/action.yaml::1", Kind: "step", RepositoryID: repo,
			StepIndex: 1, ContainerKey: "vendor/tool/nested"},
	} {
		if err := f.w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}
	return f
}

// declare writes what one commit declared, as that commit's edges run does, and makes the commit
// live: a reference resolves to it.
func (f *toolFixture) declare(t *testing.T, d toolDeclaration) {
	t.Helper()
	run, err := f.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular, QueryID: "abom/edges",
		Repository: "vendor/tool", DBCommit: d.commit})
	if err != nil {
		t.Fatal(err)
	}
	e := d.edge
	e.RepositoryID, e.QueryRunID = f.repo, run
	if e.FilePath == "" {
		e.FilePath = toolWorkflowFile
	}
	if err := f.w.AddEdge(e); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.MarkSightedContent(run); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DB().Exec(`INSERT OR IGNORE INTO abom_frontier (node_key, ref, owner, repo, depth,
		status, enqueued_at, resolved_sha) VALUES ('vendor/tool', ?, 'vendor', 'tool', 1, 'done',
		'2026-10-01T00:00:00Z', ?)`, d.commit, d.commit); err != nil {
		t.Fatal(err)
	}
}

// eachFirstWriter runs a check with each declaration written to the location first, since the row
// kept its first writer's kind: either order was a defect of its own.
func eachFirstWriter(t *testing.T, declarations []toolDeclaration, check func(t *testing.T, f *toolFixture)) {
	for i := range declarations {
		order := append([]toolDeclaration{declarations[i]}, declarations[:i]...)
		order = append(order, declarations[i+1:]...)
		t.Run("first written at "+order[0].commit, func(t *testing.T) {
			f := newToolFixture(t)
			for _, d := range order {
				f.declare(t, d)
			}
			check(t, f)
		})
	}
}

// rowIDs reads the row id of each node or edge key asked for.
func rowIDs(t *testing.T, s *Store, keys ...string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, key := range keys {
		var id int64
		if err := s.DB().QueryRow(`SELECT row_id FROM abom_node_live WHERE node_key = ?1
			UNION ALL SELECT row_id FROM abom_edge_live WHERE edge_key = ?1`, key).Scan(&id); err != nil {
			t.Fatalf("reading the row id of %s: %v", key, err)
		}
		out[key] = id
	}
	return out
}

// declarationsAt reads EdgeDeclarations at the commits the queue stands at.
func declarationsAt(t *testing.T, s *Store) []EdgeDeclaration {
	t.Helper()
	live, err := s.LiveCommits()
	if err != nil {
		t.Fatal(err)
	}
	starting, err := s.StartingCommits(live)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.EdgeDeclarations(live, starting)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A location is one row whatever its commits declared there, so what each commit declared is its
// own sighting, its kind and its declaring node included. Read from the row, the `uses:` was dropped
// by every dependency reader wherever the `needs:` was written first — check 1 found 13 of 300
// commits of github/codeql-action disagreeing with their own files — and a job became a dependency
// wherever the `uses:` was.
func TestEachCommitReadsTheKindAndSourceItDeclaredAtALocation(t *testing.T) {
	eachFirstWriter(t, []toolDeclaration{toolNeeds, toolUses}, func(t *testing.T, f *toolFixture) {
		uses := []Declaration{{File: toolWorkflowFile, Line: 108, Kind: EdgeKindStepUsesAction,
			Raw: toolLocalUses, Target: toolLocalAction}}
		for commit, want := range map[string][]Declaration{toolNeeds.commit: {}, toolUses.commit: uses} {
			got, err := f.CommitDeclarations("vendor/tool", []string{commit})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("CommitDeclarations(%s) = %+v, want %+v", commit, got, want)
			}
		}

		ids := rowIDs(t, f.Store, kindChangeLine, toolBackportStep, toolLocalAction)
		want := []EdgeDeclaration{{Row: ids[kindChangeLine], Src: ids[toolBackportStep],
			Dst: ids[toolLocalAction], Commits: []string{toolUses.commit}}}
		if got := declarationsAt(t, f.Store); !reflect.DeepEqual(got, want) {
			t.Errorf("EdgeDeclarations = %+v, want only the step's use at %s: %+v", got, toolUses.commit, want)
		}
	})
}

// The units read the same declarations: each commit's line is listed with its own kind and the job
// or step that declared it there, never with another commit's.
func TestTheRelationshipsUnitListsEachCommitsKindAndSourceAtALocation(t *testing.T) {
	eachFirstWriter(t, []toolDeclaration{toolNeeds, toolUses}, func(t *testing.T, f *toolFixture) {
		// CI belongs in this listing only at a starting point's branches, not a dependency's releases.
		if _, err := f.DB().Exec(`UPDATE repository SET origin = 'seed';
			UPDATE abom_frontier SET depth = 0`); err != nil {
			t.Fatal(err)
		}
		page, err := f.RelationshipsPage(RelationshipsQuery{Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, r := range page.Items {
			if r.EdgeKey == kindChangeLine {
				got[r.Kind+" "+r.Source+" -> "+r.Target] = strings.Join(r.Commits, ",")
			}
		}
		want := map[string]string{
			EdgeKindJobNeedsJob + " " + toolBackportJob + " -> " + toolPrepareJob:      toolNeeds.commit,
			EdgeKindStepUsesAction + " " + toolBackportStep + " -> " + toolLocalAction: toolUses.commit,
		}
		if !maps.Equal(got, want) {
			t.Errorf("the line lists %v, want %v", got, want)
		}
	})
}

// Code moves between jobs and keeps its location, so the node declaring a location is each commit's
// too: the job that runs a dependency at a commit is the one that held the line there.
func TestALineThatMovedBetweenJobsIsDeclaredByTheStepThatHeldItAtEachCommit(t *testing.T) {
	cache := func(commit, step string) toolDeclaration {
		return toolDeclaration{commit, Edge{Key: "vendor/tool:.github/workflows/release.yml:20:9",
			SrcKey: step, DstKey: "actions/cache", Kind: EdgeKindStepUsesAction,
			RefRaw: "actions/cache@v4", RefValue: "v4", StartLine: 20, StartColumn: 9}}
	}
	eachFirstWriter(t, []toolDeclaration{cache("b1", toolBackportStep), cache("t2", toolTestStep)},
		func(t *testing.T, f *toolFixture) {
			ids := rowIDs(t, f.Store, "vendor/tool:.github/workflows/release.yml:20:9",
				toolBackportStep, toolTestStep, "actions/cache")
			row := ids["vendor/tool:.github/workflows/release.yml:20:9"]
			want := []EdgeDeclaration{
				{Row: row, Src: ids[toolBackportStep], Dst: ids["actions/cache"], Ref: "v4", Commits: []string{"b1"}},
				{Row: row, Src: ids[toolTestStep], Dst: ids["actions/cache"], Ref: "v4", Commits: []string{"t2"}},
			}
			if ids[toolTestStep] < ids[toolBackportStep] {
				want[0], want[1] = want[1], want[0]
			}
			if got := declarationsAt(t, f.Store); !reflect.DeepEqual(got, want) {
				t.Errorf("EdgeDeclarations = %+v, want each commit's own step: %+v", got, want)
			}
		})
}

// The submission closure steps from each dependency it reaches into what that dependency's own jobs
// and steps declared, so it finds those declarations by the node that declared them. Read as a
// sighting's or else its row's, the declaring node lost the type an index needs, and the closure
// scanned every declaration on record once per step: check 8 ran for minutes on the crawled
// organization instead of seconds.
func TestTheSubmissionClosureFindsWhatADependencyDeclaredByTheNodeDeclaringIt(t *testing.T) {
	s := newTestStore(t)
	// The test store defers indexes, as a bulk load does; a served or crawling one has them all.
	if err := s.CreateIndexes(); err != nil {
		t.Fatalf("CreateIndexes: %v", err)
	}
	plan := explainSteps(t, s, submissionClosureQuery, "octo/app", "[]", "[]", nil)
	recursive := false
	for _, step := range plan {
		if step == "RECURSIVE STEP" {
			recursive = true
			continue
		}
		if recursive && (step == "SCAN o" || strings.HasPrefix(step, "SCAN o ")) {
			t.Errorf("each step of the closure scans every declaration:\n\t%s", strings.Join(plan, "\n\t"))
		}
	}
	if !recursive {
		t.Errorf("the closure has no recursive step:\n\t%s", strings.Join(plan, "\n\t"))
	}
}

// The row is what its last writer declared, all of it. A target beside another commit's kind is a
// row no reader can take at its word: three on the crawled organization named a composite action as
// a job to wait for, or a job as an action a step used.
func TestALocationsRowHoldsItsLastWritersKindAndSourceWithItsTarget(t *testing.T) {
	f := newToolFixture(t)
	for _, d := range []toolDeclaration{toolNeeds, toolUses, toolNeeds} {
		f.declare(t, d)
		var kind, src, dst string
		if err := f.DB().QueryRow(`SELECT e.edge_kind, s.node_key, d.node_key FROM abom_edge_live e
			JOIN abom_node_live s ON s.row_id = e.src_node_row_id
			JOIN abom_node_live d ON d.row_id = e.dst_node_row_id
			WHERE e.edge_key = ?`, kindChangeLine).Scan(&kind, &src, &dst); err != nil {
			t.Fatal(err)
		}
		if kind != d.edge.Kind || src != d.edge.SrcKey || dst != d.edge.DstKey {
			t.Errorf("after %s the row says %s %s -> %s, want %s %s -> %s", d.commit, kind, src, dst,
				d.edge.Kind, d.edge.SrcKey, d.edge.DstKey)
		}
	}
}

// A submission names what the pinned commit of a called workflow runs. A line that is a `needs:`
// at another commit of it does not hide the `uses:` the pinned commit has there, whichever of the
// two commits wrote the row: the snapshot replaces what GitHub found, so a missing one is deleted.
func TestASubmissionNamesAUseThePinnedCommitDeclaredWhereAnotherHadANeeds(t *testing.T) {
	const build = "vendor/flows/.github/workflows/build.yml"
	for _, usesFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("uses written first: %t", usesFirst), func(t *testing.T) {
			s := newTestStore(t)
			app, err := s.UpsertRepository("octo/app", "")
			if err != nil {
				t.Fatal(err)
			}
			flows, err := s.InsertUpstreamRepository("vendor/flows")
			if err != nil {
				t.Fatal(err)
			}
			w := NewWriter(s, 0)
			add := adder(t, w)
			add(Node{Key: "octo/app/.github/workflows/ci.yml", Kind: "workflow", RepositoryID: app})
			add(Node{Key: "job:octo/app:.github/workflows/ci.yml:call", Kind: "job", JobID: "call",
				StepIndex: -1, RepositoryID: app, ContainerKey: "octo/app/.github/workflows/ci.yml"})
			add(Node{Key: build, Kind: "reusable_workflow", Owner: "vendor", Repo: "flows",
				Path: ".github/workflows/build.yml", IsExternal: true})
			for _, job := range []string{"prepare", "publish"} {
				add(Node{Key: "job:vendor/flows:.github/workflows/build.yml:" + job, Kind: "job",
					JobID: job, StepIndex: -1, RepositoryID: flows, ContainerKey: build})
			}
			add(Node{Key: "step:vendor/flows:.github/workflows/build.yml:publish:0", Kind: "step",
				JobID: "publish", RepositoryID: flows, ContainerKey: build})
			add(Node{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true})

			write := func(repository string, repo int64, commit string, e Edge) {
				t.Helper()
				run, err := s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
					QueryID: "abom/edges", Repository: repository, DBCommit: commit})
				if err != nil {
					t.Fatal(err)
				}
				e.RepositoryID, e.QueryRunID, e.StartColumn = repo, run, 9
				if err := w.AddEdge(e); err != nil {
					t.Fatal(err)
				}
				if err := w.Flush(); err != nil {
					t.Fatal(err)
				}
				if err := s.MarkSightedContent(run); err != nil {
					t.Fatal(err)
				}
			}
			write("octo/app", app, "app1", Edge{Key: "octo/app:.github/workflows/ci.yml:9:9",
				SrcKey: "job:octo/app:.github/workflows/ci.yml:call", DstKey: build,
				Kind: EdgeKindJobUsesReusableWorkflow, RefRaw: build + "@v1", RefValue: "v1",
				FilePath: ".github/workflows/ci.yml", StartLine: 9})
			line := "vendor/flows:.github/workflows/build.yml:20:9"
			uses := func() {
				write("vendor/flows", flows, "b2", Edge{Key: line,
					SrcKey: "step:vendor/flows:.github/workflows/build.yml:publish:0", DstKey: "actions/cache",
					Kind: EdgeKindStepUsesAction, RefRaw: "actions/cache@v4", RefValue: "v4",
					FilePath: ".github/workflows/build.yml", StartLine: 20})
			}
			needs := func() {
				write("vendor/flows", flows, "a1", Edge{Key: line,
					SrcKey: "job:vendor/flows:.github/workflows/build.yml:publish",
					DstKey: "job:vendor/flows:.github/workflows/build.yml:prepare", Kind: EdgeKindJobNeedsJob,
					FilePath: ".github/workflows/build.yml", StartLine: 20})
			}
			if usesFirst {
				uses()
				needs()
			} else {
				needs()
				uses()
			}
			if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, ref, owner, repo, subpath,
				depth, status, enqueued_at, resolved_sha) VALUES
				('octo/app', '', 'octo', 'app', '', 0, 'done', '2026-10-01T00:00:00Z', 'app1'),
				(?, 'v1', 'vendor', 'flows', '.github/workflows/build.yml', 1, 'done',
				 '2026-10-01T00:00:00Z', 'b2')`, build); err != nil {
				t.Fatal(err)
			}

			got, err := s.SubmissionClosure("octo/app")
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, e := range got {
				names = append(names, e.Owner+"/"+e.Repo+"@"+e.Ref)
			}
			if !slices.Contains(names, "actions/cache@v4") {
				t.Errorf("the closure names %v, want actions/cache@v4, which build.yml at b2 uses", names)
			}
		})
	}
}

// An extended run re-observing a line a regular run wrote leaves the row the regular run's, as it
// leaves the row's run, so a reader of the regular graph never takes a closure database's declaring
// node from it; the closure's own sighting keeps what it declared.
func TestAnExtendedRunLeavesARegularRowsDeclarationTheRegularRunsOwn(t *testing.T) {
	f := newToolFixture(t)
	f.declare(t, toolUses)
	run, err := f.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeExtended, QueryID: "abom/edges",
		Repository: "vendor/tool", DBCommit: toolUses.commit})
	if err != nil {
		t.Fatal(err)
	}
	e := toolUses.edge
	e.SrcKey, e.FilePath, e.RepositoryID, e.QueryRunID = toolTestStep, toolWorkflowFile, f.repo, run
	if err := f.w.AddEdge(e); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Flush(); err != nil {
		t.Fatal(err)
	}

	var row, sighted string
	if err := f.DB().QueryRow(`SELECT s.node_key FROM abom_edge_live e
		JOIN abom_node_live s ON s.row_id = e.src_node_row_id WHERE e.edge_key = ?`,
		kindChangeLine).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if err := f.DB().QueryRow(`SELECT s.node_key FROM abom_edge_sighting sg
		JOIN abom_node_live s ON s.row_id = sg.src_node_row_id WHERE sg.query_run_id = ?`,
		run).Scan(&sighted); err != nil {
		t.Fatal(err)
	}
	if row != toolBackportStep || sighted != toolTestStep {
		t.Errorf("the row is declared by %s and the closure's sighting by %s, want %s and %s", row,
			sighted, toolBackportStep, toolTestStep)
	}
}

// What a rule reported on a line belongs to the version the line used at the finding's commit, and
// a `needs:` the line held at another commit does not make it a line that uses nothing, whichever
// of the two wrote the row: the line is a `uses:` line wherever its own commit's sighting says so.
func TestAFindingOnALineThatWasANeedsAtAnotherCommitCountsForTheActionItUsed(t *testing.T) {
	for _, needsFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("needs written first: %t", needsFirst), func(t *testing.T) {
			f := newLinesFixture(t)
			f.repository(t, "octo/app", false)
			add := adder(t, f.w)
			workflow := "octo/app/" + linesWorkflow
			for _, job := range []string{"build", "deploy"} {
				add(Node{Key: "job:octo/app:" + linesWorkflow + ":" + job, Kind: "job", JobID: job,
					StepIndex: -1, RepositoryID: f.repos["octo/app"], ContainerKey: workflow})
			}
			needs := func() {
				run, err := f.s.RecordQueryRun(QueryRun{AnalysisMode: AnalysisModeRegular,
					QueryID: "abom/edges", Repository: "octo/app", DBCommit: "a3"})
				if err != nil {
					t.Fatal(err)
				}
				if err := f.w.AddEdge(Edge{Key: "octo/app:" + linesWorkflow + ":9:9",
					SrcKey: "job:octo/app:" + linesWorkflow + ":deploy",
					DstKey: "job:octo/app:" + linesWorkflow + ":build", Kind: EdgeKindJobNeedsJob,
					RepositoryID: f.repos["octo/app"], QueryRunID: run, FilePath: linesWorkflow,
					StartLine: 9, StartColumn: 9}); err != nil {
					t.Fatal(err)
				}
				if err := f.w.Flush(); err != nil {
					t.Fatal(err)
				}
			}
			if needsFirst {
				needs()
			}
			f.declare(t, "octo/app", "a2", 9, "vendor/act", "v1")
			if !needsFirst {
				needs()
			}
			f.report(t, "octo/app", "a2", 9, "actions/unpinned-tag", "x2", score(5))

			got := findingsOnLines(t, f.s, "vendor/act", map[string][]string{"v1": {"octo/app"}})
			if g := got["v1"]; g.Count != 1 || g.Commits != 1 || g.Analyzed != 1 {
				t.Errorf("v1: %+v, want the finding at a2, where the line used vendor/act@v1", g)
			}
		})
	}
}

// everyKind is one declaration of each kind at a location of its own, all at commit p1: a job calling
// a reusable workflow by its path and a workflow of its own repository, a step using an image, and a
// step of an action manifest using an action and an image, in action.yml and in a nested action.yaml.
var everyKind = []toolDeclaration{
	{"p1", Edge{Key: "vendor/tool:.github/workflows/release.yml:30:5", SrcKey: toolCallJob,
		DstKey: "vendor/lib/.github/workflows/build.yml", Kind: EdgeKindJobUsesReusableWorkflow,
		RefRaw: "vendor/lib/.github/workflows/build.yml@v1", RefValue: "v1", StartLine: 30, StartColumn: 5}},
	{"p1", Edge{Key: "vendor/tool:.github/workflows/release.yml:40:5", SrcKey: toolCallJob,
		DstKey: "vendor/tool/.github/workflows/build.yml", Kind: EdgeKindJobUsesReusableWorkflow,
		RefRaw: "./.github/workflows/build.yml", StartLine: 40, StartColumn: 5}},
	{"p1", Edge{Key: "vendor/tool:.github/workflows/release.yml:50:15", SrcKey: toolTestStep,
		DstKey: "docker://alpine:3", Kind: EdgeKindStepUsesAction, RefRaw: "docker://alpine:3",
		StartLine: 50, StartColumn: 15}},
	{"p1", Edge{Key: "vendor/tool:action.yml:12:9", SrcKey: "step:vendor/tool:action.yml::0",
		DstKey: "actions/cache", Kind: EdgeKindCompositeStepUsesAction, RefRaw: "actions/cache@v4",
		RefValue: "v4", FilePath: "action.yml", StartLine: 12, StartColumn: 9}},
	{"p1", Edge{Key: "vendor/tool:nested/action.yaml:7:9", SrcKey: "step:vendor/tool:nested/action.yaml::1",
		DstKey: "docker://alpine:3", Kind: EdgeKindCompositeStepUsesAction, RefRaw: "docker://alpine:3",
		FilePath: "nested/action.yaml", StartLine: 7, StartColumn: 9}},
}

// forgetKinds makes the database what one written before sightings kept their declaration's kind and
// source holds: none on any sighting, and kindChangeLine's row with its first writer's, the `needs:`.
func forgetKinds(t *testing.T, f *toolFixture) {
	t.Helper()
	ids := rowIDs(t, f.Store, toolBackportJob)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE abom_edge_sighting SET edge_kind = NULL, src_node_row_id = NULL`, nil},
		{`UPDATE abom_edge SET edge_kind = ?, src_node_row_id = ? WHERE edge_key = ?`,
			[]any{EdgeKindJobNeedsJob, ids[toolBackportJob], kindChangeLine}},
	} {
		if _, err := f.DB().Exec(statement.sql, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// A sighting written before sightings kept their declaration's kind reads the kind its target
// implies: a `needs:` names a job and nothing else does, and only a job's `uses:` calls a workflow.
// Whether a step's is a composite action's is a fact about the location's file, which its row's kind
// already states. The declaring node is the row's, since nothing recorded a better one.
func TestADeclarationRecordedBeforeKindsWereKeptReadsTheKindItsTargetImplies(t *testing.T) {
	f := newToolFixture(t)
	past := append([]toolDeclaration{toolNeeds, toolUses}, everyKind...)
	for _, d := range past {
		f.declare(t, d)
	}
	forgetKinds(t, f)

	if err := f.Migrate(); err != nil {
		t.Fatal(err)
	}

	rows, err := f.DB().Query(`SELECT LOWER(q.db_commit) || ' ' || e.edge_key,
		COALESCE(sg.edge_kind, '') || ' ' || COALESCE(src.node_key, '')
		  FROM abom_edge_sighting sg
		  JOIN abom_query_run q ON q.row_id = sg.query_run_id
		  JOIN abom_edge_live e ON e.row_id = sg.edge_row_id
		  LEFT JOIN abom_node_live src ON src.row_id = sg.src_node_row_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var at, declared string
		if err := rows.Scan(&at, &declared); err != nil {
			t.Fatal(err)
		}
		got[at] = declared
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, d := range past {
		src := d.edge.SrcKey
		if d.edge.Key == kindChangeLine {
			src = toolBackportJob
		}
		want[d.commit+" "+d.edge.Key] = d.edge.Kind + " " + src
	}
	if !maps.Equal(got, want) {
		t.Errorf("the sightings read\n%v\nwant\n%v", got, want)
	}

	uses := []Declaration{{File: toolWorkflowFile, Line: 108, Kind: EdgeKindStepUsesAction,
		Raw: toolLocalUses, Target: toolLocalAction}}
	for commit, want := range map[string][]Declaration{toolNeeds.commit: {}, toolUses.commit: uses} {
		got, err := f.CommitDeclarations("vendor/tool", []string{commit})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("CommitDeclarations(%s) = %+v, want %+v", commit, got, want)
		}
	}
}

// A row whose kind contradicts its own target is repaired when the database is opened, and a page
// that drew it is told to load the graph again, since it changed under a row id that page holds.
// Opening the database again changes nothing and tells nobody anything.
func TestOpeningADatabaseRepairsARowWhoseKindContradictsItsTarget(t *testing.T) {
	f := newToolFixture(t)
	f.declare(t, toolNeeds)
	f.declare(t, toolUses)
	forgetKinds(t, f)

	for _, want := range []bool{true, false} {
		seen, err := f.RecordGraphGeneration(GraphRebuild{})
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Migrate(); err != nil {
			t.Fatal(err)
		}
		var kind, src string
		if err := f.DB().QueryRow(`SELECT e.edge_kind, s.node_key FROM abom_edge_live e
			JOIN abom_node_live s ON s.row_id = e.src_node_row_id
			WHERE e.edge_key = ?`, kindChangeLine).Scan(&kind, &src); err != nil {
			t.Fatal(err)
		}
		if kind != EdgeKindStepUsesAction || src != toolBackportJob {
			t.Errorf("the row says %s from %s, want the step-uses-action its target implies, from "+
				"the only declaring node on record, %s", kind, src, toolBackportJob)
		}
		delta, err := f.GraphDelta(seen, FullGraphOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if delta.Reset != want {
			t.Errorf("opening the database told a page to reload: %t, want %t", delta.Reset, want)
		}
	}
}
