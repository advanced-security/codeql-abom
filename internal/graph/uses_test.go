package graph

import (
	"maps"
	"slices"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// usesOf answers the details page's table for one node, failing the test on error.
func usesOf(t *testing.T, s *store.Store, key string, q UsesQuery) UsesPage {
	t.Helper()
	page, err := (&Builder{Store: s}).UsesOf(key, q)
	if err != nil {
		t.Fatalf("UsesOf(%q): %v", key, err)
	}
	return page
}

// useKeys names each row as repository@reference=commit/hops, sorted, so a whole answer compares.
func useKeys(page UsesPage) []string {
	out := []string{}
	for _, u := range page.Items {
		out = append(out, u.Repository+"@"+u.Reference+"="+u.Commit+"/"+string(rune('0'+u.Hops)))
	}
	slices.Sort(out)
	return out
}

// The table is the walk that writes reach, so with nothing asked its repositories under review are
// exactly the stored breakdown and its versions are exactly the stored breakdown by version.
func TestTheUsesTableIsTheStoredReach(t *testing.T) {
	deepLocks := func(t *testing.T) *store.Store {
		s := lockedVendorFixture(t)
		deepenLockedVendor(t, s)
		return s
	}
	for name, build := range map[string]func(*testing.T) *store.Store{
		"releases":                         func(t *testing.T) *store.Store { return releasesInUse(t).s },
		"pinned version":                   pinnedVersionFixture,
		"deep locks":                       deepLocks,
		"unresolved under a caller's lock": unresolvedLockedMid,
		"supply chain":                     agreementFixture,
		"shared":                           fixture,
		"a starting point is a dependency": func(t *testing.T) *store.Store { return toolFixture(t, true).s },
		"no default branch on record":      func(t *testing.T) *store.Store { return toolFixture(t, false).s },
	} {
		t.Run(name, func(t *testing.T) {
			s := build(t)
			b := &Builder{Store: s}
			if err := b.BuildBlastRadius(); err != nil {
				t.Fatal(err)
			}
			m, err := b.load()
			if err != nil {
				t.Fatal(err)
			}
			compared := 0
			for _, node := range m.referenceable {
				key := m.keyOf[node]
				page := usesOf(t, s, key, UsesQuery{Limit: 1000})
				byRepo := map[string]int{}
				byVersion := map[string]map[string]int{}
				for _, u := range page.Items {
					if u.Hops == 0 {
						continue
					}
					if h, ok := byRepo[u.Repository]; !ok || u.Hops < h {
						byRepo[u.Repository] = u.Hops
					}
					if byVersion[u.Repository] == nil {
						byVersion[u.Repository] = map[string]int{}
					}
					if h, ok := byVersion[u.Repository][u.Commit]; !ok || u.Hops < h {
						byVersion[u.Repository][u.Commit] = u.Hops
					}
				}
				stored := map[string]int{}
				for _, name := range reachedBy(t, s, key) {
					stored[name] = 0
				}
				for name := range stored {
					stored[name] = byRepo[name]
				}
				if got := slices.Sorted(maps.Keys(byRepo)); !slices.Equal(got, slices.Sorted(maps.Keys(stored))) {
					t.Errorf("%s: the table lists %v, stored reach %v", key, got, slices.Sorted(maps.Keys(stored)))
				}
				if want := versionsReaching(t, s, key); !maps.EqualFunc(byVersion, want, maps.Equal) {
					t.Errorf("%s: the table runs %v, stored reach by version %v", key, byVersion, want)
				}
				// The inventory's count of references sits on the row the versions table opens under.
				versions, err := b.VersionsOf(key, UsesQuery{})
				if err != nil {
					t.Fatal(err)
				}
				// A grouping recounts every level from the repositories in it, so the parts are the whole.
				if total, parts := storedLevels(t, s, key), storedLevelsSummed(t, s, key); total != parts {
					t.Errorf("%s: stored reach totals %+v, its repositories sum to %+v", key, total, parts)
				}
				if got := storedReferences(t, s, key); got != versions.Total {
					t.Errorf("%s: stored reach counts %d references in use, the versions table %d",
						key, got, versions.Total)
				}
				compared += len(byRepo)
			}
			if compared == 0 {
				t.Fatal("no node is used by anything, so the comparison is vacuous")
			}
		})
	}
}

func storedReferences(t *testing.T, s *store.Store, key string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT b.refs_in_use FROM abom_blast_radius b
		JOIN abom_node n ON n.row_id = b.node_row_id WHERE n.node_key = ?`, key).Scan(&n); err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return n
}

// releasesInUse pins vendor/comp at each kind of release: two analyzed ones, one identical to an
// analyzed one, and one ABOM has no reading of.
func releasesInUse(t *testing.T) *releaseFixture {
	t.Helper()
	f := newReleaseFixture(t)
	f.resolve(t, "v1.0.1", "c1-rebuilt")
	for _, c := range []string{"c1", "c1-rebuilt"} {
		if err := f.s.RecordCommitContent("vendor/comp", c, "same-tree"); err != nil {
			t.Fatal(err)
		}
	}
	f.pins(t, "octo/app", "v1")
	f.pins(t, "octo/other", "v2")
	f.pins(t, "octo/patched", "v1.0.1")
	f.pins(t, "octo/unanalyzed", "v9")
	return f
}

// A row says which reference reached the dependency and which commit that runs, directly or
// through what the repository names. Through vendor/comp, actions/cache is reached by the reference
// vendor/comp's own release wrote, not by the one the repository wrote for vendor/comp.
func TestAUseNamesTheReferenceWrittenAndTheCommitItRuns(t *testing.T) {
	f := releasesInUse(t)

	if got, want := useKeys(usesOf(t, f.s, "vendor/comp", UsesQuery{})), []string{
		"octo/app@v1=c1/1", "octo/other@v2=c2/1", "octo/patched@v1.0.1=c1-rebuilt/1",
		"octo/unanalyzed@v9=/1",
	}; !slices.Equal(got, want) {
		t.Errorf("vendor/comp is used as %v, want %v", got, want)
	}
	cache := usesOf(t, f.s, "actions/cache", UsesQuery{})
	if got, want := useKeys(cache), []string{"octo/app@v3=/2", "octo/patched@v3=/2"}; !slices.Equal(got, want) {
		t.Errorf("actions/cache is used as %v, want %v", got, want)
	}
	for _, u := range cache.Items {
		if u.Through != "vendor/comp" || u.Lines != 0 {
			t.Errorf("%s reaches actions/cache through %q with %d lines of its own; want through "+
				"vendor/comp and none to edit", u.Repository, u.Through, u.Lines)
		}
		if len(u.Workflows) != 1 || u.Workflows[0] != u.Repository+"/.github/workflows/ci.yml" {
			t.Errorf("%s names workflows %v", u.Repository, u.Workflows)
		}
	}
}

// What a lock selects is what runs, and the row says the lock chose it: a moved tag does not move a
// repository whose lock pins the commit.
func TestAUseSaysWhenALockChoseTheCommit(t *testing.T) {
	s := lockedVendorFixture(t)
	page := usesOf(t, s, "vendor/action", UsesQuery{})
	got := map[string]bool{}
	for _, u := range page.Items {
		got[u.Repository+"="+u.Commit] = u.Locked
	}
	want := map[string]bool{"octo/locked=" + vendorLocked: true, "octo/unlocked=" + vendorLive: false}
	if !maps.Equal(got, want) {
		t.Errorf("vendor/action rows (locked?) %v, want %v", got, want)
	}
}

// Upstream repositories carry routes but are nobody's to change here, so they are hidden by
// default and counted; asking for them by origin shows them.
func TestUpstreamUsesAreHiddenAndCountedUntilAskedFor(t *testing.T) {
	f := releasesInUse(t)
	upstream, err := f.s.InsertUpstreamRepository("third/party")
	if err != nil {
		t.Fatal(err)
	}
	workflow := "third/party/.github/workflows/ci.yml"
	f.node(t, store.Node{Key: workflow, Kind: "workflow", RepositoryID: upstream})
	f.node(t, store.Node{Key: "third-step", Kind: "step", RepositoryID: upstream, ContainerKey: workflow})
	if err := f.w.AddEdge(store.Edge{Key: "third/party:ci.yml:3:3", SrcKey: "third-step",
		DstKey: "vendor/comp", Kind: store.EdgeKindStepUsesAction, RefValue: "v2",
		RepositoryID: upstream, FilePath: ".github/workflows/ci.yml", StartLine: 3}); err != nil {
		t.Fatal(err)
	}
	f.flush(t)

	page := usesOf(t, f.s, "vendor/comp", UsesQuery{})
	if page.HiddenUpstream != 1 || slices.Contains(useKeys(page), "third/party@v2=c2/1") {
		t.Errorf("by default %v with %d upstream hidden; want third/party hidden and counted",
			useKeys(page), page.HiddenUpstream)
	}
	asked := usesOf(t, f.s, "vendor/comp", UsesQuery{Origins: []string{"upstream"}})
	if got := useKeys(asked); !slices.Equal(got, []string{"third/party@v2=c2/1"}) || asked.HiddenUpstream != 0 {
		t.Errorf("origins=upstream answered %v, hidden %d", got, asked.HiddenUpstream)
	}
}

// A user is owned where the declaration covers its repository, never because it was seeded: of
// four starting points the two declared answer origins=owned, and seed still answers all four.
func TestAUseIsOwnedOnlyWhereTheDeclarationCoversItsRepository(t *testing.T) {
	f := releasesInUse(t)
	if err := f.s.SetOwnedScope([]store.DeclaredAccount{{Owner: "octo", Kind: store.AccountOrganization,
		Enabled: true, IncludesOnly: true, Includes: []string{"app", "other"}}}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	all := usesOf(t, f.s, "vendor/comp", UsesQuery{})
	if len(all.Items) != 4 {
		t.Fatalf("vendor/comp has %d users, want the fixture's four", len(all.Items))
	}
	for _, u := range all.Items {
		if want := u.Repository == "octo/app" || u.Repository == "octo/other"; u.Owned != want {
			t.Errorf("%s (origin %q) reads owned=%v, want %v", u.Repository, u.Origin, u.Owned, want)
		}
	}
	owned := usesOf(t, f.s, "vendor/comp", UsesQuery{Origins: []string{store.OwnedOrigin}})
	if got, want := useKeys(owned), []string{"octo/app@v1=c1/1", "octo/other@v2=c2/1"}; !slices.Equal(got, want) {
		t.Errorf("origins=owned answered %v, want %v", got, want)
	}
	if seeds := usesOf(t, f.s, "vendor/comp", UsesQuery{Origins: []string{store.OriginSeed}}); len(seeds.Items) != 4 {
		t.Errorf("origins=seed answered %v, want every starting point", useKeys(seeds))
	}
}

// The terms narrow the rows before they are counted, and the count is of the match, not the page.
func TestTheUsesTermsNarrowBeforeCountingAndPagingCutsTheMatch(t *testing.T) {
	f := releasesInUse(t)
	for _, c := range []struct {
		name string
		q    UsesQuery
		want []string
	}{
		{"one reference", UsesQuery{At: []string{"v2"}}, []string{"octo/other@v2=c2/1"}},
		{"transitive", UsesQuery{Reached: []string{"transitive"}}, nil},
		{"named repositories", UsesQuery{Repositories: map[string]bool{"octo/app": true}},
			[]string{"octo/app@v1=c1/1"}},
		{"commit style", UsesQuery{RefStyle: "sha"}, nil},
	} {
		page := usesOf(t, f.s, "vendor/comp", c.q)
		if got := useKeys(page); !slices.Equal(got, c.want) && len(got)+len(c.want) > 0 {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if page.Total != len(c.want) {
			t.Errorf("%s: total %d, want %d", c.name, page.Total, len(c.want))
		}
	}
	paged := usesOf(t, f.s, "vendor/comp", UsesQuery{Sort: "reference", Desc: true, Offset: 1, Limit: 2})
	if got := []string{paged.Items[0].Reference, paged.Items[1].Reference}; paged.Total != 4 ||
		!slices.Equal(got, []string{"v2", "v1.0.1"}) {
		t.Errorf("sorted by reference descending, page 2 of 2 is %v of %d", got, paged.Total)
	}
	if _, err := (&Builder{Store: f.s}).UsesOf("vendor/comp", UsesQuery{Sort: "nonsense"}); err == nil {
		t.Error("an unknown sort was accepted; it would show an order nobody asked for")
	}
}

// If a tag moves, who runs something different: everyone it reaches with no lock, directly and
// through an intermediary kept apart, while a lock keeps its own repository where it was. A commit
// does not move, and a tag ABOM has no reading of names no commit.
func TestIfATagChangesSaysWhoMovesAndWhoALockKeeps(t *testing.T) {
	s := lockedVendorFixture(t)
	change := usesOf(t, s, "vendor/action", UsesQuery{At: []string{"v1"}}).Change
	if change == nil {
		t.Fatal("one tag was asked for and nothing said what moving it would do")
	}
	if change.NamesNow != vendorLive || !slices.Equal(change.Direct, []string{"octo/unlocked"}) ||
		len(change.Transitive) != 0 || !slices.Equal(change.Kept, []string{"octo/locked"}) {
		t.Errorf("moving v1: %+v; want octo/unlocked moved directly and octo/locked kept by its lock", *change)
	}

	f := releasesInUse(t)
	cache := usesOf(t, f.s, "actions/cache", UsesQuery{At: []string{"v3"}}).Change
	if cache == nil || len(cache.Direct) != 0 ||
		!slices.Equal(cache.Transitive, []string{"octo/app", "octo/patched"}) || cache.NamesNow != "" {
		t.Errorf("moving actions/cache@v3: %+v; want two repositories through vendor/comp, and no "+
			"commit named because nothing resolved it", cache)
	}

	for _, q := range []UsesQuery{{}, {At: []string{"v1", "v2"}}} {
		if got := usesOf(t, s, "vendor/action", q).Change; got != nil {
			t.Errorf("at=%v answered what moving a tag would do: %+v", q.At, *got)
		}
	}
	pinned := usesOf(t, s, "vendor/action",
		UsesQuery{At: []string{"5555555555555555555555555555555555555555"}}).Change
	if pinned == nil || !pinned.Fixed || len(pinned.Direct)+len(pinned.Transitive)+len(pinned.Kept) > 0 {
		t.Errorf("a commit reference answered %+v; a commit does not move, and that is the whole answer", pinned)
	}
}

// A lock on the commit a tag names now still keeps its repository there when the tag moves, and it
// keeps that repository alone: another naming the same tag with no lock moves with it.
func TestALockOnTheCommitTheTagNamesNowKeepsOnlyItsOwnRepository(t *testing.T) {
	s := lockedVendorFixture(t)
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET resolved_sha = ?
		WHERE node_key = 'vendor/action' AND ref = 'v1'`, vendorLocked); err != nil {
		t.Fatal(err)
	}
	change := usesOf(t, s, "vendor/action", UsesQuery{At: []string{"v1"}}).Change
	if change == nil || !slices.Equal(change.Direct, []string{"octo/unlocked"}) ||
		!slices.Equal(change.Kept, []string{"octo/locked"}) {
		t.Errorf("moving v1 from the locked commit: %+v; want octo/unlocked moved, octo/locked kept", change)
	}
}
