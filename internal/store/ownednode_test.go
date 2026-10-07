package store

import (
	"testing"
	"time"
)

// Yours is what the declaration covers, never how a repository arrived. In declaringApp only
// octo-org/app is declared: octo-org/tool is a starting point like it, octo-org/shared arrived
// upstream, and calling either owned would claim a repository nobody declared.
func TestANodeIsOwnedOnlyWhereTheDeclarationCoversItsRepository(t *testing.T) {
	s := declaringApp(t)
	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	owned := 0
	for _, n := range g.Nodes {
		want := n.Repository == "octo-org/app" || n.Key == OrgKey("octo-org")
		if n.Owned != want {
			t.Errorf("%s (%s, origin %q) reads owned=%v, want %v", n.Key, n.Kind, n.Origin, n.Owned, want)
		}
		if n.Owned {
			owned++
		}
	}
	if owned < 3 {
		t.Fatalf("only %d nodes read owned; the fixture should hold app's workflow, its repository and its account", owned)
	}

	// The owned scope holds exactly what reads owned: the fact and the scope are one predicate.
	scoped, err := s.FullGraph(FullGraphOptions{Scope: ScopeOwned})
	if err != nil {
		t.Fatalf("FullGraph(owned): %v", err)
	}
	inScope := keysOf(scoped)
	for _, n := range g.Nodes {
		if n.Owned && !inScope[n.Key] {
			t.Errorf("%s reads owned and the owned scope does not hold it", n.Key)
		}
	}
	for _, n := range scoped.Nodes {
		if !n.Owned {
			t.Errorf("the owned scope holds %s (%s, origin %q), which is not yours", n.Key, n.Kind, n.Origin)
		}
	}
}

