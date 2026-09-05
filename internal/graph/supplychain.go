package graph

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// SupplyChain is how each dependency in the graph comes to be part of it.
//
// Runtime means something under review executes the dependency: a composite action's own steps
// run inside whoever calls the action, and a reusable workflow's steps run for whoever invokes
// it. Build-time means it only ever runs in an upstream project's own CI. Both are followed —
// compromising a release pipeline is how consumers receive a malicious version — but they are
// different claims, and reporting build-time work as though this organization ran it would
// overstate the exposure.
type SupplyChain struct {
	// byKey holds runtime for every dependency the graph names. Absence is not build-time; see Of.
	byKey map[string]bool
}

// Of reports how a dependency is classified.
//
// known is false when nothing in the graph names the key at all, which is a different fact from
// build-time: an absent measurement is not a measurement of zero, and a caller that conflated
// them would demote whatever it had never looked at.
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

// ClassifyDependencies decides, for every dependency in the graph, whether anything under
// review executes it.
//
// The test is transitive reachability from the seed repositories, not "is this container
// referenced by something". A local test answers the base case backwards — a repository under
// review runs its own CI workflow, which nothing references — and carries a claim one hop past
// its evidence, because a caller that is itself build-time-only still counts as a reference.
//
// Reachability is computed here rather than with a recursive SQL query for the reason the rest
// of this package gives: the same closure in SQLite took 36 seconds on the reference corpus.
func (b *Builder) ClassifyDependencies() (*SupplyChain, error) {
	// A container is a workflow or a composite action's `runs:` block; it is what a `uses:` can
	// name, and what the callee's own edges hang from once that repository is analyzed. The
	// repository comes from the edge rather than the node because a node upsert never revises
	// repository_row_id, so a container first seen as somebody else's dependency has none.
	rows, err := b.Store.DB().Query(`
		SELECT COALESCE(src.container_node_row_id, src.row_id), e.dst_node_row_id,
		       dst.node_key, r.origin
		FROM abom_edge e
		JOIN abom_node src ON src.row_id = e.src_node_row_id
		JOIN abom_node dst ON dst.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id`)
	if err != nil {
		return nil, fmt.Errorf("loading dependency calls: %w", err)
	}
	defer rows.Close()

	type call struct {
		container int64
		dst       int64
		key       string
	}
	var calls []call
	calledBy := map[int64][]int64{}
	seeded := map[int64]bool{}
	for rows.Next() {
		var c call
		var origin string
		if err := rows.Scan(&c.container, &c.dst, &c.key, &origin); err != nil {
			return nil, fmt.Errorf("scanning dependency call: %w", err)
		}
		calls = append(calls, c)
		calledBy[c.container] = append(calledBy[c.container], c.dst)
		if origin == store.OriginSeed {
			seeded[c.container] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading dependency calls: %w", err)
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
		byKey[c.key] = byKey[c.key] || reached[c.container]
	}
	return &SupplyChain{byKey: byKey}, nil
}
