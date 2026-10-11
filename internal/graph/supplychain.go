package graph

import (
	"github.com/advanced-security/codeql-abom/internal/store"
)

// SupplyChain is how each dependency in the graph comes to be part of it.
//
// Runtime means something under review executes the dependency; build-time means only an upstream
// project's own CI does.
type SupplyChain struct {
	// byKey holds runtime for every dependency the graph names. Absence is not build-time; see Of.
	byKey map[string]bool
}

// Of reports how a dependency is classified.
//
// known is false when nothing in the graph names the key; that is not the same as build-time.
func (c *SupplyChain) Of(nodeKey string) (runtime, known bool) {
	runtime, known = c.byKey[nodeKey]
	return runtime, known
}

// Runtime counts the dependencies something under review executes.
func (c *SupplyChain) Runtime() int {
	n := 0
	for _, runtime := range c.byKey {
		if runtime {
			n++
		}
	}
	return n
}

// Len counts every dependency the graph names.
func (c *SupplyChain) Len() int { return len(c.byKey) }

// ClassifyDependencies decides, for every dependency in the graph, whether anything under review
// executes it.
//
// The test is transitive reachability from seed repositories, not whether a container is
// referenced. Reachability stays in Go because the equivalent recursive SQL is too slow here.
func (b *Builder) ClassifyDependencies() (*SupplyChain, error) {
	// A container is a workflow or a composite action's `runs:` block; it is what a `uses:` can
	// name, and what the callee's own edges hang from once that repository is analyzed. The
	// repository comes from the edge rather than the node because a node upsert never revises
	// repository_row_id, so a container first seen as somebody else's dependency has none.
	calls, err := b.Store.DependencyCalls()
	if err != nil {
		return nil, err
	}
	calledBy := map[int64][]int64{}
	seeded := map[int64]bool{}
	for _, c := range calls {
		calledBy[c.Container] = append(calledBy[c.Container], c.Target)
		if c.Origin == store.OriginSeed {
			seeded[c.Container] = true
		}
	}

	reached := make(map[int64]bool, len(seeded))
	queue := make([]int64, 0, len(seeded))
	for id := range seeded {
		reached[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		container := queue[0]
		queue = queue[1:]
		for _, callee := range calledBy[container] {
			if reached[callee] {
				continue
			}
			reached[callee] = true
			queue = append(queue, callee)
		}
	}

	byKey := make(map[string]bool, len(calls))
	for _, c := range calls {
		byKey[c.Key] = byKey[c.Key] || reached[c.Container]
	}
	return &SupplyChain{byKey: byKey}, nil
}
