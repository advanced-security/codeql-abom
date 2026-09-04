package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// ActionSummary is one action or workflow as the inventory lists it.
type ActionSummary struct {
	NodeKey string `json:"nodeKey"`
	Kind    string `json:"kind"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	// Origin separates what the organization owns from what it merely depends on.
	Origin string `json:"origin"`
	// DependentRepos and DirectDependents come from the graph, not from any advisory.
	DependentRepos   int `json:"dependentRepos"`
	DirectDependents int `json:"directDependents"`
	// DistinctRefs counts the different references pointing here. One action used at forty
	// different commits is forty upgrade decisions, which a total reference count hides.
	DistinctRefs int `json:"distinctRefs"`
	Findings     int `json:"findings"`
	// Advisories counts live advisories naming this action's repository. It is a count of
	// published statements, not a judgement that this reference is affected.
	Advisories int `json:"advisories"`
	// Analyzed reports whether the dependency itself has been expanded, which is what decides
	// if anything downstream of it is known.
	Analyzed bool `json:"analyzed"`
	// Refs are the references observed across the corpus, most used first, verbatim.
	Refs []string `json:"refs"`
}

// NodeMatch is one candidate for the node picker.
type NodeMatch struct {
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	Repository     string `json:"repository"`
	Origin         string `json:"origin"`
	DependentRepos int    `json:"dependentRepos"`
}

// SearchNodes finds nodes a reader could sensibly stand on.
//
// Steps and jobs are excluded: they are implementation detail rather than somewhere to focus.
// An empty query returns the widest-reaching candidates, because that is the opening state of
// the picker and returning nothing there would read as broken.
func (s *Store) SearchNodes(search string, limit int) ([]NodeMatch, error) {
	where := `WHERE n.node_kind NOT IN ('step', 'job')`
	args := []any{}
	if search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}

	query := `
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), COALESCE(b.dependent_repos, 0)
		FROM abom_node n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		` + where + `
		ORDER BY COALESCE(b.dependent_repos, 0) DESC, n.node_key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	return queryAll(s, "searching nodes", func(r *sql.Rows) (NodeMatch, error) {
		var m NodeMatch
		return m, r.Scan(&m.Key, &m.Kind, &m.Repository, &m.Origin, &m.DependentRepos)
	}, query, args...)
}

// ActionsInventory excludes steps and jobs as implementation detail; repositories have their
// own listing.
func (s *Store) ActionsInventory(search string, limit int) ([]ActionSummary, error) {
	where := `WHERE n.node_kind IN ('action', 'composite_action', 'workflow',
	                               'reusable_workflow', 'docker_image')`
	args := []any{}
	if search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}

	query := `
		SELECT n.node_key, n.node_kind, COALESCE(n.owner,''), COALESCE(n.repo,''),
		       COALESCE(r.origin, ''),
		       COALESCE(b.dependent_repos, 0), COALESCE(b.direct_dependents, 0),
		       (SELECT COUNT(DISTINCT e.ref_value) FROM abom_edge e
		         WHERE e.dst_node_row_id = n.row_id
		           AND e.ref_value IS NOT NULL AND e.ref_value <> ''),
		       (SELECT COUNT(*) FROM abom_finding_node fn WHERE fn.node_row_id = n.row_id),
		       (SELECT COUNT(DISTINCT a.ghsa_id)
		          FROM abom_advisory a
		          JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		         WHERE a.withdrawn_at IS NULL
		           AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		           AND p.package_name = n.owner || '/' || n.repo),
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done')
		FROM abom_node n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		` + where + `
		ORDER BY COALESCE(b.dependent_repos, 0) DESC, n.node_key`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	out, err := queryAll(s, "listing actions", func(r *sql.Rows) (ActionSummary, error) {
		var a ActionSummary
		return a, r.Scan(&a.NodeKey, &a.Kind, &a.Owner, &a.Repo, &a.Origin,
			&a.DependentRepos, &a.DirectDependents, &a.DistinctRefs, &a.Findings,
			&a.Advisories, &a.Analyzed)
	}, query, args...)
	if err != nil {
		return nil, err
	}

	for i := range out {
		refs, err := s.referencesTo(out[i].NodeKey)
		if err != nil {
			return nil, err
		}
		out[i].Refs = refs
	}
	return out, nil
}

// RepositorySummary is one repository as the inventory lists it.
type RepositorySummary struct {
	FullName string `json:"fullName"`
	// Origin separates repositories under review from those fetched as dependencies.
	Origin          string `json:"origin"`
	Workflows       int    `json:"workflows"`
	Dependencies    int    `json:"dependencies"`
	Findings        int    `json:"findings"`
	Exposures       int    `json:"exposures"`
	DirectExposures int    `json:"directExposures"`
}

