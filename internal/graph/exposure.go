package graph

import (
	"encoding/json"
	"fmt"
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

	tx, err := b.Store.DB().Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM abom_exposure`); err != nil {
		return fmt.Errorf("clearing exposure: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO abom_exposure (repository_row_id, node_row_id, ghsa_id, hops, path)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repository_row_id, node_row_id, ghsa_id) DO UPDATE SET
			hops = excluded.hops, path = excluded.path`)
	if err != nil {
		return fmt.Errorf("preparing exposure insert: %w", err)
	}
	defer stmt.Close()

	for _, t := range targets {
		for repo, e := range m.exposureOf(t.nodeID, b.MaxDepth) {
			path, err := json.Marshal(e.path)
			if err != nil {
				return fmt.Errorf("encoding path: %w", err)
			}
			if _, err := stmt.Exec(repo, t.nodeID, t.ghsaID, e.hops, string(path)); err != nil {
				return fmt.Errorf("recording exposure of %d to %s: %w", repo, t.ghsaID, err)
			}
		}
	}
	return tx.Commit()
}

// affectedNodes lists the graph nodes covered by a live advisory.
//
// Advisory packages name a repository, so one package covers every subpath action in it.
func (b *Builder) affectedNodes() ([]affected, error) {
	rows, err := b.Store.DB().Query(`
		SELECT DISTINCT a.ghsa_id, n.row_id
		FROM abom_advisory a
		JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		JOIN abom_node_live n
		  ON n.owner IS NOT NULL AND n.repo IS NOT NULL
		 AND n.owner || '/' || n.repo = p.package_name
		WHERE a.withdrawn_at IS NULL`)
	if err != nil {
		return nil, fmt.Errorf("listing affected nodes: %w", err)
	}
	defer rows.Close()

	var out []affected
	for rows.Next() {
		var a affected
		if err := rows.Scan(&a.ghsaID, &a.nodeID); err != nil {
			return nil, fmt.Errorf("scanning affected node: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
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
