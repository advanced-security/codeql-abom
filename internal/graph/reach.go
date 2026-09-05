package graph

import (
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// reachStore is what ReachOfNode needs, named so the test can be explicit about it.
type reachStore = *store.Store

// NodeReach is one repository under review that reaches a node, and how.
type NodeReach struct {
	Repository string `json:"repository"`
	// Hops is 0 when the node lives in this repository, 1 when the repository names its
	// container directly, and more when it arrives through intermediaries someone else owns.
	Hops int      `json:"hops"`
	Path []string `json:"path"`
}

// ReachOfNode reports which repositories under review reach a node, and by what route.
//
// This is the question a package-version match cannot answer. Depending on an action says the
// name appears somewhere; reaching a step says something the organization actually runs arrives
// at it, and through whom — which decides whether the fix is theirs to make or someone else's.
//
// Nothing here judges whether arriving is dangerous. Reach is a route, not a verdict.
func (b *Builder) ReachOfNode(nodeKey string, maxDepth int) ([]NodeReach, error) {
	m, err := b.load()
	if err != nil {
		return nil, err
	}
	if maxDepth == 0 {
		maxDepth = b.MaxDepth
	}

	var target int64
	for id, key := range m.keyOf {
		if key == nodeKey {
			target = id
			break
		}
	}
	if target == 0 {
		// Not an error: asking about something the graph has never seen is a fair question
		// whose answer is that nothing reaches it.
		return nil, nil
	}

	// A step is not referenced by anything; it runs because its container was invoked. So the
	// reachable unit is the container, and the step inherits its callers.
	unit := target
	if c, ok := m.container[target]; ok {
		unit = c
	}

	out := make([]NodeReach, 0, 4)
	// The repository the node lives in runs it without depending on anything.
	if repo, ok := m.repoOf[target]; ok && m.seedRepo[repo] {
		name, err := b.repositoryName(repo)
		if err != nil {
			return nil, err
		}
		out = append(out, NodeReach{Repository: name, Hops: 0, Path: []string{m.keyOf[target]}})
	}

	for repo, e := range m.exposureOf(unit, maxDepth) {
		name, err := b.repositoryName(repo)
		if err != nil {
			return nil, err
		}
		if name == "" {
			continue
		}
		out = append(out, NodeReach{Repository: name, Hops: e.hops, Path: e.path})
	}
	return out, nil
}

func (b *Builder) repositoryName(id int64) (string, error) {
	var name string
	if err := b.Store.DB().QueryRow(
		`SELECT repository_full_name FROM repository WHERE row_id = ?`, id).Scan(&name); err != nil {
		return "", fmt.Errorf("naming repository %d: %w", id, err)
	}
	return name, nil
}
