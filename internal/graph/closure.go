package graph

import (
	"fmt"
	"sort"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Closure is every repository reachable from a declared account by dependency edges.
//
// This is what bounds discovery. A dependency of something you own is your supply chain however
// deep it sits; a dependency of a repository nothing owned reaches is somebody else's, and
// following it is how a corpus of 56,830 nodes grows from 204 repositories nobody owns.
type Closure struct {
	// byRepository holds every repository admitted. Absence means "not reached", which is a real
	// answer rather than a missing one: the walk visits the whole graph.
	byRepository map[string]bool
	roots        int
}

// Contains reports whether a repository is inside the owned closure.
func (c *Closure) Contains(fullName string) bool {
	if c == nil {
		return true
	}
	return c.byRepository[fullName]
}

// Roots is how many declared repositories the walk started from. Zero means nothing is declared
// or nothing declared is in the graph, and a caller drawing a conclusion from an empty closure
// needs to be able to tell those from a corpus that genuinely reaches nothing.
func (c *Closure) Roots() int {
	if c == nil {
		return 0
	}
	return c.roots
}

// Size is how many repositories are admitted.
func (c *Closure) Size() int {
	if c == nil {
		return 0
	}
	return len(c.byRepository)
}

// Repositories is every repository admitted, sorted so two closures can be diffed.
func (c *Closure) Repositories() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.byRepository))
	for name := range c.byRepository {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// OwnedClosure walks outward from the declared accounts.
//
// Repository-granular rather than node-granular, because the frontier queues repositories and a
// boundary expressed in a different unit from the thing it bounds would need a translation nobody
// could check. `ClassifyDependencies` answers a neighboring question — whether anything under
// review *executes* a dependency — over containers; this one answers whether it is reachable at
// all.
func (b *Builder) OwnedClosure() (*Closure, error) {
	roots, err := b.Store.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	return b.ClosureFrom(roots)
}

// KeepList names the repositories the corpus holds that the declared scope reaches, and the
// closure it derived them from.
//
// Written once because the command and the server both prune, and two answers to "what survives"
// would be two different corpora depending on which door was used.
func (b *Builder) KeepList() ([]string, *Closure, error) {
	closure, err := b.OwnedClosure()
	if err != nil {
		return nil, nil, err
	}
	all, err := b.Store.AllRepositories()
	if err != nil {
		return nil, nil, err
	}
	keep := make([]string, 0, len(all))
	for _, name := range all {
		if closure.Contains(name) {
			keep = append(keep, name)
		}
	}
	return keep, closure, nil
}

// ClosureFrom walks outward from an explicit root set, so a declaration that has not been stored
// can be measured before anyone commits to it.
func (b *Builder) ClosureFrom(roots []string) (*Closure, error) {
	out := &Closure{byRepository: map[string]bool{}, roots: len(roots)}

	// Edges by containing repository to the repository each target belongs to. The target's
	// owner and repo come from the node rather than its repository_row_id, which a node first
	// seen as somebody else's dependency does not have.
	rows, err := b.Store.DB().Query(`
		SELECT r.repository_full_name, dst.owner || '/' || dst.repo
		FROM abom_edge_live e
		JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
		JOIN repository r ON r.row_id = e.repository_row_id
		WHERE ` + store.DependencyEdgeFilter + `
		  AND dst.owner IS NOT NULL AND dst.repo IS NOT NULL
		  AND dst.owner <> '' AND dst.repo <> ''
		GROUP BY r.repository_full_name, dst.owner || '/' || dst.repo`)
	if err != nil {
		return nil, fmt.Errorf("loading dependency edges: %w", err)
	}
	defer rows.Close()

	dependsOn := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scanning a dependency edge: %w", err)
		}
		dependsOn[from] = append(dependsOn[from], to)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("loading dependency edges: %w", err)
	}

	queue := make([]string, 0, len(roots))
	for _, r := range roots {
		if !out.byRepository[r] {
			out.byRepository[r] = true
			queue = append(queue, r)
		}
	}
	for len(queue) > 0 {
		from := queue[0]
		queue = queue[1:]
		for _, to := range dependsOn[from] {
			if out.byRepository[to] {
				continue
			}
			out.byRepository[to] = true
			queue = append(queue, to)
		}
	}
	return out, nil
}
