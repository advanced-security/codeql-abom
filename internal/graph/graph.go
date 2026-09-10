// Package graph derives the aggregates that answer supply-chain questions about the ABOM.
//
// Reachability is computed in memory rather than with a recursive SQL query. The edge list is
// small enough to hold (millions of rows, two integers each) and a single breadth-first pass
// per referenceable node is far cheaper than repeatedly re-walking the graph in SQLite.
//
// Only aggregate counts are stored. Materializing every (node, dependent) pair is quadratic in
// the worst case, so the full dependent set is produced on demand by Dependents instead.
package graph

import (
	"database/sql"
	"fmt"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Builder computes graph aggregates over a store.
type Builder struct {
	Store *store.Store
	// MaxDepth bounds traversal. Zero means unlimited; cycles terminate regardless because
	// nodes are visited once.
	MaxDepth int
}

// model is the in-memory projection of the graph used for traversal.
type model struct {
	// reverse maps a node to the source nodes that depend on it.
	reverse map[int64][]int64
	// container maps a node to the workflow or composite action it belongs to, which is what
	// other repositories can reference.
	container map[int64]int64
	repoOf    map[int64]int64
	kindOf    map[int64]string
	// keyOf names nodes, which exposure paths are reported in terms of.
	keyOf map[int64]string
	// seedRepo marks repositories under review. Upstream repositories still contribute edges,
	// because a composite action's own steps are part of what its callers depend on, but their
	// CI must not register as dependency pressure on the organization.
	seedRepo map[int64]bool
	// conditionOf holds the `if:` guarding a step, verbatim. Empty for the overwhelming majority
	// of steps, which are unconditional.
	conditionOf map[int64]string
	// referenceable are the nodes that can appear as an edge target.
	referenceable []int64
}

func (b *Builder) load() (*model, error) {
	m := &model{
		reverse:     make(map[int64][]int64),
		container:   make(map[int64]int64),
		repoOf:      make(map[int64]int64),
		kindOf:      make(map[int64]string),
		keyOf:       make(map[int64]string),
		seedRepo:    make(map[int64]bool),
		conditionOf: make(map[int64]string),
	}

	repos, err := b.Store.DB().Query(
		`SELECT row_id FROM repository WHERE origin = ?`, store.OriginSeed)
	if err != nil {
		return nil, fmt.Errorf("loading repositories: %w", err)
	}
	for repos.Next() {
		var id int64
		if err := repos.Scan(&id); err != nil {
			repos.Close()
			return nil, fmt.Errorf("scanning repository: %w", err)
		}
		m.seedRepo[id] = true
	}
	repos.Close()
	if err := repos.Err(); err != nil {
		return nil, err
	}

	nodes, err := b.Store.DB().Query(
		`SELECT row_id, node_key, node_kind, repository_row_id, container_node_row_id FROM abom_node_live`)
	if err != nil {
		return nil, fmt.Errorf("loading nodes: %w", err)
	}
	defer nodes.Close()

	for nodes.Next() {
		var id int64
		var key, kind string
		var repo, container sql.NullInt64
		if err := nodes.Scan(&id, &key, &kind, &repo, &container); err != nil {
			return nil, fmt.Errorf("scanning node: %w", err)
		}
		m.kindOf[id] = kind
		m.keyOf[id] = key
		if repo.Valid {
			m.repoOf[id] = repo.Int64
		}
		if container.Valid {
			m.container[id] = container.Int64
		}
	}
	if err := nodes.Err(); err != nil {
		return nil, err
	}

	edges, err := b.Store.DB().Query(`SELECT e.src_node_row_id, e.dst_node_row_id
		FROM abom_edge_live e WHERE ` + store.DependencyEdgeFilter)
	if err != nil {
		return nil, fmt.Errorf("loading edges: %w", err)
	}
	defer edges.Close()

	seenTarget := make(map[int64]bool)
	for edges.Next() {
		var src, dst int64
		if err := edges.Scan(&src, &dst); err != nil {
			return nil, fmt.Errorf("scanning edge: %w", err)
		}
		m.reverse[dst] = append(m.reverse[dst], src)
		if !seenTarget[dst] {
			seenTarget[dst] = true
			m.referenceable = append(m.referenceable, dst)
		}
	}
	if err := edges.Err(); err != nil {
		return nil, err
	}
	return m, m.loadConditions(b.Store)
}

// loadConditions attaches each step's `if:` to the traversal, so a route can say what stands
// between an entry point and the step at the end of it.
//
// Ordered by row id so the most recent statement about a step wins deterministically; several
// runs of the same query against different commits may each have said something.
func (m *model) loadConditions(s *store.Store) error {
	rows, err := s.DB().Query(store.ConditionObservationsQuery,
		store.SubjectNode, store.ObservationCondition)
	if err != nil {
		return fmt.Errorf("loading step conditions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var condition string
		if err := rows.Scan(&id, &condition); err != nil {
			return fmt.Errorf("scanning step condition: %w", err)
		}
		if condition != "" {
			m.conditionOf[id] = condition
		}
	}
	return rows.Err()
}

// Reach is the set of things that transitively depend on a node.
type Reach struct {
	Repositories map[int64]bool
	Workflows    map[int64]bool
	Direct       int
	MaxDepth     int
}

// reachOf walks dependents breadth-first from target.
//
// Each hop moves from a node to the sources that reference it, then up to those sources'
// containers, because depending on a container means depending on everything it uses.
func (m *model) reachOf(target int64, maxDepth int) Reach {
	r := Reach{Repositories: map[int64]bool{}, Workflows: map[int64]bool{}}

	visited := map[int64]bool{target: true}
	frontier := []int64{target}

	for depth := 1; len(frontier) > 0; depth++ {
		if maxDepth > 0 && depth > maxDepth {
			break
		}
		var next []int64
		for _, node := range frontier {
			for _, src := range m.reverse[node] {
				if depth == 1 {
					r.Direct++
				}
				if visited[src] {
					continue
				}
				visited[src] = true

				if repo, ok := m.repoOf[src]; ok && m.seedRepo[repo] {
					r.Repositories[repo] = true
					switch m.kindOf[src] {
					case "workflow", "composite_action":
						r.Workflows[src] = true
					}
				}
				if c, ok := m.container[src]; ok {
					if repo, ok := m.repoOf[c]; ok && m.seedRepo[repo] {
						r.Workflows[c] = true
					}
					if !visited[c] {
						next = append(next, c)
					}
				}
			}
			r.MaxDepth = depth
		}
		frontier = next
	}
	return r
}

// BuildBlastRadius recomputes aggregate reach for every referenceable node.
func (b *Builder) BuildBlastRadius() error {
	m, err := b.load()
	if err != nil {
		return err
	}

	tx, err := b.Store.DB().Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_blast_radius`); err != nil {
		return fmt.Errorf("clearing blast radius: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing insert: %w", err)
	}
	defer stmt.Close()

	for _, target := range m.referenceable {
		r := m.reachOf(target, b.MaxDepth)
		if _, err := stmt.Exec(target, len(r.Repositories), len(r.Workflows),
			r.Direct, r.MaxDepth); err != nil {
			return fmt.Errorf("writing blast radius for node %d: %w", target, err)
		}
	}
	return tx.Commit()
}

// Dependents returns the repositories that would be affected by a change to a node.
//
// This is the on-demand counterpart to the stored counts, used to answer "which repositories
// does a version bump touch" without materializing every pair up front.
func (b *Builder) Dependents(nodeKey string) ([]string, error) {
	var target int64
	err := b.Store.DB().QueryRow(
		`SELECT row_id FROM abom_node_live WHERE node_key = ?`, nodeKey).Scan(&target)
	if err != nil {
		return nil, fmt.Errorf("resolving %q: %w", nodeKey, err)
	}

	m, err := b.load()
	if err != nil {
		return nil, err
	}
	reach := m.reachOf(target, b.MaxDepth)
	if len(reach.Repositories) == 0 {
		return nil, nil
	}

	rows, err := b.Store.DB().Query(
		`SELECT row_id, repository_full_name FROM repository ORDER BY repository_full_name`)
	if err != nil {
		return nil, fmt.Errorf("loading repositories: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		if reach.Repositories[id] {
			out = append(out, name)
		}
	}
	return out, rows.Err()
}

// RebuildAggregates recomputes the passes that clear their table and derive it again. Their
// cost tracks corpus size rather than how much changed, so callers schedule them deliberately.
func (b *Builder) RebuildAggregates() error {
	// Classifying first: it only ever fills edges that have none, so it costs nothing once the
	// graph and the classification agree, and the aggregates below can read it.
	if _, err := b.Store.ClassifyEdgeBoundaries(); err != nil {
		return err
	}
	// Same shape: the queue already resolved these tags, and recording them is what lets a
	// commit-pinned reference be named as the version GitHub can match an advisory against.
	if _, err := b.Store.BackfillRefResolutions(); err != nil {
		return err
	}
	if err := b.BuildBlastRadius(); err != nil {
		return err
	}
	return b.BuildExposure()
}

// BuildAll finishes a run: findings become properties of nodes, then every aggregate is derived
// again. A finding that is never attributed, or an aggregate never rebuilt, is data the graph
// holds and no view can reach.
func (b *Builder) BuildAll() error {
	if _, err := b.Store.LinkFindingsToNodes(); err != nil {
		return err
	}
	return b.RebuildAggregates()
}
