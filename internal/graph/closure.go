package graph

import (
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

// OwnedClosure walks outward from the declared accounts at repository granularity.
func (b *Builder) OwnedClosure() (*Closure, error) {
	roots, err := b.Store.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	return b.ClosureFrom(roots)
}

// OwnedClosureNames is the owned closure as the discovery queue reads it, walked afresh on every
// call so a declaration or an analysis moves the bound at once.
func OwnedClosureNames(s store.Backend) func() ([]string, error) {
	return func() ([]string, error) {
		c, err := (&Builder{Store: s}).OwnedClosure()
		if err != nil {
			return nil, err
		}
		return c.Repositories(), nil
	}
}

// KeepList names corpus repositories the declared scope reaches, and the closure behind them.
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
	rows, err := b.Store.RepositoryDependencies()
	if err != nil {
		return nil, err
	}

	dependsOn := map[string][]string{}
	for _, d := range rows {
		dependsOn[d.From] = append(dependsOn[d.From], d.To)
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
