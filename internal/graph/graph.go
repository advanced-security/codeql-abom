// Package graph derives ABOM supply-chain aggregates with in-memory reachability.
//
// Only aggregate counts are stored; full dependent sets are produced on demand to avoid
// materializing every (node, dependent) pair.
package graph

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Builder computes graph aggregates over a store.
type Builder struct {
	Store *store.Store
	// MaxDepth bounds traversal. Zero means unlimited; cycles terminate regardless because
	// nodes are visited once.
	MaxDepth int
}

// inEdge is one reference into a node: what a declaring location said at the commits it said it
// at. A location whose commits declared different things is one inEdge per thing declared.
type inEdge struct {
	row int64
	src int64
	ref string
	// commits are the commits a walk may stand at that declare this. Empty string is a reference
	// recorded by no run, which binds to no commit.
	commits []string
}

// edgeLock is one commit a current lock selects for an edge, and whose lock: the edge's own
// repository for its workflows and composites, or a caller whose lock reaches the composite.
type edgeLock struct {
	commit   string
	consumer int64
	own      bool
}

type loadedNode struct {
	id        int64
	key       string
	kind      string
	repo      sql.NullInt64
	container sql.NullInt64
	external  bool
}

// model is the in-memory projection of the graph used for traversal.
type model struct {
	// reverse maps a node to the references made to it.
	reverse map[int64][]inEdge
	// container maps a node to the workflow or composite action it belongs to, which is what
	// other repositories can reference.
	container map[int64]int64
	repoOf    map[int64]int64
	kindOf    map[int64]string
	// keyOf names nodes, which exposure paths are reported in terms of.
	keyOf map[int64]string
	// isExternal marks nodes that are somebody else's dependency, so a walk can tell crossing
	// into a dependency from moving around inside one repository.
	isExternal map[int64]bool
	// resolved maps a dependency's node key and reference to the commit that reference resolved
	// to, keyed as key + NUL + ref. It is how a pinned version is turned into a commit.
	resolved map[string]string
	// seedRepo marks repositories under review. Upstream repositories still contribute edges,
	// because a composite action's own steps are part of what its callers depend on, but their
	// CI must not register as dependency pressure on the organization.
	seedRepo map[int64]bool
	// conditionOf holds the `if:` guarding a step, verbatim. Empty for the overwhelming majority
	// of steps, which are unconditional.
	conditionOf map[int64]string
	// locks holds, by edge row, what the current locks select for it. Empty for every edge of an
	// unlocked corpus.
	locks map[int64][]edgeLock
	// referenceable are the nodes that can appear as an edge target.
	referenceable []int64
}

func (b *Builder) load() (*model, error) {
	m := newModel()
	if err := m.loadSeedRepositories(b.Store); err != nil {
		return nil, err
	}
	if err := m.loadNodes(b.Store); err != nil {
		return nil, err
	}
	if err := m.loadEdges(b.Store); err != nil {
		return nil, err
	}
	if err := m.loadResolutions(b.Store); err != nil {
		return nil, err
	}
	if err := m.loadLocks(b.Store); err != nil {
		return nil, err
	}
	return m, m.loadConditions(b.Store)
}

func newModel() *model {
	return &model{
		reverse:     make(map[int64][]inEdge),
		container:   make(map[int64]int64),
		repoOf:      make(map[int64]int64),
		kindOf:      make(map[int64]string),
		keyOf:       make(map[int64]string),
		isExternal:  make(map[int64]bool),
		resolved:    make(map[string]string),
		seedRepo:    make(map[int64]bool),
		conditionOf: make(map[int64]string),
		locks:       make(map[int64][]edgeLock),
	}
}

func (m *model) loadSeedRepositories(s *store.Store) error {
	repos, err := s.DB().Query(
		`SELECT row_id FROM repository WHERE origin = ?`, store.OriginSeed)
	if err != nil {
		return fmt.Errorf("loading repositories: %w", err)
	}
	for repos.Next() {
		var id int64
		if err := repos.Scan(&id); err != nil {
			repos.Close()
			return fmt.Errorf("scanning repository: %w", err)
		}
		m.seedRepo[id] = true
	}
	repos.Close()
	return repos.Err()
}

func (m *model) loadNodes(s *store.Store) error {
	nodes, err := s.DB().Query(
		`SELECT row_id, node_key, node_kind, repository_row_id, container_node_row_id, is_external
		   FROM abom_node_live`)
	if err != nil {
		return fmt.Errorf("loading nodes: %w", err)
	}
	defer nodes.Close()

	for nodes.Next() {
		var n loadedNode
		if err := nodes.Scan(&n.id, &n.key, &n.kind, &n.repo, &n.container, &n.external); err != nil {
			return fmt.Errorf("scanning node: %w", err)
		}
		m.addNode(n)
	}
	return nodes.Err()
}

func (m *model) addNode(n loadedNode) {
	m.kindOf[n.id] = n.kind
	m.keyOf[n.id] = n.key
	if n.external {
		m.isExternal[n.id] = true
	}
	if n.repo.Valid {
		m.repoOf[n.id] = n.repo.Int64
	}
	if n.container.Valid {
		m.container[n.id] = n.container.Int64
	}
}

