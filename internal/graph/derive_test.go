package graph

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// derivedRows reads every maintained row, the way check 7 reads both copies it compares.
func derivedRows(t *testing.T, s *store.Store) []store.DerivedTable {
	t.Helper()
	rows, err := s.DerivedRows()
	if err != nil {
		t.Fatalf("DerivedRows: %v", err)
	}
	return rows
}

// differences lists, table by table, the rows only one side holds, as check 7 reports them.
func differences(got, want []store.DerivedTable) []string {
	var out []string
	for _, p := range CompareDerived(got, want) {
		for _, d := range p.Disagreements {
			out = append(out, fmt.Sprintf("%s %s: derived %s; the rebuild %s", p.Part, d.Subject,
				d.Claim, d.Independent))
		}
	}
	return out
}

// bystanders are starting points vendor/comp never touches; octo/web has a spare job and workflow.
func bystanders(t *testing.T, s *store.Store) {
	t.Helper()
	w := store.NewWriter(s, 0)
	actions := []string{"actions/checkout", "actions/setup-node"}
	for _, a := range actions {
		owner, name, _ := strings.Cut(a, "/")
		if err := w.AddNode(store.Node{Key: a, Kind: "action", Owner: owner, Repo: name,
			IsExternal: true}); err != nil {
			t.Fatal(err)
		}
	}
	uses := func(repo int64, workflow, job, step, action string, line int) {
		t.Helper()
		if err := w.AddNode(store.Node{Key: step, Kind: "step", RepositoryID: repo,
			ContainerKey: workflow, JobID: job}); err != nil {
			t.Fatal(err)
		}
		if err := w.AddEdge(store.Edge{Key: step + ":uses", SrcKey: step, DstKey: action,
			Kind: store.EdgeKindStepUsesAction, RefRaw: action + "@v4", RefValue: "v4", RepositoryID: repo,
			FilePath: ".github/workflows/build.yml", StartLine: line, StartColumn: 9}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"octo/web", "octo/api", "octo/docs"} {
		repo, err := s.UpsertRepository(name, "")
		if err != nil {
			t.Fatal(err)
		}
		workflow := name + "/.github/workflows/build.yml"
		if err := w.AddNode(store.Node{Key: workflow, Kind: "workflow", RepositoryID: repo}); err != nil {
			t.Fatal(err)
		}
		for i, a := range actions {
			uses(repo, workflow, "build", fmt.Sprintf("%s-build-%d", name, i), a, 10+i)
		}
		if name == "octo/web" {
			uses(repo, workflow, "test", "octo/web-test-0", "actions/checkout", 20)
			if err := w.AddNode(store.Node{Key: name + "/.github/workflows/release.yml", Kind: "workflow",
				RepositoryID: repo}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	affects(t, s, "GHSA-checkout", "actions/checkout", "")
}

func exec(t *testing.T, s *store.Store, query string, args ...any) {
	t.Helper()
	if _, err := s.DB().Exec(query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// derivationFixture is vendor/comp's releases in use beside repositories that never touch it.
func derivationFixture(t *testing.T) *store.Store {
	t.Helper()
	f := releasesInUse(t)
	bystanders(t, f.s)
	return f.s
}

// lockedDerivationFixture is the locked vendor three levels deep, beside the same bystanders.
func lockedDerivationFixture(t *testing.T) *store.Store {
	t.Helper()
	s := lockedVendorFixture(t)
	deepenLockedVendor(t, s)
	bystanders(t, s)
	return s
}

// unanalyzedResolutionFixture resolves v9 to c9 before anything analyzed c9, as the queue does.
func unanalyzedResolutionFixture(t *testing.T) *store.Store {
	t.Helper()
	f := releasesInUse(t)
	f.resolve(t, "v9", "c9")
	bystanders(t, f.s)
	return f.s
}

// assertDerivedIsTheRebuild holds Derive to a Rebuild after it, row for row, and returns both counts.
func assertDerivedIsTheRebuild(t *testing.T, s *store.Store, before []store.DerivedTable) (
	derived, rebuilt Derivation, moved bool) {
	t.Helper()
	b := &Builder{Store: s}
	derived, err := b.Derive()
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	got := derivedRows(t, s)
	if rebuilt, err = b.Rebuild(); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	want := derivedRows(t, s)
	for _, d := range differences(got, want) {
		t.Error(d)
	}
	return derived, rebuilt, len(differences(before, want)) > 0
}

// A derived row the rebuild would not write is a figure shown as measured that nothing measured.
func TestIncrementalDerivationEqualsTheRebuildAfterEachChange(t *testing.T) {
	cases := []struct {
		name    string
		fixture func(*testing.T) *store.Store
		change  func(*testing.T, *store.Store)
	}{
		{"a ref moves to a commit declaring different children", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_frontier SET resolved_sha = 'c2'
				             WHERE node_key = 'vendor/comp' AND ref = 'v1'`)
			}},
		{"a commit is observed for the first time and a ref moves to it", derivationFixture,
			func(t *testing.T, s *store.Store) {
				f := &releaseFixture{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
				if err := s.DB().QueryRow(`SELECT row_id FROM repository
					WHERE repository_full_name = 'vendor/comp'`).Scan(&f.comp); err != nil {
					t.Fatal(err)
				}
				f.node(t, store.Node{Key: "vendor/comp", Kind: "action", Owner: "vendor", Repo: "comp",
					IsExternal: true})
				f.node(t, store.Node{Key: "comp-step", Kind: "step", RepositoryID: f.comp,
					ContainerKey: "vendor/comp"})
				f.node(t, store.Node{Key: "actions/upload-artifact", Kind: "action", Owner: "actions",
					Repo: "upload-artifact", IsExternal: true})
				f.flush(t)
				f.declare(t, "c3", "actions/upload-artifact", "v4")
				exec(t, s, `UPDATE abom_frontier SET resolved_sha = 'c3'
				             WHERE node_key = 'vendor/comp' AND ref = 'v2'`)
			}},
		{"a commit a reference already resolved to is analyzed", unanalyzedResolutionFixture,
			func(t *testing.T, s *store.Store) {
				f := &releaseFixture{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
				if err := s.DB().QueryRow(`SELECT row_id FROM repository
					WHERE repository_full_name = 'vendor/comp'`).Scan(&f.comp); err != nil {
					t.Fatal(err)
				}
				for _, n := range []store.Node{
					{Key: "vendor/comp", Kind: "action", Owner: "vendor", Repo: "comp", IsExternal: true},
					{Key: "comp-step", Kind: "step", RepositoryID: f.comp, ContainerKey: "vendor/comp"},
					{Key: "actions/cache", Kind: "action", Owner: "actions", Repo: "cache", IsExternal: true},
				} {
					f.node(t, n)
				}
				f.flush(t)
				f.declare(t, "c9", "actions/cache", "v3")
			}},
		{"a step moves to another job", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_node SET job_id = 'build' WHERE node_key = 'octo/web-test-0'`)
			}},
		{"a step moves to another workflow", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_node SET container_node_row_id = (SELECT row_id FROM abom_node
				              WHERE node_key = 'octo/web/.github/workflows/release.yml')
				             WHERE node_key = 'octo/web-test-0'`)
			}},
		{"a step is attributed to another repository", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_node SET repository_row_id = (SELECT row_id FROM repository
				              WHERE repository_full_name = 'octo/api')
				             WHERE node_key = 'octo/web-test-0'`)
			}},
		{"a starting point adds a reference", derivationFixture,
			func(t *testing.T, s *store.Store) {
				f := &releaseFixture{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
				f.node(t, store.Node{Key: "vendor/comp", Kind: "action", Owner: "vendor", Repo: "comp",
					IsExternal: true})
				f.pins(t, "octo/new", "v2")
			}},
		{"a dependency nothing referenced before is referenced", derivationFixture,
			func(t *testing.T, s *store.Store) {
				w := store.NewWriter(s, 0)
				repo, err := s.UpsertRepository("octo/web", "")
				if err != nil {
					t.Fatal(err)
				}
				for _, n := range []store.Node{
					{Key: "octo/fresh", Kind: "action", Owner: "octo", Repo: "fresh", IsExternal: true},
					{Key: "octo/web-build-9", Kind: "step", RepositoryID: repo,
						ContainerKey: "octo/web/.github/workflows/build.yml", JobID: "build"},
				} {
					if err := w.AddNode(n); err != nil {
						t.Fatal(err)
					}
				}
				if err := w.AddEdge(store.Edge{Key: "octo/web-build-9:uses", SrcKey: "octo/web-build-9",
					DstKey: "octo/fresh", Kind: store.EdgeKindStepUsesAction, RefRaw: "octo/fresh@v1",
					RefValue: "v1", RepositoryID: repo, FilePath: ".github/workflows/build.yml",
					StartLine: 30, StartColumn: 9}); err != nil {
					t.Fatal(err)
				}
				if err := w.Flush(); err != nil {
					t.Fatal(err)
				}
			}},
		{"nothing resolves a ref any more", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_frontier SET resolved_sha = NULL
				             WHERE node_key = 'vendor/comp' AND ref = 'v2'`)
			}},
		{"a reference is tombstoned", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_edge SET removed_at = '2026-10-01T00:00:00Z'
				             WHERE edge_key = 'octo/other:.github/workflows/ci.yml:9:9'`)
			}},
		{"an upstream repository becomes a starting point", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE repository SET origin = 'seed'
				             WHERE repository_full_name = 'vendor/comp'`)
				startingPointOf(s).queued(t, "vendor/comp", "", 0, "c2")
			}},
		{"a commit live as a dependency becomes a protected branch's",
			func(t *testing.T) *store.Store { return toolFixture(t, true).s },
			func(t *testing.T, s *store.Store) {
				startingPointOf(s).queued(t, "acme/tool", "release", 0, "r1")
			}},
		{"a starting point's default branch is recorded",
			func(t *testing.T) *store.Store { return toolFixture(t, false).s },
			func(t *testing.T, s *store.Store) {
				startingPointOf(s).queued(t, "acme/tool", "", 0, "d1")
			}},
		{"an advisory arrives", derivationFixture,
			func(t *testing.T, s *store.Store) { affects(t, s, "GHSA-comp", "vendor/comp", "") }},
		{"an advisory is withdrawn", derivationFixture,
			func(t *testing.T, s *store.Store) {
				exec(t, s, `UPDATE abom_advisory SET withdrawn_at = '2026-10-01T00:00:00Z'
				             WHERE ghsa_id = 'GHSA-cache'`)
			}},
		{"a lock selects another commit", lockedDerivationFixture,
			func(t *testing.T, s *store.Store) {
				if err := s.RecordLockReading("octo/unlocked", unlockedRoot, "test-reader",
					[]store.LockCoverage{{Coverage: "covered", Schema: "v0.0.2"},
						{WorkflowPath: ".github/workflows/ci.yml", Coverage: "covered", Schema: "v0.0.2"}},
					store.LockGraph{
						Dependencies: []store.LockDependency{
							{Pin: "vendor/action@v1", Owner: "vendor", Repo: "action", Ref: "v1",
								Algorithm: "sha1", Digest: vendorLocked},
							{Pin: "older/tool@v1", Owner: "older", Repo: "tool", Ref: "v1",
								Algorithm: "sha1", Digest: olderLocked},
						},
						Uses: []store.LockUse{{Parent: "vendor/action@v1", Child: "older/tool@v1"}},
						WorkflowPins: []store.LockWorkflowPin{
							{WorkflowPath: ".github/workflows/ci.yml", Pin: "vendor/action@v1"}},
					}); err != nil {
					t.Fatalf("RecordLockReading: %v", err)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.fixture(t)
			if _, err := (&Builder{Store: s}).Rebuild(); err != nil {
				t.Fatalf("Rebuild: %v", err)
			}
			before := derivedRows(t, s)
			c.change(t, s)

			derived, rebuilt, moved := assertDerivedIsTheRebuild(t, s, before)
			t.Logf("derived %+v; rebuilt %+v", derived, rebuilt)
			if !moved {
				t.Fatal("the change moved nothing the rebuild derives, so the comparison is vacuous")
			}
			if derived.Walked >= rebuilt.Walked || derived.Rows >= rebuilt.Rows {
				t.Errorf("derived from the change walked %d and wrote %d rows; the rebuild walked %d "+
					"and wrote %d", derived.Walked, derived.Rows, rebuilt.Walked, rebuilt.Rows)
			}
		})
	}
}

// A moved reference is the change incremental derivation exists for, so every fixture moves each one.
func TestIncrementalDerivationEqualsTheRebuildWhenAnyReferenceMoves(t *testing.T) {
	deepLocks := func(t *testing.T) *store.Store {
		s := lockedVendorFixture(t)
		deepenLockedVendor(t, s)
		return s
	}
	compared, moved := 0, 0
	for name, build := range map[string]func(*testing.T) *store.Store{
		"releases":                         func(t *testing.T) *store.Store { return releasesInUse(t).s },
		"pinned version":                   pinnedVersionFixture,
		"deep locks":                       deepLocks,
		"unresolved under a caller's lock": unresolvedLockedMid,
		"supply chain":                     agreementFixture,
		"shared":                           fixture,
		"bystanders":                       derivationFixture,
		"a starting point is a dependency": func(t *testing.T) *store.Store { return toolFixture(t, true).s },
		"no default branch on record":      func(t *testing.T) *store.Store { return toolFixture(t, false).s },
	} {
		t.Run(name, func(t *testing.T) {
			s := build(t)
			if _, err := (&Builder{Store: s}).Rebuild(); err != nil {
				t.Fatalf("Rebuild: %v", err)
			}
			type resolution struct{ key, ref, repository, commit string }
			var resolutions []resolution
			rows, err := s.DB().Query(`SELECT node_key, ref, owner || '/' || repo, resolved_sha
				FROM abom_frontier WHERE COALESCE(resolved_sha, '') <> '' ORDER BY 1, 2`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var r resolution
				if err := rows.Scan(&r.key, &r.ref, &r.repository, &r.commit); err != nil {
					t.Fatal(err)
				}
				resolutions = append(resolutions, r)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			for _, r := range resolutions {
				others, err := s.DB().Query(`SELECT DISTINCT db_commit FROM abom_query_run
					WHERE LOWER(repository) = LOWER(?) AND COALESCE(db_commit, '') <> ''
					  AND LOWER(db_commit) <> LOWER(?) ORDER BY 1`, r.repository, r.commit)
				if err != nil {
					t.Fatal(err)
				}
				var commits []string
				for others.Next() {
					var c string
					if err := others.Scan(&c); err != nil {
						t.Fatal(err)
					}
					commits = append(commits, c)
				}
				if err := others.Err(); err != nil {
					t.Fatal(err)
				}
				others.Close()
				for _, to := range append(commits, r.commit) {
					before := derivedRows(t, s)
					exec(t, s, `UPDATE abom_frontier SET resolved_sha = ? WHERE node_key = ? AND ref = ?`,
						to, r.key, r.ref)
					if _, _, changed := assertDerivedIsTheRebuild(t, s, before); changed {
						moved++
					}
					compared++
				}
			}
		})
	}
	t.Logf("%d moves compared, %d moved what the rebuild derives", compared, moved)
	if compared == 0 || moved == 0 {
		t.Fatalf("%d moves compared, %d of them moved anything, so the comparison is vacuous", compared, moved)
	}
}

// The cost of keeping reach current tracks the change, so no change costs no walk and no write.
func TestADerivationWithNothingChangedWalksAndWritesNothing(t *testing.T) {
	s := derivationFixture(t)
	b := &Builder{Store: s}
	rebuilt, err := b.Rebuild()
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if rebuilt.Walked == 0 || rebuilt.Rows == 0 {
		t.Fatalf("the rebuild reports walking %d and writing %d rows, so the comparison is vacuous",
			rebuilt.Walked, rebuilt.Rows)
	}
	d, err := b.Derive()
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if d.Changed != 0 || d.Walked != 0 || d.Rows != 0 {
		t.Errorf("with nothing changed the derivation reports %+v", d)
	}
}

// A derivation that guessed what changed would leave a missing row reading as a measured zero.
func TestDerivingWithoutAMarkRefusesRatherThanGuessing(t *testing.T) {
	for name, prepare := range map[string]func(*testing.T, *store.Store, *Builder){
		"nothing was ever derived": func(*testing.T, *store.Store, *Builder) {},
		"reach was rewritten without saying what it read": func(t *testing.T, s *store.Store, b *Builder) {
			if _, err := b.Rebuild(); err != nil {
				t.Fatal(err)
			}
			if err := b.BuildBlastRadius(); err != nil {
				t.Fatal(err)
			}
		},
		"exposure was rewritten without saying what it read": func(t *testing.T, s *store.Store, b *Builder) {
			if _, err := b.Rebuild(); err != nil {
				t.Fatal(err)
			}
			if err := b.BuildExposure(); err != nil {
				t.Fatal(err)
			}
		},
		"the walk before this one wrote the mark": func(t *testing.T, s *store.Store, b *Builder) {
			if _, err := b.Rebuild(); err != nil {
				t.Fatal(err)
			}
			exec(t, s, `UPDATE abom_reach_mark SET version = replace(version, ?, 'reach-inputs/1')`,
				ReachMarkVersion)
		},
		"another derivation wrote the mark": func(t *testing.T, s *store.Store, b *Builder) {
			if _, err := b.Rebuild(); err != nil {
				t.Fatal(err)
			}
			exec(t, s, `UPDATE abom_reach_mark SET version = 'another build'`)
		},
		"the walk was bounded differently": func(t *testing.T, s *store.Store, b *Builder) {
			if _, err := (&Builder{Store: s, MaxDepth: 1}).Rebuild(); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := derivationFixture(t)
			b := &Builder{Store: s}
			prepare(t, s, b)
			exec(t, s, `UPDATE abom_frontier SET resolved_sha = 'c2'
			             WHERE node_key = 'vendor/comp' AND ref = 'v1'`)
			before := derivedRows(t, s)

			d, err := b.Derive()
			if !errors.Is(err, ErrNoMark) {
				t.Fatalf("Derive = %+v, %v; want ErrNoMark", d, err)
			}
			for _, diff := range differences(derivedRows(t, s), before) {
				t.Errorf("a refused derivation wrote: %s", diff)
			}
		})
	}
}

// No row under a watermark saying reach is measured reads as "nothing depends on this".
func TestANodeFirstReferencedSinceTheMarkIsMeasuredNotLeftMissing(t *testing.T) {
	s := derivationFixture(t)
	b := &Builder{Store: s}
	if _, err := b.Rebuild(); err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{s: s, w: store.NewWriter(s, 0), runs: map[string]int64{}}
	f.node(t, store.Node{Key: "octo/fresh", Kind: "action", Owner: "octo", Repo: "fresh", IsExternal: true})
	f.flush(t)
	repo, err := s.UpsertRepository("octo/api", "")
	if err != nil {
		t.Fatal(err)
	}
	f.node(t, store.Node{Key: "octo/api-build-9", Kind: "step", RepositoryID: repo,
		ContainerKey: "octo/api/.github/workflows/build.yml", JobID: "build"})
	f.flush(t)
	if err := f.w.AddEdge(store.Edge{Key: "octo/api-build-9:uses", SrcKey: "octo/api-build-9",
		DstKey: "octo/fresh", Kind: store.EdgeKindStepUsesAction, RefRaw: "octo/fresh@v1",
		RefValue: "v1", RepositoryID: repo, FilePath: ".github/workflows/build.yml",
		StartLine: 30, StartColumn: 9}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)

	if _, err := b.Derive(); err != nil {
		t.Fatalf("Derive: %v", err)
	}
	var repos int
	err = s.DB().QueryRow(`SELECT b.dependent_repos FROM abom_blast_radius b
		JOIN abom_node n ON n.row_id = b.node_row_id WHERE n.node_key = 'octo/fresh'`).Scan(&repos)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("octo/fresh has no reach row after the derivation, which reads as nothing depending on it")
	}
	if err != nil {
		t.Fatal(err)
	}
	if repos != 1 {
		t.Errorf("octo/fresh is run by %d repositories, want octo/api", repos)
	}
}
