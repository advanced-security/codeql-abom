package frontier

import "testing"

// fixedChain answers for a handful of keys and knows nothing about the rest, which is what a
// whole-graph judgement looks like to the queue.
type fixedChain map[string]bool

func (c fixedChain) Of(nodeKey string) (runtime, known bool) {
	runtime, known = c[nodeKey]
	return runtime, known
}

func chainOf(t *testing.T, q *Queue, key string) string {
	t.Helper()
	var chain string
	if err := q.Store.DB().QueryRow(
		`SELECT supply_chain FROM abom_frontier WHERE node_key = ?`, key).Scan(&chain); err != nil {
		t.Fatalf("reading supply_chain for %s: %v", key, err)
	}
	return chain
}

// Enqueue may only ever strengthen a classification, so a rule that admits fewer runtime
// dependencies can never take effect through it. Without a pass that is allowed to lower one,
// the stored answer stays at whatever the loosest rule ever said — which is how 254 rows on the
// reference corpus were still labeled runtime under a rule that no longer claimed it.
func TestReclassifyLowersAClaimEnqueueWouldHaveKept(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if got := chainOf(t, q, "a"); got != SupplyChainRuntime {
		t.Fatalf("fixture setup: supply_chain = %q, want %q", got, SupplyChainRuntime)
	}

	n, err := q.Reclassify(fixedChain{"a": false})
	if err != nil {
		t.Fatalf("Reclassify: %v", err)
	}

	if n != 1 {
		t.Errorf("Reclassify changed %d rows, want 1", n)
	}
	if got := chainOf(t, q, "a"); got != SupplyChainBuild {
		t.Errorf("supply_chain = %q, want %q", got, SupplyChainBuild)
	}
}

// A dependency nothing in the graph names has not been measured, and an unmeasured row is not a
// build-time row. Demoting it would assert something no query ever said.
func TestReclassifyLeavesUnclassifiedRowsAlone(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	n, err := q.Reclassify(fixedChain{})
	if err != nil {
		t.Fatalf("Reclassify: %v", err)
	}

	if n != 0 {
		t.Errorf("Reclassify changed %d rows, want 0", n)
	}
	if got := chainOf(t, q, "a"); got != SupplyChainRuntime {
		t.Errorf("supply_chain = %q, want it untouched at %q", got, SupplyChainRuntime)
	}
}

// The pass runs on every drained cycle, so it has to settle. One that kept reporting changes
// would have the supervisor logging a reclassification all night against a graph nothing moved.
func TestReclassifyIsIdempotent(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{item("a", 1), item("b", 1)}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	chain := fixedChain{"a": false, "b": true}

	if _, err := q.Reclassify(chain); err != nil {
		t.Fatalf("first Reclassify: %v", err)
	}
	n, err := q.Reclassify(chain)
	if err != nil {
		t.Fatalf("second Reclassify: %v", err)
	}

	if n != 0 {
		t.Errorf("second Reclassify changed %d rows, want 0", n)
	}
}

// Raising a claim is the other half: a dependency that turns out to be reachable from something
// under review must stop being reported as work only its own project's CI does.
func TestReclassifyRaisesABuildTimeClaimToRuntime(t *testing.T) {
	q, _ := newQueue(t)
	if _, err := q.Enqueue([]Item{{
		NodeKey: "a", Owner: "octo-org", Repo: "a", Depth: 1,
		SupplyChain: SupplyChainBuild,
	}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if _, err := q.Reclassify(fixedChain{"a": true}); err != nil {
		t.Fatalf("Reclassify: %v", err)
	}

	if got := chainOf(t, q, "a"); got != SupplyChainRuntime {
		t.Errorf("supply_chain = %q, want %q", got, SupplyChainRuntime)
	}
}
