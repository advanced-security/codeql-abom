package graph

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// reachedNode carries the commit and consumer bindings that make a route possible.
// excludes is sorted and comma-joined so reachedNode stays usable as a map key.
type reachedNode struct {
	node     int64
	commit   string
	requires int64
	excludes string
}

// exposureOf walks dependents from target, keeping each repository's shortest route.
//
// Routes stay inside the dependency commit a consumer pinned; exposureagreement_test.go holds
// this walk to the same rule as the submission closure.
func (m *model) exposureOf(target int64, maxDepth int) map[int64]reachPath {
	out := map[int64]reachPath{}

	// from records the node each container was reached from, so a path can be walked back.
	from := map[int64]int64{}
	// enteredBy records the step that made the reference, which is where an `if:` sits. The
	// container alone cannot say it: a workflow has many steps and only one of them named this.
	enteredBy := map[int64]int64{}
	start := reachedNode{node: target}
	visited := map[reachedNode]bool{start: true}
	frontier := []reachedNode{start}

	for depth := 1; len(frontier) > 0; depth++ {
		if maxDepth > 0 && depth > maxDepth {
			break
		}
		var next []reachedNode
		for _, cur := range frontier {
			for _, in := range m.reverse[cur.node] {
				// Leaving a dependency requires a consumer that can run this commit.
				requires, excludes, ok := m.admits(in, cur)
				if !ok {
					continue
				}

				// Steps are an implementation detail; what a caller references is the
				// workflow or composite action holding them.
				vias := []reachedNode{{node: in.src, requires: requires, excludes: excludes}}
				if c, ok := m.container[in.src]; ok {
					vias[0].node = c
					// Crossing into a dependency binds the onward route to one shipped commit.
					if m.isExternal[c] {
						vias = vias[:0]
						for _, commit := range in.commits {
							vias = append(vias, reachedNode{
								node: c, commit: commit, requires: requires, excludes: excludes,
							})
						}
					}
				}
				if _, ok := from[vias[0].node]; !ok && vias[0].node != target {
					from[vias[0].node] = cur.node
					enteredBy[vias[0].node] = in.src
				}

				if repo, ok := m.repoOf[in.src]; ok && m.seedRepo[repo] &&
					(requires == 0 || requires == repo) && !listed(excludes, repo) {
					if _, seen := out[repo]; !seen {
						path, conditions := m.routeFrom(vias[0].node, target, from, enteredBy)
						out[repo] = reachPath{hops: depth, path: path, conditions: conditions}
					}
				}
				for _, via := range vias {
					if via.node != in.src && !visited[via] {
						visited[via] = true
						next = append(next, via)
					}
				}
			}
		}
		frontier = next
	}
	return out
}

// admits reports whether a reference can reach cur.commit and how that binds later routes.
func (m *model) admits(in inEdge, cur reachedNode) (int64, string, bool) {
	if cur.commit == "" {
		return cur.requires, cur.excludes, true
	}
	locks := m.locks[in.row]
	own := false
	for _, l := range locks {
		if !l.own {
			continue
		}
		own = true
		if l.commit == cur.commit {
			return cur.requires, cur.excludes, true
		}
	}
	if own {
		return 0, "", false
	}
	if m.resolved[m.keyOf[cur.node]+"\x00"+in.ref] == cur.commit {
		excludes := cur.excludes
		blocked := false
		for _, l := range locks {
			if l.commit == cur.commit {
				continue
			}
			if cur.requires == l.consumer {
				blocked = true
			}
			excludes = withListed(excludes, l.consumer)
		}
		if !blocked {
			return cur.requires, excludes, true
		}
	}
	for _, l := range locks {
		if l.commit == cur.commit && (cur.requires == 0 || cur.requires == l.consumer) &&
			!listed(cur.excludes, l.consumer) {
			return l.consumer, cur.excludes, true
		}
	}
	return 0, "", false
}

// listed reports whether a comma-joined set of repository ids holds one.
func listed(set string, id int64) bool {
	if set == "" {
		return false
	}
	return slices.Contains(strings.Split(set, ","), strconv.FormatInt(id, 10))
}

// withListed adds a repository id to a comma-joined set, kept sorted so equal sets compare equal.
func withListed(set string, id int64) string {
	if listed(set, id) {
		return set
	}
	ids := []string{strconv.FormatInt(id, 10)}
	if set != "" {
		ids = append(ids, strings.Split(set, ",")...)
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
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
