package frontier

import "fmt"

// Classification answers how a dependency comes to be part of the supply chain.
//
// An interface so the queue does not depend on the traversal that produces the answer: what
// makes a claim sound is that it was derived from the whole graph at once, not where it came
// from.
type Classification interface {
	// Of reports whether something under review executes the dependency, and whether the graph
	// names it at all.
	Of(nodeKey string) (runtime, known bool)
}

// Reclassify rewrites how queued dependencies are classified, and is the only pass allowed to
// lower a claim.
//
// Enqueue may only ever strengthen one, so that the order repositories happen to be analyzed in
// cannot decide how a dependency is reported. The cost is that a rule admitting fewer runtime
// dependencies can never take effect through it, and on a drained queue nothing re-enqueues
// anyway. Lowering is sound here for the same reason it is unsound there: the classification is
// computed over the entire graph at once, so it cannot depend on arrival order.
//
// A key the classification does not know is left alone. An absent measurement is not a
// measurement of zero, and demoting an unmeasured row would assert something no query said.
func (q *Queue) Reclassify(c Classification) (int, error) {
	rows, err := q.Store.DB().Query(`SELECT node_key, supply_chain FROM abom_frontier`)
	if err != nil {
		return 0, fmt.Errorf("reading the queue to reclassify it: %w", err)
	}
	defer rows.Close()

	type change struct{ key, chain string }
	var changes []change
	for rows.Next() {
		var key, current string
		if err := rows.Scan(&key, &current); err != nil {
			return 0, fmt.Errorf("scanning a queued dependency: %w", err)
		}
		runtime, known := c.Of(key)
		if !known {
			continue
		}
		want := SupplyChainBuild
		if runtime {
			want = SupplyChainRuntime
		}
		if want != current {
			changes = append(changes, change{key, want})
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reading the queue to reclassify it: %w", err)
	}
	if len(changes) == 0 {
		return 0, nil
	}

	tx, err := q.Store.DB().Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	update, err := tx.Prepare(`UPDATE abom_frontier SET supply_chain = ? WHERE node_key = ?`)
	if err != nil {
		return 0, fmt.Errorf("preparing reclassification: %w", err)
	}
	defer update.Close()

	for _, ch := range changes {
		if _, err := update.Exec(ch.chain, ch.key); err != nil {
			return 0, fmt.Errorf("reclassifying %q: %w", ch.key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(changes), nil
}