// loadEdges reads what each declaring location said, at the commits a walk may stand at.
func (m *model) loadEdges(s *store.Store) error {
	edges, err := s.EdgeDeclarations()
	if err != nil {
		return err
	}
	referenced := map[int64]bool{}
	for _, e := range edges {
		m.reverse[e.Dst] = append(m.reverse[e.Dst],
			inEdge{row: e.Row, src: e.Src, ref: e.Ref, commits: e.Commits})
		if !referenced[e.Dst] {
			referenced[e.Dst] = true
			m.referenceable = append(m.referenceable, e.Dst)
		}
	}
	return nil
}

// loadLocks records what the current locks select for each edge they govern, with whose lock.
func (m *model) loadLocks(s *store.Store) error {
	governed, err := s.LockGovernedEdges()
	if err != nil || len(governed) == 0 {
		return err
	}
	rows, err := s.DB().Query(`SELECT row_id, LOWER(repository_full_name) FROM repository`)
	if err != nil {
		return fmt.Errorf("loading repositories: %w", err)
	}
	repositories := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return fmt.Errorf("scanning a repository: %w", err)
		}
		repositories[name] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, g := range governed {
		consumer, ok := repositories[strings.ToLower(g.Consumer)]
		if !ok {
			continue
		}
		m.locks[g.EdgeID] = append(m.locks[g.EdgeID], edgeLock{
			commit: strings.ToLower(g.Commit), consumer: consumer,
			own: g.Rule != store.LockRuleCaller,
		})
	}
	return nil
}

// loadResolutions records which commit each analyzed reference of a dependency resolved to, so a
// traversal can tell one version of that dependency from another.
//
// Only what was measured: a reference with no recorded commit constrains nothing, because an
// unanalyzed version is something ABOM has no reading of rather than a version with no children.
func (m *model) loadResolutions(s *store.Store) error {
	rows, err := s.DB().Query(`
		SELECT node_key, COALESCE(ref, ''), resolved_sha
		  FROM abom_frontier
		 WHERE COALESCE(resolved_sha, '') <> ''`)
	if err != nil {
		return fmt.Errorf("loading reference resolutions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var key, ref, sha string
		if err := rows.Scan(&key, &ref, &sha); err != nil {
			return fmt.Errorf("scanning reference resolution: %w", err)
		}
		m.resolved[key+"\x00"+ref] = strings.ToLower(sha)
	}
	return rows.Err()
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
	// Repositories maps each dependent repository to the fewest hops in which it reaches the node.
	// One means it names the dependency itself; more means it inherits it from something it uses,
	// and the two are different remediation work.
	Repositories map[int64]int
	Workflows    map[int64]bool
	Direct       int
	MaxDepth     int
}

// reachOf walks dependents breadth-first from target.
//
// Each hop moves from a node to the sources that reference it, then up to those sources'
// containers, because depending on a container means depending on everything it uses.
func (m *model) reachOf(target int64, maxDepth int) Reach {
	r := Reach{Repositories: map[int64]int{}, Workflows: map[int64]bool{}}

	visited := map[int64]bool{target: true}
	frontier := []int64{target}
	// A location declaring this at two releases is still one direct reference.
	direct := map[int64]bool{}

	for depth := 1; len(frontier) > 0; depth++ {
		if maxDepth > 0 && depth > maxDepth {
			break
		}
		var next []int64
		for _, node := range frontier {
			for _, in := range m.reverse[node] {
				src := in.src
				if depth == 1 && !direct[in.row] {
					direct[in.row] = true
					r.Direct++
				}
				if visited[src] {
					continue
				}
				visited[src] = true

				if repo, ok := m.repoOf[src]; ok && m.seedRepo[repo] {
					// Breadth-first, so the first sighting is the fewest hops.
					if _, seen := r.Repositories[repo]; !seen {
						r.Repositories[repo] = depth
					}
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
	if _, err := tx.Exec(`DELETE FROM abom_node_reach`); err != nil {
		return fmt.Errorf("clearing the reach breakdown: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO abom_blast_radius
			(node_row_id, dependent_repos, dependent_workflows, direct_dependents, max_depth)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing insert: %w", err)
	}
	defer stmt.Close()

	// The walk already knows which repositories it found, so the breakdown costs one insert per
	// pair rather than a second traversal.
	pair, err := tx.Prepare(
		`INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing the reach insert: %w", err)
	}
	defer pair.Close()

	for _, target := range m.referenceable {
		r := m.reachOf(target, b.MaxDepth)
		if _, err := stmt.Exec(target, len(r.Repositories), len(r.Workflows),
			r.Direct, r.MaxDepth); err != nil {
			return fmt.Errorf("writing blast radius for node %d: %w", target, err)
		}
		for repo, hops := range r.Repositories {
			if _, err := pair.Exec(target, repo, hops); err != nil {
				return fmt.Errorf("writing reach for node %d: %w", target, err)
			}
		}
	}
	return tx.Commit()
}

// Dependents returns the repositories that would be affected by a change to a node.
//
// This is the on-demand counterpart to the stored counts, used to answer "which repositories
// does a version bump touch" from the graph in memory, without reading the stored breakdown.
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
		if _, reaches := reach.Repositories[id]; reaches {
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
	// From nodes alone, so it owes nothing to the walks below and they read none of it.
	if _, err := b.Store.DeriveContainment(); err != nil {
		return err
	}
	if err := b.BuildBlastRadius(); err != nil {
		return err
	}
	if err := b.BuildExposure(); err != nil {
		return err
	}
	// Last, because the advisory fact reads the routes exposure just stored. Deriving it before
	// would leave every edge naming the previous cycle's exposure.
	_, err := b.Store.DeriveEdgeFacts()
	return err
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
