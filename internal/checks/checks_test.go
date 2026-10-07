package checks

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/graph"
	"github.com/advanced-security/codeql-abom/internal/store"
)

func exec(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// fixture: octo/app@a1 runs vendor/act@v1 (c1, also analyzed at c2), which runs actions/cache@v4.
func fixture(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "octo", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	app, err := s.UpsertRepository("octo/app", "")
	if err != nil {
		t.Fatal(err)
	}
	vendor, err := s.InsertUpstreamRepository("vendor/act")
	if err != nil {
		t.Fatal(err)
	}
	run := func(repository, commit string) int64 {
		id, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
			QueryID: "abom/edges", Repository: repository, DBCommit: commit})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSightedContent(id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	atA1, atC1 := run("octo/app", "a1"), run("vendor/act", "c1")
	w := store.NewWriter(s, 0)
	workflow := "octo/app/.github/workflows/ci.yml"
	for _, n := range []store.Node{
		{Key: workflow, Kind: "workflow", RepositoryID: app, Path: ".github/workflows/ci.yml"},
		{Key: "octo/app:build:9", Kind: "step", RepositoryID: app, ContainerKey: workflow,
			JobID: "build"},
		{Key: "vendor/act", Kind: "action", Owner: "vendor", Repo: "act", IsExternal: true},
		{Key: "vendor/act:step:5", Kind: "step", RepositoryID: vendor, ContainerKey: "vendor/act"},
		{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
	} {
		if err := w.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range []store.Edge{
		{Key: "octo/app:ci.yml:9:9", SrcKey: "octo/app:build:9", DstKey: "vendor/act",
			Kind: store.EdgeKindStepUsesAction, RefRaw: "vendor/act@v1", RefValue: "v1",
			RepositoryID: app, QueryRunID: atA1, FilePath: ".github/workflows/ci.yml",
			StartLine: 9, StartColumn: 9},
		{Key: "vendor/act:action.yml:5:9", SrcKey: "vendor/act:step:5", DstKey: "actions/cache",
			Kind: store.EdgeKindCompositeStepUsesAction, RefRaw: "actions/cache@v4", RefValue: "v4",
			RepositoryID: vendor, QueryRunID: atC1, FilePath: "action.yml", StartLine: 5,
			StartColumn: 9},
	} {
		if err := w.AddEdge(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	exec(t, s, `INSERT INTO abom_edge_sighting (edge_row_id, query_run_id, dst_node_row_id, ref_value)
		SELECT row_id, ?, dst_node_row_id, 'v4' FROM abom_edge_live
		 WHERE edge_key = 'vendor/act:action.yml:5:9'`, run("vendor/act", "c2"))
	for _, q := range []struct {
		key, ref string
		depth    int
		commit   string
	}{{"octo/app", "", 0, "a1"}, {"vendor/act", "v1", 1, "c1"}} {
		owner, repo, _ := strings.Cut(q.key, "/")
		exec(t, s, `INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
			enqueued_at, resolved_sha) VALUES (?, ?, ?, ?, ?, 'done', '2026-10-01T00:00:00Z', ?)`,
			q.key, q.ref, owner, repo, q.depth, q.commit)
	}
	for _, pkg := range []string{"vendor/act", "actions/cache"} {
		if _, err := s.AddAdvisories([]store.Advisory{{GHSAID: "GHSA-" + pkg, Severity: "high",
			Summary: pkg, Packages: []store.AdvisoryPackage{
				{Ecosystem: "actions", Name: pkg, VulnerableRange: "< 99"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// derivedFixture is the fixture with reach derived and marked, as a serving process leaves it.
func derivedFixture(t *testing.T) *store.Store {
	t.Helper()
	s := fixture(t)
	if _, err := (&graph.Builder{Store: s}).Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return s
}

func run(t *testing.T, s *store.Store) []store.CheckRun {
	t.Helper()
	runs, err := Run(s, "test-build", t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return runs
}

func partOf(t *testing.T, runs []store.CheckRun, check int, part string) store.CheckPart {
	t.Helper()
	for _, r := range runs {
		if r.Check != check {
			continue
		}
		for _, p := range r.Parts {
			if p.Part == part || strings.HasPrefix(p.Part, part+" ") {
				return p
			}
		}
		t.Fatalf("check %d made no comparison named %q: %+v", check, part, r.Parts)
	}
	t.Fatalf("check %d did not run: %+v", check, runs)
	return store.CheckPart{}
}

// A corrupt reach row survives incremental derivation, so only the recompute can disagree with it.
func TestCheck7CatchesAMutatedStoredReachRow(t *testing.T) {
	s := derivedFixture(t)
	exec(t, s, `UPDATE abom_node_reach SET hops = 9 WHERE node_row_id =
		(SELECT row_id FROM abom_node_live WHERE node_key = 'actions/cache')`)

	runs := run(t, s)
	got := partOf(t, runs, 7, "abom_node_reach")
	if got.Compared == 0 || len(got.Disagreements) != 1 {
		t.Fatalf("check 7 compared %d reach subjects and reports %+v, want the mutated row alone",
			got.Compared, got.Disagreements)
	}
	d := got.Disagreements[0]
	if d.Subject != "actions/cache" || !strings.Contains(d.Claim, "octo/app|9|") ||
		!strings.Contains(d.Independent, "octo/app|2|") {
		t.Errorf("the disagreement reads %+v, want actions/cache at 9 hops against 2", d)
	}
	for _, other := range []string{"abom_blast_radius", "abom_node_version_reach", "abom_exposure",
		"abom_edge.advisory_route"} {
		if p := partOf(t, runs, 7, other); p.Compared == 0 || len(p.Disagreements) > 0 {
			t.Errorf("%s compared %d and reports %+v, want agreement", other, p.Compared,
				p.Disagreements)
		}
	}
	page, err := s.OpenDisagreements(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].Check != 7 || page.Items[0].Class != "derivation" {
		t.Errorf("the queue holds %+v, want the one check 7 disagreement classed as derivation", page)
	}
}

// Exposure is rewritten only where its inputs moved, so a corrupt row survives the same way.
func TestCheck7CatchesAMutatedExposureRow(t *testing.T) {
	s := derivedFixture(t)
	exec(t, s, `UPDATE abom_exposure SET hops = 9 WHERE ghsa_id = 'GHSA-actions/cache'`)

	got := partOf(t, run(t, s), 7, "abom_exposure")
	if len(got.Disagreements) != 1 || got.Disagreements[0].Subject != "actions/cache" {
		t.Errorf("check 7 reports %+v, want the mutated exposure of actions/cache alone",
			got.Disagreements)
	}
}

// A refused incremental path is no reading at all, which is unknown and never zero disagreements.
func TestCheck7IsSkippedWithItsReasonWhenTheIncrementalPathRefuses(t *testing.T) {
	s := fixture(t)
	run(t, s)

	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	seven, ok := latest[7]
	if !ok || len(seven.Parts) == 0 {
		t.Fatalf("check 7's latest run is %+v, want one with its parts", seven)
	}
	for _, p := range seven.Parts {
		if p.Disagreed != nil || p.Compared != 0 {
			t.Errorf("%s compared %d and reports %v disagreements; nothing was read", p.Part,
				p.Compared, p.Disagreed)
		}
		if len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0].Reason, graph.ErrNoMark.Error()) {
			t.Errorf("%s skipped %+v, want the refusal as its reason", p.Part, p.Skipped)
		}
	}
	for _, check := range []int{8, 9} {
		compared := 0
		for _, p := range latest[check].Parts {
			compared += p.Compared
		}
		if compared == 0 {
			t.Errorf("check %d compared nothing; it reads the wholesale copy, which still exists",
				check)
		}
	}
}

// A check that read nothing found no agreement: zero disagreements from it would read as health.
func TestChecks8And9AreSkippedWhenTheRecomputeTheyReadFailed(t *testing.T) {
	s := derivedFixture(t)
	exec(t, s, `CREATE TRIGGER planted_failure BEFORE INSERT ON abom_blast_radius
		BEGIN SELECT RAISE(ABORT, 'planted failure'); END`)
	run(t, s)

	latest, err := s.LatestCheckRuns()
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []int{7, 8, 9} {
		for _, p := range latest[check].Parts {
			if p.Compared != 0 || p.Disagreed != nil || len(p.Skipped) != 1 ||
				!strings.Contains(p.Skipped[0].Reason, "planted failure") {
				t.Errorf("check %d's %s compared %d and skipped %+v, want nothing read and the "+
					"failed recompute named", check, p.Part, p.Compared, p.Skipped)
			}
		}
		if len(latest[check].Parts) == 0 {
			t.Errorf("check %d recorded no run", check)
		}
	}
}

// Each invariant is its own comparison, and must catch a violation of exactly what it states.
func TestCheck9CatchesEachPlantedViolation(t *testing.T) {
	clean, err := invariants(derivedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(clean) != 6 {
		t.Fatalf("check 9 made %d comparisons, want one per invariant a to f", len(clean))
	}
	for _, p := range clean {
		if p.Compared == 0 {
			t.Errorf("%s compared nothing on the fixture, so catching a plant would be vacuous",
				p.Part)
		}
		for _, d := range p.Disagreements {
			t.Errorf("%s disagrees before anything was planted: %s: %s / %s", p.Part, d.Subject,
				d.Claim, d.Independent)
		}
	}

	for _, c := range []struct {
		part, subject string
		plant         func(*testing.T, *store.Store)
	}{
		{"9a", "dependencies", func(t *testing.T, s *store.Store) {
			// A composite only a closure database observed: the Actions unit lists regular evidence.
			id, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeExtended,
				QueryID: "abom/edges", Repository: "octo/app", DBCommit: "a1"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.UpsertRepository("octo/app", "")
			if err != nil {
				t.Fatal(err)
			}
			w := store.NewWriter(s, 0)
			local := "octo/app/.github/actions/setup"
			for _, n := range []store.Node{
				{Key: local, Kind: "composite_action", RepositoryID: app, Path: ".github/actions/setup"},
				{Key: local + ":step:3", Kind: "step", RepositoryID: app, ContainerKey: local},
				{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
			} {
				if err := w.AddNode(n); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.AddEdge(store.Edge{Key: "octo/app:setup:3:9", SrcKey: local + ":step:3",
				DstKey: "actions/cache", Kind: store.EdgeKindCompositeStepUsesAction,
				RefValue: "v4", RepositoryID: app, QueryRunID: id,
				FilePath: ".github/actions/setup/action.yml", StartLine: 3}); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
		}},
		{"9b", "actions/cache", func(t *testing.T, s *store.Store) {
			exec(t, s, `UPDATE abom_blast_radius SET dependent_repos = 5 WHERE node_row_id =
				(SELECT row_id FROM abom_node_live WHERE node_key = 'actions/cache')`)
		}},
		{"9c", "actions/cache -> vendor/act", func(t *testing.T, s *store.Store) {
			exec(t, s, `INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops)
				SELECT n.row_id, r.row_id, 1 FROM abom_node_live n, repository r
				 WHERE n.node_key = 'actions/cache' AND r.repository_full_name = 'vendor/act'`)
		}},
		{"9d", "octo/app:ci.yml:9:9", func(t *testing.T, s *store.Store) {
			// The same line, analyzed at a second commit, naming a job rather than an action.
			id, err := s.RecordQueryRun(store.QueryRun{AnalysisMode: store.AnalysisModeRegular,
				QueryID: "abom/edges", Repository: "octo/app", DBCommit: "a2"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.UpsertRepository("octo/app", "")
			if err != nil {
				t.Fatal(err)
			}
			w := store.NewWriter(s, 0)
			if err := w.AddNode(store.Node{Key: "octo/app/.github/workflows/ci.yml:deploy",
				Kind: "job", RepositoryID: app, ContainerKey: "octo/app/.github/workflows/ci.yml",
				JobID: "deploy"}); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			exec(t, s, `INSERT INTO abom_edge_sighting (edge_row_id, query_run_id, dst_node_row_id)
				SELECT e.row_id, ?, n.row_id FROM abom_edge_live e, abom_node_live n
				 WHERE e.edge_key = 'octo/app:ci.yml:9:9'
				   AND n.node_key = 'octo/app/.github/workflows/ci.yml:deploy'`, id)
		}},
		{"9e", "octo/app@a9", func(t *testing.T, s *store.Store) {
			// A branch the queue calls done at a commit nothing analyzed.
			exec(t, s, `INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status,
				enqueued_at, resolved_sha) VALUES ('octo/app', 'release', 'octo', 'app', 0, 'done',
				'2026-10-01T00:00:00Z', 'a9')`)
		}},
		{"9f", "relationships", func(t *testing.T, s *store.Store) {
			// A live line whose target was tombstoned on its own, which a prune never does.
			exec(t, s, `UPDATE abom_node SET removed_at = '2026-10-02T00:00:00Z'
				WHERE node_key = 'actions/cache'`)
		}},
	} {
		t.Run(c.part, func(t *testing.T) {
			s := derivedFixture(t)
			c.plant(t, s)
			parts, err := invariants(s)
			if err != nil {
				t.Fatal(err)
			}
			var got store.CheckPart
			for _, p := range parts {
				if strings.HasPrefix(p.Part, c.part+" ") {
					got = p
				}
			}
			subjects := []string{}
			for _, d := range got.Disagreements {
				subjects = append(subjects, d.Subject)
			}
			if !slices.Contains(subjects, c.subject) {
				t.Errorf("%q reports %v after comparing %d, want %s among them", got.Part, subjects,
					got.Compared, c.subject)
			}
		})
	}
}

// contents reads every table outside the checker's own, and the schema, as sorted text.
func contents(t *testing.T, s *store.Store) map[string][]string {
	t.Helper()
	rows, err := s.DB().Query(`SELECT type, name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'abom_check%' AND name NOT LIKE 'idx_check%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	out := map[string][]string{}
	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		out["schema"] = append(out["schema"], kind+" "+name+" "+ddl)
		if kind == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	for _, table := range tables {
		rows, err := s.DB().Query(`SELECT * FROM "` + table + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		lines := []string{}
		for rows.Next() {
			values := make([]any, len(columns))
			into := make([]any, len(columns))
			for i := range values {
				into[i] = &values[i]
			}
			if err := rows.Scan(into...); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%q", values))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		slices.Sort(lines)
		out[table] = lines
	}
	return out
}

// The checker cannot change what it measures, so every table outside its own is untouched.
func TestTheCheckerWritesNothingOutsideItsOwnTables(t *testing.T) {
	s := derivedFixture(t)
	before := contents(t, s)
	run(t, s)
	after := contents(t, s)

	for name, rows := range before {
		if !slices.Equal(rows, after[name]) {
			t.Errorf("%s changed during a run:\nbefore %v\nafter  %v", name, rows, after[name])
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			t.Errorf("a run created %s", name)
		}
	}
	var recorded int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_check_run`).Scan(&recorded); err != nil ||
		recorded == 0 {
		t.Fatalf("the run recorded %d check runs (%v), so the comparison above is vacuous",
			recorded, err)
	}
}
