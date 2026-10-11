// Package graph derives ABOM supply-chain aggregates with in-memory reachability.
//
// Only aggregate counts are stored; full dependent sets are produced on demand to avoid
// materializing every (node, dependent) pair.
package graph

import (
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Builder computes graph aggregates over a store.
type Builder struct {
	Store store.Backend
	// MaxDepth bounds traversal. Zero means unlimited; cycles terminate regardless because
	// nodes are visited once.
	MaxDepth int
	// Models lets the uses and versions walks share one loaded graph across builders. Nil loads
	// it on every call.
	Models *Models
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
	// live is the commits the walk may stand at, per repository, as store.LiveCommits says.
	live map[string]map[string][]string
	// starts holds, by repository under review, the commits at which its own declarations are a
	// starting point's, as store.StartingCommits says. Absent where no reference resolves it.
	starts map[int64]map[string]bool
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
		starts:      make(map[int64]map[string]bool),
		conditionOf: make(map[int64]string),
		locks:       make(map[int64][]edgeLock),
	}
}

func (m *model) loadSeedRepositories(s store.Backend) error {
	repos, err := s.SeedRepositoryIDs()
	if err != nil {
		return err
	}
	for _, id := range repos {
		m.seedRepo[id] = true
	}
	return nil
}

func (m *model) loadNodes(s store.Backend) error {
	nodes, err := s.TraversalNodes()
	if err != nil {
		return err
	}
	for _, n := range nodes {
		m.addNode(n)
	}
	return nil
}

func (m *model) addNode(n store.TraversalNode) {
	m.kindOf[n.ID] = n.Kind
	m.keyOf[n.ID] = n.Key
	if n.External {
		m.isExternal[n.ID] = true
	}
	if n.RepositoryID != nil {
		m.repoOf[n.ID] = *n.RepositoryID
	}
	if n.ContainerID != nil {
		m.container[n.ID] = *n.ContainerID
		if n.Job != "" {
			m.jobOf[n.ID] = fmt.Sprintf("%d/%s", *n.ContainerID, n.Job)
		}
	}
}

// loadEdges reads what each declaring location said, at the commits a walk may stand at.
func (m *model) loadEdges(s store.Backend) error {
	live, err := s.LiveCommits()
	if err != nil {
		return err
	}
	m.live = live
	starting, err := s.StartingCommits(live)
	if err != nil {
		return err
	}
	if err := m.loadStarts(s, starting); err != nil {
		return err
	}
	edges, err := s.EdgeDeclarations(live, starting)
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

// loadStarts records, for each repository under review, the commits it stands at as a starting
// point: starting, as store.StartingCommits says.
func (m *model) loadStarts(s store.Backend, starting map[string]map[string][]string) error {
	rows, err := s.TraversalRepositories(true)
	if err != nil {
		return err
	}
	for _, r := range rows {
		commits, ok := starting[r.Name]
		if !ok {
			continue
		}
		m.starts[r.ID] = make(map[string]bool, len(commits))
		for commit := range commits {
			m.starts[r.ID][commit] = true
		}
	}
	return nil
}

// loadLocks records what the current locks select for each edge they govern, with whose lock.
func (m *model) loadLocks(s store.Backend) error {
	governed, err := s.LockGovernedEdges()
	if err != nil || len(governed) == 0 {
		return err
	}
	rows, err := s.TraversalRepositories(false)
	if err != nil {
		return err
	}
	repositories := map[string]int64{}
	for _, r := range rows {
		repositories[r.Name] = r.ID
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
func (m *model) loadResolutions(s store.Backend) error {
	rows, err := s.FrontierResolutions()
	if err != nil {
		return err
	}
	for _, r := range rows {
		m.resolved[r.Key+"\x00"+r.Ref] = strings.ToLower(r.SHA)
	}
	return nil
}

// loadConditions attaches each step's `if:` to the traversal, so a route can say what stands
// between an entry point and the step at the end of it.
//
// Ordered by row id so the most recent statement about a step wins deterministically; several
// runs of the same query against different commits may each have said something.
func (m *model) loadConditions(s store.Backend) error {
	rows, err := s.StepConditions()
	if err != nil {
		return err
	}
	for _, c := range rows {
		if c.Condition != "" {
			m.conditionOf[c.ID] = c.Condition
		}
	}
	return nil
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
	_, err = b.writeBlastRadius(m)
	return err
}

// writeBlastRadius rewrites all reach from m, unmarked until exposure and its edge fact agree.
func (b *Builder) writeBlastRadius(m *model) (Derivation, error) {
	var d Derivation
	tx, err := b.Store.BeginDerivation()
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.ClearReachMark(); err != nil {
		return d, err
	}
	n, err := tx.ClearReach()
	if err != nil {
		return d, err
	}
	d.Rows += n
	if err := tx.PrepareReach(); err != nil {
		return d, err
	}

	for _, target := range m.referenceable {
		n, err := tx.WriteReach(target, storedReach(m.reachOf(target, b.MaxDepth)))
		if err != nil {
			return d, err
		}
		d.Walked, d.Rows = d.Walked+1, d.Rows+n
	}
	return d, tx.Commit()
}

func storedReach(r Reach) store.ReachWrite {
	out := store.ReachWrite{Repositories: map[int64]store.ReachCounts{},
		Versions: r.Versions, MaxDepth: r.MaxDepth, RefsInUse: len(r.References)}
	for repo, hops := range r.Repositories {
		d := r.Dependents[repo]
		out.Repositories[repo] = store.ReachCounts{Hops: hops, Steps: len(d.Steps),
			Jobs: len(d.Jobs), Workflows: len(d.Workflows)}
	}
	return out
}

// Dependents returns the repositories that would be affected by a change to a node.
//
// This is the on-demand counterpart to the stored counts, used to answer "which repositories
// does a version bump touch" from the graph in memory, without reading the stored breakdown.
func (b *Builder) Dependents(nodeKey string) ([]string, error) {
	target, err := b.Store.NodeID(nodeKey)
	if err != nil {
		return nil, err
	}

	m, err := b.load()
	if err != nil {
		return nil, err
	}
	reach := m.reachOf(target, b.MaxDepth)
	if len(reach.Repositories) == 0 {
		return nil, nil
	}

	rows, err := b.Store.OrderedRepositories()
	if err != nil {
		return nil, err
	}

	var out []string
	for _, r := range rows {
		if _, reaches := reach.Repositories[r.ID]; reaches {
			out = append(out, r.Name)
		}
	}
	return out, nil
}

// RebuildAggregates recomputes the passes that clear their table and derive it again. Their
// cost tracks corpus size rather than how much changed, so callers schedule them deliberately.
func (b *Builder) RebuildAggregates() error {
	_, err := b.Rebuild()
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