// A repository a full listing of its account no longer names is not yours, whatever the declaration
// says about the account: the same predicate the owned scope roots on decides it.
func TestARepositoryTheListingNoLongerNamesIsNotOwned(t *testing.T) {
	s := declaringApp(t)
	before, err := s.NodeDetailOf("octo-org/app/.github/workflows/ci.yml", nil)
	if err != nil || !before.Owned {
		t.Fatalf("app's workflow reads owned=%v (%v) while its repository is listed; this proves nothing", before.Owned, err)
	}
	if _, err := s.DB().Exec(`INSERT INTO abom_repository_absence (repository, missing_at) VALUES (?, ?)`,
		"octo-org/app", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	g, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	for _, n := range g.Nodes {
		if n.Owned {
			t.Errorf("%s reads owned after its repository left the listing", n.Key)
		}
	}
}

// origins=owned keeps exactly what reads owned, and seed keeps every starting point as it always
// has: a shared link asking for seeds must not quietly stop listing the ones you own.
func TestTheOwnedOriginKeepsExactlyWhatReadsOwnedAndSeedKeepsItsMeaning(t *testing.T) {
	s := declaringApp(t)
	all, err := s.FullGraph(FullGraphOptions{})
	if err != nil {
		t.Fatalf("FullGraph: %v", err)
	}
	want := map[string]bool{}
	for _, n := range all.Nodes {
		if n.Owned {
			want[n.Key] = true
		}
	}
	if len(want) == 0 {
		t.Fatal("nothing reads owned, so this proves nothing")
	}
	owned, err := s.FullGraph(FullGraphOptions{Origins: []string{OwnedOrigin}})
	if err != nil {
		t.Fatalf("FullGraph(origins=owned): %v", err)
	}
	got := keysOf(owned)
	for key := range want {
		if !got[key] {
			t.Errorf("origins=owned dropped %s, which reads owned", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("origins=owned kept %s, which is not yours", key)
		}
	}

	seeds, err := s.FullGraph(FullGraphOptions{Origins: []string{OriginSeed}})
	if err != nil {
		t.Fatalf("FullGraph(origins=seed): %v", err)
	}
	for _, key := range []string{"octo-org/app/.github/workflows/ci.yml", "octo-org/tool/.github/workflows/ci.yml"} {
		if !keysOf(seeds)[key] {
			t.Errorf("origins=seed dropped the starting point %s", key)
		}
	}
}

// The Actions unit carries the same fact, filters on it, and sorts its Origin column in the words it
// shows: owned, then a starting point nobody declared, then upstream, then external.
func TestTheActionsUnitSaysWhatIsOwnedFiltersAndSortsByIt(t *testing.T) {
	s := declaringApp(t)
	kinds := []string{"workflow", "reusable_workflow", "action"}
	page, err := s.ActionsInventoryPage(InventoryQuery{Kinds: kinds, Limit: 100})
	if err != nil {
		t.Fatalf("ActionsInventoryPage: %v", err)
	}
	ownedRows := 0
	for _, row := range page.Items {
		want := row.NodeKey == "octo-org/app/.github/workflows/ci.yml"
		if row.Owned != want {
			t.Errorf("%s (origin %q) reads owned=%v, want %v", row.NodeKey, row.Origin, row.Owned, want)
		}
		if row.Owned {
			ownedRows++
		}
	}
	if ownedRows != 1 {
		t.Fatalf("listed %d owned rows among %+v; the fixture proves nothing", ownedRows, page.Items)
	}

	owned, err := s.ActionsInventoryPage(InventoryQuery{Kinds: kinds, Origins: []string{OwnedOrigin}, Limit: 100})
	if err != nil {
		t.Fatalf("ActionsInventoryPage(origins=owned): %v", err)
	}
	if owned.Total != 1 || len(owned.Items) != 1 || !owned.Items[0].Owned {
		t.Errorf("origins=owned listed %+v (total %d), want app's workflow alone", owned.Items, owned.Total)
	}
	seeds, err := s.ActionsInventoryPage(InventoryQuery{Kinds: kinds, Origins: []string{OriginSeed}, Limit: 100})
	if err != nil {
		t.Fatalf("ActionsInventoryPage(origins=seed): %v", err)
	}
	seeded := map[string]bool{}
	for _, row := range seeds.Items {
		seeded[row.NodeKey] = true
	}
	if !seeded["octo-org/app/.github/workflows/ci.yml"] || !seeded["octo-org/tool/.github/workflows/ci.yml"] {
		t.Errorf("origins=seed listed %v, want both starting points", seeded)
	}

	rank := func(a ActionSummary) int {
		switch {
		case a.Owned:
			return 0
		case a.Origin == OriginSeed:
			return 1
		case a.Origin == OriginUpstream:
			return 2
		}
		return 3
	}
	for _, desc := range []bool{false, true} {
		sorted, err := s.ActionsInventoryPage(InventoryQuery{Kinds: kinds, Sort: "origin", Desc: desc, Limit: 100})
		if err != nil {
			t.Fatalf("sorting by origin: %v", err)
		}
		ranks := map[int]bool{}
		for i := 1; i < len(sorted.Items); i++ {
			before, after := rank(sorted.Items[i-1]), rank(sorted.Items[i])
			if (!desc && before > after) || (desc && before < after) {
				t.Errorf("sorting by origin (desc=%v) lists %s after %s", desc,
					sorted.Items[i].NodeKey, sorted.Items[i-1].NodeKey)
			}
		}
		for _, row := range sorted.Items {
			ranks[rank(row)] = true
		}
		if len(ranks) < 3 {
			t.Fatalf("sorting by origin listed %d distinct origins; the fixture proves nothing", len(ranks))
		}
	}
}

// A details page reads the same fact for the node it is about.
func TestANodesDetailsSayWhetherItIsOwned(t *testing.T) {
	s := declaringApp(t)
	for key, want := range map[string]bool{
		"octo-org/app/.github/workflows/ci.yml":  true,
		"octo-org/tool/.github/workflows/ci.yml": false,
		"actions/checkout":                       false,
	} {
		d, err := s.NodeDetailOf(key, nil)
		if err != nil {
			t.Fatalf("NodeDetailOf(%s): %v", key, err)
		}
		if d.Owned != want {
			t.Errorf("%s (origin %q) reads owned=%v, want %v", key, d.Origin, d.Owned, want)
		}
	}
}
