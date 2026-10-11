package graph

import (
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Exposure is one repository's shortest path to an advisory-affected action.
type Exposure struct {
	RepositoryID int64
	NodeID       int64
	GHSAID       string
	// Hops of 1 means the repository's own workflow uses the affected action.
	Hops int
	// Path runs from the repository's own workflow to the affected action.
	Path []string
}

// affected is one action an advisory covers.
type affected struct {
	ghsaID string
	nodeID int64
}

// BuildExposure records how each repository under review reaches an advisory-affected action.
//
// Blast radius answers how many repositories are touched; exposure answers by what route, which
// is what decides whether a fix is available to the organization at all. A direct use can be
// bumped where it is; a transitive one waits on an intermediary that may belong to someone else.
func (b *Builder) BuildExposure() error {
	m, err := b.load()
	if err != nil {
		return err
	}
	targets, err := b.affectedNodes()
	if err != nil {
		return err
	}
	_, err = b.writeExposure(m, targets)
	return err
}

// writeExposure rewrites all exposure from m, unmarked for the reason writeBlastRadius is.
func (b *Builder) writeExposure(m *model, targets []affected) (Derivation, error) {
	var d Derivation
	tx, err := b.Store.BeginDerivation()
	if err != nil {
		return d, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := tx.ClearReachMark(); err != nil {
		return d, err
	}
	n, err := tx.ClearExposure()
	if err != nil {
		return d, err
	}
	d.Rows += n
	if err := tx.PrepareExposure(); err != nil {
		return d, err
	}

	for _, t := range targets {
		n, err := tx.WriteExposure(t.nodeID, t.ghsaID, storedExposure(m.exposureOf(t.nodeID, b.MaxDepth)))
		if err != nil {
			return d, err
		}
		d.Walked, d.Rows = d.Walked+1, d.Rows+n
	}
	return d, tx.Commit()
}

func storedExposure(exposed map[int64]reachPath) []store.ExposureRoute {
	routes := make([]store.ExposureRoute, 0, len(exposed))
	for repo, e := range exposed {
		routes = append(routes, store.ExposureRoute{RepositoryID: repo, Hops: e.hops, Path: e.path})
	}
	return routes
}

// affectedNodes lists the graph nodes covered by a live advisory.
//
// Advisory packages name a repository, so one package covers every subpath action in it.
func (b *Builder) affectedNodes() ([]affected, error) {
	rows, err := b.Store.AdvisoryTargets()
	if err != nil {
		return nil, err
	}

	var out []affected
	for _, a := range rows {
		out = append(out, affected{ghsaID: a.GHSAID, nodeID: a.NodeID})
	}
	return out, nil
}

type reachPath struct {
	hops       int
	path       []string
	conditions []StepCondition
}

// exposureOf keeps each repository's shortest route to target, and the conditions guarding it.
func (m *model) exposureOf(target int64, maxDepth int) map[int64]reachPath {
	out := map[int64]reachPath{}

	// from records the node each container was reached from, so a path can be walked back.
	from := map[int64]int64{}
	// enteredBy records the step that made the reference, which is where an `if:` sits. The
	// container alone cannot say it: a workflow has many steps and only one of them named this.
	enteredBy := map[int64]int64{}

	m.walk(target, maxDepth, func(s step) {
		if _, ok := from[s.via.node]; !ok && s.via.node != target {
			from[s.via.node] = s.from.node
			enteredBy[s.via.node] = s.in.src
		}
		repo, ok := m.countedFor(s)
		if _, seen := out[repo]; !ok || seen {
			return
		}
		path, conditions := m.routeFrom(s.via.node, target, from, enteredBy)
		out[repo] = reachPath{hops: s.depth, path: path, conditions: conditions}
	})
	return out
}

// routeFrom walks the recorded predecessors back to the target, collecting the containers passed
// through and the `if:` guarding each hop.
//
// Conditions are gathered here rather than joined afterwards because only the walk knows which
// step made each reference; a later join could only say that the container holds a conditional
// step somewhere.
func (m *model) routeFrom(
	start, target int64, from, enteredBy map[int64]int64,
) ([]string, []StepCondition) {
	path := []string{m.keyOf[start]}
	var conditions []StepCondition

	seen := map[int64]bool{start: true}
	for node := start; node != target; {
		if condition := m.conditionOf[enteredBy[node]]; condition != "" {
			conditions = append(conditions, StepCondition{
				In: m.keyOf[node], Condition: condition,
			})
		}
		prev, ok := from[node]
		if !ok {
			break
		}
		// A cycle would otherwise walk forever; the graph admits them via mutual references.
		if seen[prev] {
			break
		}
		seen[prev] = true
		path = append(path, m.keyOf[prev])
		node = prev
	}
	return path, conditions
}
