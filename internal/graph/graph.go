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
	job       string
}

// model is the in-memory projection of the graph used for traversal.
type model struct {
	// reverse maps a node to the references made to it.
	reverse map[int64][]inEdge
	// container maps a node to the workflow or composite action it belongs to, which is what
	// other repositories can reference.
	container map[int64]int64
	// jobOf names the job a step or job node belongs to, as its workflow and job id: two
	// workflows' `build` jobs are two jobs. Absent for a composite action's steps, which have none.
	jobOf  map[int64]string
	repoOf map[int64]int64
	kindOf map[int64]string
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
		jobOf:       make(map[int64]string),
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
		`SELECT row_id, node_key, node_kind, repository_row_id, container_node_row_id, is_external,
		        COALESCE(job_id, '')
		   FROM abom_node_live`)
	if err != nil {
		return fmt.Errorf("loading nodes: %w", err)
	}
	defer nodes.Close()

	for nodes.Next() {
		var n loadedNode
		if err := nodes.Scan(&n.id, &n.key, &n.kind, &n.repo, &n.container, &n.external, &n.job); err != nil {
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
		if n.job != "" {
			m.jobOf[n.id] = fmt.Sprintf("%d/%s", n.container.Int64, n.job)
		}
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
	// Versions breaks Repositories down by the commit of the node each route runs, "" where that
	// is not known or the node has no version of its own. A repository running two versions is in
	// both, and Repositories is their union.
	Versions map[string]map[int64]int
	// References are those starting points run the node by: the rows of its versions table, so the
	// inventory's count and that table cannot disagree.
	References map[string]bool
	// Dependents are, by repository, its steps, jobs and workflow files that run the node.
	Dependents map[int64]*Dependents
	MaxDepth   int
}

// Dependents are one repository's steps, jobs and workflow files that run a node, directly or
// through another action.
type Dependents struct {
	Steps     map[int64]bool
	Jobs      map[string]bool
	Workflows map[int64]bool
}

// note counts the step or job making a reference, and the job and workflow file holding it. A
// composite action's own steps count as steps; the action file holding them is not a workflow.
func (d *Dependents) note(m *model, src int64) {
	if m.kindOf[src] == "step" {
		d.Steps[src] = true
	}
	if job, ok := m.jobOf[src]; ok {
		d.Jobs[job] = true
	}
	switch file := m.container[src]; m.kindOf[file] {
	case "workflow", "reusable_workflow":
		d.Workflows[file] = true
	}
}

// reachOf counts what the dependents walk reaches from target, by version.
func (m *model) reachOf(target int64, maxDepth int) Reach {
	r := Reach{Repositories: map[int64]int{}, Versions: map[string]map[int64]int{},
		References: map[string]bool{}, Dependents: map[int64]*Dependents{}}

	r.MaxDepth = m.walk(target, maxDepth, func(s step) {
		repo, ok := m.countedFor(s)
		if !ok {
			return
		}
		if inUse(s.depth, s.at.entry) {
			r.References[s.at.entry] = true
		}
		// Breadth-first, so the first sighting is the fewest hops.
		if _, seen := r.Repositories[repo]; !seen {
			r.Repositories[repo] = s.depth
		}
		if r.Versions[s.at.version] == nil {
			r.Versions[s.at.version] = map[int64]int{}
		}
		if _, seen := r.Versions[s.at.version][repo]; !seen {
			r.Versions[s.at.version][repo] = s.depth
		}
		d := r.Dependents[repo]
		if d == nil {
			d = &Dependents{Steps: map[int64]bool{}, Jobs: map[string]bool{}, Workflows: map[int64]bool{}}
			r.Dependents[repo] = d
		}
		d.note(m, s.in.src)
	})
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

	// One walk writes all three, so they are cleared together. Names are constants, never input.
	for _, table := range []string{"abom_blast_radius", "abom_node_reach", "abom_node_version_reach"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("clearing %s: %w", table, err)
		}
	}
	w, err := prepareReachWriter(tx)
	if err != nil {
		return err
	}
	defer w.close()

	for _, target := range m.referenceable {
		if err := w.write(target, m.reachOf(target, b.MaxDepth)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// reachWriter writes one node's reach. The walk already knows which repositories it found and at
// which version, so each breakdown costs one insert per row rather than a second traversal.
type reachWriter struct {
	rollup, repositories, versions *sql.Stmt
}

func prepareReachWriter(tx *sql.Tx) (*reachWriter, error) {
	w := &reachWriter{}
	var err error
	if w.rollup, err = tx.Prepare(`
		INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_steps, dependent_jobs,
			dependent_workflows, max_depth, refs_in_use)
		VALUES (?, ?, ?, ?, ?, ?, ?)`); err != nil {
		return nil, fmt.Errorf("preparing the blast radius insert: %w", err)
	}
	if w.repositories, err = tx.Prepare(
		`INSERT INTO abom_node_reach (node_row_id, repository_row_id, hops, steps, jobs, workflows)
		 VALUES (?, ?, ?, ?, ?, ?)`); err != nil {
		w.close()
		return nil, fmt.Errorf("preparing the reach insert: %w", err)
	}
	if w.versions, err = tx.Prepare(`INSERT INTO abom_node_version_reach
		(node_row_id, commit_sha, repository_row_id, hops) VALUES (?, ?, ?, ?)`); err != nil {
		w.close()
		return nil, fmt.Errorf("preparing the reach-by-version insert: %w", err)
	}
	return w, nil
}

func (w *reachWriter) close() {
	for _, stmt := range []*sql.Stmt{w.rollup, w.repositories, w.versions} {
		if stmt != nil {
			stmt.Close()
		}
	}
}

// write stores one node's reach. Each repository's members are its own, so the totals are the sums.
func (w *reachWriter) write(target int64, r Reach) error {
	var steps, jobs, workflows int
	for repo, hops := range r.Repositories {
		d := r.Dependents[repo]
		steps, jobs, workflows = steps+len(d.Steps), jobs+len(d.Jobs), workflows+len(d.Workflows)
		if _, err := w.repositories.Exec(target, repo, hops,
			len(d.Steps), len(d.Jobs), len(d.Workflows)); err != nil {
			return fmt.Errorf("writing reach for node %d: %w", target, err)
		}
	}
	if _, err := w.rollup.Exec(target, len(r.Repositories), steps, jobs, workflows,
		r.MaxDepth, len(r.References)); err != nil {
		return fmt.Errorf("writing blast radius for node %d: %w", target, err)
	}
	for commit, repos := range r.Versions {
		for repo, hops := range repos {
			if _, err := w.versions.Exec(target, commit, repo, hops); err != nil {
				return fmt.Errorf("writing reach for node %d at %q: %w", target, commit, err)
			}
		}
	}
	return nil
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
