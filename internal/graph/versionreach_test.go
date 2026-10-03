package graph

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// versionsReaching lists, by repository, the commits of a node the per-version breakdown says it
// runs and the fewest hops to each.
func versionsReaching(t *testing.T, s *store.Store, key string) map[string]map[string]int {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT r.repository_full_name, v.commit_sha, v.hops
		  FROM abom_node_version_reach v
		  JOIN abom_node n ON n.row_id = v.node_row_id
		  JOIN repository r ON r.row_id = v.repository_row_id
		 WHERE n.node_key = ?`, key)
	if err != nil {
		t.Fatalf("reading the per-version breakdown for %q: %v", key, err)
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var repository, commit string
		var hops int
		if err := rows.Scan(&repository, &commit, &hops); err != nil {
			t.Fatal(err)
		}
		if out[repository] == nil {
			out[repository] = map[string]int{}
		}
		out[repository][commit] = hops
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// assertReachIsTheExposureWalk rebuilds reach and holds every stored figure to the walk exposure
// and the node page take: the same repositories at the same hops for every node, and a per-version
// breakdown whose union is exactly that. A reach that counted a version nobody runs would show up
// here as a repository the exposure walk does not name.
func assertReachIsTheExposureWalk(t *testing.T, s *store.Store) {
	t.Helper()
	b := &Builder{Store: s}
	if err := b.BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}
	m, err := b.load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	stored := map[int64]map[int64]int{}
	versions := map[int64]map[int64]int{}
	read := func(query string, into map[int64]map[int64]int) {
		t.Helper()
		rows, err := s.DB().Query(query)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		defer rows.Close()
		for rows.Next() {
			var node, repo int64
			var hops int
			if err := rows.Scan(&node, &repo, &hops); err != nil {
				t.Fatal(err)
			}
			if into[node] == nil {
				into[node] = map[int64]int{}
			}
			into[node][repo] = hops
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	read(`SELECT node_row_id, repository_row_id, hops FROM abom_node_reach`, stored)
	read(`SELECT node_row_id, repository_row_id, MIN(hops) FROM abom_node_version_reach
	       GROUP BY 1, 2`, versions)

	reaching := 0
	for _, node := range m.referenceable {
		walked := map[int64]int{}
		for repo, p := range m.exposureOf(node, 0) {
			walked[repo] = p.hops
		}
		if len(walked) > 0 {
			reaching++
		}
		if got := stored[node]; !maps.Equal(got, walked) && len(got)+len(walked) > 0 {
			t.Errorf("%s: reach stores %v, the exposure walk reaches %v", m.keyOf[node], got, walked)
		}
		if got := versions[node]; !maps.Equal(got, walked) && len(got)+len(walked) > 0 {
			t.Errorf("%s: the per-version breakdown unites to %v, the exposure walk reaches %v",
				m.keyOf[node], got, walked)
		}
	}
	if reaching == 0 {
		t.Fatal("no node reaches a repository, so the comparison is vacuous")
	}
}

// Reach orders the crawl and is what "used by" means, so it counts a repository toward a dependency
// only where a release it pins runs that dependency. Counted across releases, octo/other would
// inflate actions/cache, which only the release octo/app pins declares.
func TestReachCountsOnlyTheRepositoriesRunningAReleaseThatDeclaresIt(t *testing.T) {
	f := newReleaseFixture(t)
	f.pins(t, "octo/app", "v1")
	f.pins(t, "octo/other", "v2")
	if err := (&Builder{Store: f.s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	for key, want := range map[string]string{
		"actions/cache": "octo/app", "actions/setup-go": "octo/other",
	} {
		if got := reachedBy(t, f.s, key); len(got) != 1 || got[0] != want {
			t.Errorf("%s is reached by %v; only %s pins the release declaring it", key, got, want)
		}
		if repos, _, _ := blastRadius(t, f.s, key); repos != 1 {
			t.Errorf("%s counts %d dependent repositories; one runs it", key, repos)
		}
	}
}

// assertReachCarriesWhatTheClosureCarries holds stored reach to the submission closure, which
// answers the same question in SQL from the other end: every external dependency a repository under
// review reaches is one its closure carries, and the reverse. Reach and exposure share a walk, so
// this is the comparison that can catch the walk itself admitting too much or too little.
func assertReachCarriesWhatTheClosureCarries(t *testing.T, s *store.Store) {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT r.repository_full_name, COALESCE(group_concat(n.node_key, ','), '')
		  FROM repository r
		  LEFT JOIN abom_node_reach x ON x.repository_row_id = r.row_id
		  LEFT JOIN abom_node n ON n.row_id = x.node_row_id AND n.is_external = 1
		 WHERE r.origin = 'seed'
		 GROUP BY r.row_id`)
	if err != nil {
		t.Fatalf("reading reach by repository: %v", err)
	}
	reached := map[string]map[string]bool{}
	for rows.Next() {
		var repository, keys string
		if err := rows.Scan(&repository, &keys); err != nil {
			t.Fatal(err)
		}
		reached[repository] = map[string]bool{}
		for _, key := range strings.Split(keys, ",") {
			if key != "" {
				reached[repository][key] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	carrying := 0
	for repository, keys := range reached {
		carried := submittedFrom(t, s, repository)
		if len(carried) > 0 {
			carrying++
		}
		if !maps.Equal(keys, carried) {
			t.Errorf("%s reaches %v; its submission closure carries %v", repository,
				slices.Sorted(maps.Keys(keys)), slices.Sorted(maps.Keys(carried)))
		}
	}
	if carrying == 0 {
		t.Fatal("no closure carries anything, so the comparison is vacuous")
	}
}

// Reach, exposure and the node page are one walk, and the submission closure answers the same
// question independently. Walks over one graph that quietly differ are found only when two figures
// cannot both be right, so each fixture that exercises a version constraint is held to both here.
func TestReachAgreesWithTheExposureWalkForEveryNode(t *testing.T) {
	releases := func(t *testing.T) *store.Store {
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
		return f.s
	}
	deepLocks := func(t *testing.T) *store.Store {
		s := lockedVendorFixture(t)
		deepenLockedVendor(t, s)
		return s
	}
	for name, build := range map[string]func(*testing.T) *store.Store{
		"releases":                         releases,
		"pinned version":                   pinnedVersionFixture,
		"locks":                            lockedVendorFixture,
		"deep locks":                       deepLocks,
		"unresolved under a caller's lock": unresolvedLockedMid,
		"supply chain":                     agreementFixture,
	} {
		t.Run(name, func(t *testing.T) {
			s := build(t)
			assertReachIsTheExposureWalk(t, s)
			assertReachCarriesWhatTheClosureCarries(t, s)
		})
	}
	// The shared fixture links owned repositories through nodes that are not external, which the
	// submission closure never leaves a repository through; "supply chain" is it made walkable.
	t.Run("shared", func(t *testing.T) { assertReachIsTheExposureWalk(t, fixture(t)) })
}

// A dependency's reach is the union over the versions somebody runs, and the breakdown says which
// version each repository runs: the commit its reference resolves to, even where an identical
// commit's analysis answers for it. A reference resolving to nothing recorded runs a version ABOM
// has no reading of, so it is attributed to no commit and reaches nothing through it.
func TestReachRecordsWhichReleaseEachRepositoryRuns(t *testing.T) {
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
	if err := (&Builder{Store: f.s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	want := map[string]map[string]int{
		"octo/app":        {"c1": 1},
		"octo/other":      {"c2": 1},
		"octo/patched":    {"c1-rebuilt": 1},
		"octo/unanalyzed": {"": 1},
	}
	if got := versionsReaching(t, f.s, "vendor/comp"); !maps.EqualFunc(got, want, maps.Equal) {
		t.Errorf("vendor/comp is run at %v, want %v", got, want)
	}
	cache := versionsReaching(t, f.s, "actions/cache")
	if _, ok := cache["octo/unanalyzed"]; ok {
		t.Errorf("octo/unanalyzed reaches actions/cache through a release ABOM has no reading of: %v", cache)
	}
	if _, ok := cache["octo/patched"]; !ok {
		t.Errorf("octo/patched runs a release identical to c1, which declares actions/cache: %v", cache)
	}
}

// A lock chooses the commit, and reach per version follows it exactly as exposure does: a caller's
// lock binds its own route, and a repository naming the same commit without one runs the tag's.
func TestReachPerVersionFollowsTheCommitALockSelects(t *testing.T) {
	s := lockedVendorFixture(t)
	deepenLockedVendor(t, s)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	if got := versionsReaching(t, s, "vendor/action"); got["octo/locked"][vendorLocked] != 1 ||
		got["octo/unlocked"][vendorLive] != 1 || len(got["octo/locked"]) != 1 ||
		len(got["octo/unlocked"]) != 1 {
		t.Errorf("vendor/action is run at %v; octo/locked's lock selects %s and octo/unlocked "+
			"runs the tag at %s", got, vendorLocked, vendorLive)
	}
	mid := versionsReaching(t, s, "mid/composite")
	if hops, ok := mid["octo/locked"][midLocked]; !ok || hops != 2 || len(mid["octo/locked"]) != 1 {
		t.Errorf("octo/locked runs mid/composite at %v; its lock selects %s two hops down",
			mid["octo/locked"], midLocked)
	}
	if hops, ok := mid["octo/pinned"][midLive]; !ok || hops != 2 || len(mid["octo/pinned"]) != 1 {
		t.Errorf("octo/pinned runs mid/composite at %v; it has no lock, so it runs the tag's %s",
			mid["octo/pinned"], midLive)
	}
}

// unresolvedLockedMid is the deep lock fixture with mid/composite@v1 resolving to nothing recorded,
// so only octo/locked's caller lock names a commit for that reference.
func unresolvedLockedMid(t *testing.T) *store.Store {
	s := lockedVendorFixture(t)
	deepenLockedVendor(t, s)
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET resolved_sha = NULL
		WHERE node_key = 'mid/composite' AND ref = 'v1'`); err != nil {
		t.Fatal(err)
	}
	return s
}

// A reference that resolves to nothing recorded still runs something, and a caller's lock naming a
// commit for it binds only that caller. Everyone else runs a version ABOM has no reading of: still a
// dependent, attributed to no commit, and never through the locked caller's route.
func TestAnUnresolvedReferenceUnderACallersLockRunsAnUnknownVersionForEveryoneElse(t *testing.T) {
	s := unresolvedLockedMid(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	want := map[string]map[string]int{
		"octo/locked": {midLocked: 2},
		"octo/pinned": {"": 2},
	}
	if got := versionsReaching(t, s, "mid/composite"); !maps.EqualFunc(got, want, maps.Equal) {
		t.Errorf("mid/composite is run at %v, want %v", got, want)
	}
	if got := reachedBy(t, s, "leaf/tool"); len(got) != 1 || got[0] != "octo/locked" {
		t.Errorf("leaf/tool is reached by %v; only octo/locked's lock selects the commit declaring it", got)
	}
}