// RepositoriesOverview lists repositories under review, most findings first.
//
// Upstream repositories are excluded: they are not the organization's to remediate, and listing
// them alongside would imply otherwise.
func (s *Store) RepositoriesOverview(search string, limit int) ([]RepositorySummary, error) {
	where := `WHERE r.origin = ?`
	args := []any{OriginSeed}
	if search != "" {
		where += ` AND r.repository_full_name LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}

	query := `
		SELECT r.repository_full_name, r.origin,
		       (SELECT COUNT(*) FROM abom_node n
		         WHERE n.repository_row_id = r.row_id AND n.node_kind = 'workflow'),
		       (SELECT COUNT(*) FROM abom_edge e WHERE e.repository_row_id = r.row_id),
		       (SELECT COUNT(*) FROM abom_finding f WHERE f.repository_row_id = r.row_id),
		       (SELECT COUNT(*) FROM abom_exposure x WHERE x.repository_row_id = r.row_id),
		       (SELECT COUNT(*) FROM abom_exposure x
		         WHERE x.repository_row_id = r.row_id AND x.hops = 1)
		FROM repository r
		` + where + `
		ORDER BY 5 DESC, r.repository_full_name`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	return queryAll(s, "listing repositories", func(row *sql.Rows) (RepositorySummary, error) {
		var r RepositorySummary
		return r, row.Scan(&r.FullName, &r.Origin, &r.Workflows, &r.Dependencies,
			&r.Findings, &r.Exposures, &r.DirectExposures)
	}, query, args...)
}

// RuleCount is how often one rule fired within a scope.
type RuleCount struct {
	RuleID string `json:"ruleId"`
	Count  int    `json:"count"`
}

// WorkflowSummary is one workflow within a repository.
type WorkflowSummary struct {
	NodeKey      string `json:"nodeKey"`
	Path         string `json:"path"`
	Dependencies int    `json:"dependencies"`
	Findings     int    `json:"findings"`
}

// RepositoryDetail is everything the interface shows for one repository.
type RepositoryDetail struct {
	FullName string `json:"fullName"`
	Origin   string `json:"origin"`
	// FindingsByRule keeps every claim attributed to the rule that made it.
	FindingsByRule []RuleCount       `json:"findingsByRule"`
	Workflows      []WorkflowSummary `json:"workflows"`
	Exposures      []Exposure        `json:"exposures"`
}

// RepositoryDetailOf returns one repository's posture.
func (s *Store) RepositoryDetailOf(fullName string) (RepositoryDetail, error) {
	out := RepositoryDetail{FullName: fullName}

	var rowID int64
	if err := s.db.QueryRow(
		`SELECT row_id, origin FROM repository WHERE repository_full_name = ?`, fullName).
		Scan(&rowID, &out.Origin); err != nil {
		return out, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}

	rules, err := s.db.Query(`
		SELECT rule_id, COUNT(*) FROM abom_finding
		WHERE repository_row_id = ? GROUP BY rule_id ORDER BY 2 DESC`, rowID)
	if err != nil {
		return out, fmt.Errorf("reading findings for %q: %w", fullName, err)
	}
	for rules.Next() {
		var rc RuleCount
		if err := rules.Scan(&rc.RuleID, &rc.Count); err != nil {
			rules.Close()
			return out, err
		}
		out.FindingsByRule = append(out.FindingsByRule, rc)
	}
	rules.Close()
	if err := rules.Err(); err != nil {
		return out, err
	}

	flows, err := s.db.Query(`
		SELECT n.node_key, COALESCE(n.path,''),
		       (SELECT COUNT(*) FROM abom_edge e
		         JOIN abom_node src ON src.row_id = e.src_node_row_id
		        WHERE src.container_node_row_id = n.row_id OR src.row_id = n.row_id),
		       (SELECT COUNT(*) FROM abom_finding_node fn WHERE fn.node_row_id = n.row_id)
		FROM abom_node n
		WHERE n.repository_row_id = ? AND n.node_kind IN ('workflow', 'composite_action')
		ORDER BY n.path`, rowID)
	if err != nil {
		return out, fmt.Errorf("reading workflows for %q: %w", fullName, err)
	}
	for flows.Next() {
		var wf WorkflowSummary
		if err := flows.Scan(&wf.NodeKey, &wf.Path, &wf.Dependencies, &wf.Findings); err != nil {
			flows.Close()
			return out, err
		}
		out.Workflows = append(out.Workflows, wf)
	}
	flows.Close()
	if err := flows.Err(); err != nil {
		return out, err
	}

	all, err := s.ExposuresByRepository(0)
	if err != nil {
		return out, err
	}
	for _, e := range all {
		if e.Repository == fullName {
			out.Exposures = append(out.Exposures, e)
		}
	}
	return out, nil
}

// escapeLike neutralizes wildcards so a search for "actions/" cannot match everything.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
