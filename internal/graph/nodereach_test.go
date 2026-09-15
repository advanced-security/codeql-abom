package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// reachedBy names the repositories the breakdown records as reaching a node.
func reachedBy(t *testing.T, s *store.Store, key string) []string {
	t.Helper()
	rows, err := s.DB().Query(`
		SELECT r.repository_full_name
		FROM abom_node_reach nr
		JOIN abom_node n ON n.row_id = nr.node_row_id
		JOIN repository r ON r.row_id = nr.repository_row_id
		WHERE n.node_key = ?
		ORDER BY r.repository_full_name`, key)
	if err != nil {
		t.Fatalf("reading the breakdown for %q: %v", key, err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The invariant that makes the breakdown trustworthy: it is the same measurement as the rollup,
// so the two can never disagree about how far a node reaches.
//
// Both are written from one walk. Derived separately they would drift, and a count that disagrees
// with the list beneath it is worse than either alone.
func TestTheReachBreakdownAgreesWithTheRollupForEveryNode(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	rows, err := s.DB().Query(`
		SELECT b.node_row_id, b.dependent_repos,
		       (SELECT COUNT(*) FROM abom_node_reach nr WHERE nr.node_row_id = b.node_row_id)
		FROM abom_blast_radius b`)
	if err != nil {
		t.Fatalf("comparing the rollup with the breakdown: %v", err)
	}
	defer rows.Close()

	var checked, reaching int
	for rows.Next() {
		var node int64
		var rollup, breakdown int
		if err := rows.Scan(&node, &rollup, &breakdown); err != nil {
			t.Fatal(err)
		}
		checked++
		if rollup > 0 {
			reaching++
		}
		if rollup != breakdown {
			t.Errorf("node %d: the rollup counts %d repositories, the breakdown lists %d",
				node, rollup, breakdown)
		}
	}
	if checked == 0 {
		t.Fatal("no blast radius rows; the fixture proves nothing")
	}
	if reaching == 0 {
		t.Fatal("no node reaches any repository; the comparison is vacuous")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// The breakdown names repositories, not just a quantity of them — which is the whole reason for
// storing it, since a count cannot be filtered.
func TestTheReachBreakdownNamesTheRepositories(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	repos, _, _ := blastRadius(t, s, "actions/checkout")
	named := reachedBy(t, s, "actions/checkout")
	if len(named) != repos {
		t.Fatalf("the rollup counts %d repositories, the breakdown names %v", repos, named)
	}
	for _, name := range named {
		if name == "" {
			t.Error("the breakdown names an empty repository")
		}
	}
}

// An upstream repository is not a repository the organization can act on, so it is absent from the
// breakdown for the same reason it is absent from the count.
func TestTheReachBreakdownExcludesUpstreamRepositories(t *testing.T) {
	s := fixture(t)
	upstreamUser(t, s, "third-party/tool", "actions/checkout")
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	for _, name := range reachedBy(t, s, "actions/checkout") {
		if name == "third-party/tool" {
			t.Error("the breakdown names an upstream repository as depending on the action")
		}
	}
}

// Direct and transitive reach are different remediation work, so the breakdown records which it is
// rather than only that the repository is affected.
func TestTheReachBreakdownDistinguishesDirectFromTransitive(t *testing.T) {
	s := fixture(t)
	if err := (&Builder{Store: s}).BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}

	var low, high, total int
	if err := s.DB().QueryRow(
		`SELECT COALESCE(MIN(hops), 0), COALESCE(MAX(hops), 0), COUNT(*) FROM abom_node_reach`).
		Scan(&low, &high, &total); err != nil {
		t.Fatal(err)
	}
	if total == 0 {
		t.Fatal("no reach rows; the fixture proves nothing")
	}
	if low < 1 {
		t.Errorf("a repository reaches a node in %d hops; the nearest possible is 1", low)
	}
	if high <= low {
		t.Errorf("every one of %d reach rows is %d hops away, so the fixture cannot tell direct "+
			"from transitive and this assertion is vacuous", total, low)
	}
}

// Reach is recomputed wholesale, so the breakdown has to be replaced rather than added to. Left
// accumulating, a node would appear to reach repositories that stopped reaching it.
func TestRebuildingReplacesTheReachBreakdown(t *testing.T) {
	s := fixture(t)
	b := &Builder{Store: s}
	if err := b.BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius: %v", err)
	}
	first := reachedBy(t, s, "actions/checkout")

	if err := b.BuildBlastRadius(); err != nil {
		t.Fatalf("BuildBlastRadius again: %v", err)
	}
	second := reachedBy(t, s, "actions/checkout")

	if len(second) != len(first) {
		t.Errorf("a second build lists %d repositories against %d in the first: %v",
			len(second), len(first), second)
	}
}
