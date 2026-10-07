package store

import (
	"database/sql"
	"fmt"
)

// A unit is a node kind, and its columns are its own.
//
// Workflows and jobs were listed through the actions column set — reach, total refs, distinct refs
// — which measure being *depended upon*. A workflow is a consumer: nothing points at it, so those
// columns were inapplicable. These two listings carry what those kinds actually have.
//
// The facts come from `abom_observation` rather than `abom_surface`, which declares all of them and
// is never populated, and every one of them is recorded against a *job*: a workflow's answer is an
// aggregate over its jobs, a job's is its own.

// WorkflowSummary is one workflow or reusable workflow as its unit lists it.
type WorkflowSummary struct {
	NodeKey    string `json:"nodeKey"`
	Kind       string `json:"kind"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
	// Triggers is the `on:` events, read from any one job because `on:` is workflow-level and every
	// job of a workflow carries the same value.
	Triggers     string `json:"triggers"`
	Jobs         int    `json:"jobs"`
	Steps        int    `json:"steps"`
	Dependencies int    `json:"dependencies"`
	// PrivilegedJobs is a count rather than a flag: "two of nine" is a different situation from
	// "nine of nine", and a boolean would report them alike.
	PrivilegedJobs int `json:"privilegedJobs"`
	Findings       int `json:"findings"`
	// Severity is the same findings by band.
	Severity SeverityCounts `json:"severity"`
	// DependedOnBy counts references pointing at this workflow, which is only ever non-zero for a
	// reusable one. Reported rather than assumed absent.
	DependedOnBy int `json:"dependedOnBy"`
}

// WorkflowsQuery narrows and orders the workflows unit, and cuts one page out of the match.
type WorkflowsQuery struct {
	Search   string
	Scope    string
	Flags    []string
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping RepositoryScope
}

// WorkflowsPage is one page of workflows and the size of the match it was cut from.
type WorkflowsPage struct {
	Items []WorkflowSummary `json:"items"`
	Total int               `json:"total"`
}

var workflowSorts = map[string]string{
	"nodeKey":      "n.node_key",
	"repository":   "repository",
	"triggers":     "triggers",
	"jobs":         "jobs",
	"steps":        "steps",
	"dependencies": "dependencies",
	"privileged":   "privileged_jobs",
	"findings":     "findings",
	"dependedOnBy": "depended_on_by",
}

// The subqueries every workflow column is built from.
//
// Steps hang off the declaring file rather than off the job, so a workflow's steps and its
// dependencies are one join through `container_node_row_id`; a job's are joined by `job_id` too.
const workflowColumns = `
	       COALESCE(r.repository_full_name, '') AS repository,
	       COALESCE(n.path, '')                 AS path,
	       COALESCE((SELECT o.value FROM abom_observation o
	                   JOIN abom_node_live j ON j.node_key = o.subject_key
	                  WHERE j.container_node_row_id = n.row_id AND j.node_kind = 'job'
	                    AND o.name = 'triggers' LIMIT 1), '') AS triggers,
	       (SELECT COUNT(*) FROM abom_node_live j
	         WHERE j.container_node_row_id = n.row_id AND j.node_kind = 'job') AS jobs,
	       (SELECT COUNT(*) FROM abom_node_live s
	         WHERE s.container_node_row_id = n.row_id AND s.node_kind = 'step') AS steps,
	       (SELECT COUNT(DISTINCT e.dst_node_row_id)
	          FROM abom_node_live s JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
	         WHERE s.container_node_row_id = n.row_id AND s.node_kind = 'step') AS dependencies,
	       (SELECT COUNT(*) FROM abom_node_live j
	          JOIN abom_observation o ON o.subject_key = j.node_key
	         WHERE j.container_node_row_id = n.row_id AND j.node_kind = 'job'
	           AND o.name = 'privileged' AND o.value = 'true') AS privileged_jobs,
	       (SELECT COUNT(*) FROM abom_finding_node fn
	         JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
	         WHERE fn.node_row_id = n.row_id) AS findings,
	       (SELECT COUNT(*) FROM abom_edge_regular e
	         WHERE e.dst_node_row_id = n.row_id) AS depended_on_by`

// WorkflowsPage lists the workflows the scope matches, ordered across the whole match.
func (s *Store) WorkflowsPage(q WorkflowsQuery) (WorkflowsPage, error) {
	order, sorted := workflowSorts[q.Sort]
	if q.Sort != "" && !sorted {
		return WorkflowsPage{}, fmt.Errorf("store: cannot order workflows by %q; try one of %v",
			q.Sort, sortNames(workflowSorts))
	}

	where := `WHERE n.node_kind IN ('workflow', 'reusable_workflow')`
	var args []any
	if q.Search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q.Search)+"%")
	}
	bound, bindings, err := s.restrict(q.Grouping, "r.repository_full_name")
	if err != nil {
		return WorkflowsPage{}, err
	}
	where += bound
	args = append(args, bindings...)

	where += unitScopeFilter(q.Scope)
	where += unitFindingsFlag(q.Flags)

	var total int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id `+where, args...).Scan(&total); err != nil {
		return WorkflowsPage{}, fmt.Errorf("counting workflows: %w", err)
	}

	ordering := ` ORDER BY findings DESC, n.node_key`
	if sorted {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		ordering = unitOrdering(q.Sort, order, direction)
	}

	query := `SELECT n.node_key, n.node_kind,` + workflowColumns + `
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		` + where + ordering
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}

	items, err := queryAll(s, "listing workflows", func(row *sql.Rows) (WorkflowSummary, error) {
		var w WorkflowSummary
		return w, row.Scan(&w.NodeKey, &w.Kind, &w.Repository, &w.Path, &w.Triggers,
			&w.Jobs, &w.Steps, &w.Dependencies, &w.PrivilegedJobs, &w.Findings, &w.DependedOnBy)
	}, query, args...)
	if err != nil {
		return WorkflowsPage{}, err
	}
	keys := make([]string, 0, len(items))
	for _, w := range items {
		keys = append(keys, w.NodeKey)
	}
	bands, err := s.severityByNode(AnalysisModeRegular, keys)
	if err != nil {
		return WorkflowsPage{}, err
	}
	for i := range items {
		items[i].Severity = bands[items[i].NodeKey]
	}
	return WorkflowsPage{Items: items, Total: total}, nil
}

// JobSummary is one job as its unit lists it. Nothing here is aggregated: every fact the graph
// extraction records about a job is recorded against the job.
type JobSummary struct {
	NodeKey  string `json:"nodeKey"`
	JobID    string `json:"jobId"`
	Workflow string `json:"workflow"`
	// RunsOn, PermissionsScope, Privileged and ExternallyTriggerable are empty or false when the
	// extraction never stated them. Absent and false are different, which is why the strings are
	// carried verbatim rather than mapped onto a yes/no.
	RunsOn                string `json:"runsOn"`
	PermissionsScope      string `json:"permissionsScope"`
	Privileged            bool   `json:"privileged"`
	ExternallyTriggerable bool   `json:"externallyTriggerable"`
	SecretsInherit        bool   `json:"secretsInherit"`
	Steps                 int    `json:"steps"`
	Dependencies          int    `json:"dependencies"`
	Findings              int    `json:"findings"`
	// Severity is the same findings by band.
	Severity SeverityCounts `json:"severity"`
}

// JobsQuery narrows and orders the jobs unit, and cuts one page out of the match.
type JobsQuery struct {
	Search   string
	Scope    string
	Flags    []string
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping RepositoryScope
}

// JobsPage is one page of jobs and the size of the match it was cut from.
type JobsPage struct {
	Items []JobSummary `json:"items"`
	Total int          `json:"total"`
}

var jobSorts = map[string]string{
	"nodeKey":      "n.node_key",
	"jobId":        "n.job_id",
	"workflow":     "workflow",
	"runsOn":       "runs_on",
	"privileged":   "privileged",
	"externally":   "externally_triggerable",
	"steps":        "steps",
	"dependencies": "dependencies",
	"findings":     "findings",
}

// observation reads one fact the extraction stated about this node.
func observation(name string) string {
	return `COALESCE((SELECT o.value FROM abom_observation o
	                   WHERE o.subject_key = n.node_key AND o.name = '` + name + `' LIMIT 1), '')`
}

// A job's steps are those sharing its `job_id` inside the same declaring file. Joining on the
// container alone would pool every sibling job's steps into each row.
const jobStepsPredicate = `s.node_kind = 'step' AND s.job_id = n.job_id
	                         AND s.container_node_row_id = n.container_node_row_id`

// JobsPage lists the jobs the scope matches, ordered across the whole match.
func (s *Store) JobsPage(q JobsQuery) (JobsPage, error) {
	order, sorted := jobSorts[q.Sort]
	if q.Sort != "" && !sorted {
		return JobsPage{}, fmt.Errorf("store: cannot order jobs by %q; try one of %v",
			q.Sort, sortNames(jobSorts))
	}

	where := `WHERE n.node_kind = 'job'`
	var args []any
	if q.Search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q.Search)+"%")
	}
	bound, bindings, err := s.restrict(q.Grouping, "r.repository_full_name")
	if err != nil {
		return JobsPage{}, err
	}
	where += bound
	args = append(args, bindings...)

	where += unitScopeFilter(q.Scope)
	where += unitFindingsFlag(q.Flags)

	var total int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id `+where, args...).Scan(&total); err != nil {
		return JobsPage{}, fmt.Errorf("counting jobs: %w", err)
	}

	ordering := ` ORDER BY privileged DESC, findings DESC, n.node_key`
	if sorted {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		ordering = unitOrdering(q.Sort, order, direction)
	}

	query := `
		SELECT n.node_key, COALESCE(n.job_id, ''),
		       COALESCE((SELECT w.node_key FROM abom_node_live w
		                  WHERE w.row_id = n.container_node_row_id), '') AS workflow,
		       ` + observation("runsOn") + ` AS runs_on,
		       ` + observation("permissionsScope") + ` AS permissions_scope,
		       ` + observation("privileged") + ` = 'true' AS privileged,
		       ` + observation("externallyTriggerable") + ` = 'true' AS externally_triggerable,
		       ` + observation("secretsInherit") + ` = 'true' AS secrets_inherit,
		       (SELECT COUNT(*) FROM abom_node_live s WHERE ` + jobStepsPredicate + `) AS steps,
		       (SELECT COUNT(DISTINCT e.dst_node_row_id)
		          FROM abom_node_live s JOIN abom_edge_regular e ON e.src_node_row_id = s.row_id
		         WHERE ` + jobStepsPredicate + `) AS dependencies,
		       (SELECT COUNT(*) FROM abom_finding_node fn
		         JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		         WHERE fn.node_row_id = n.row_id) AS findings
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		` + where + ordering
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}

	items, err := queryAll(s, "listing jobs", func(row *sql.Rows) (JobSummary, error) {
		var j JobSummary
		return j, row.Scan(&j.NodeKey, &j.JobID, &j.Workflow, &j.RunsOn, &j.PermissionsScope,
			&j.Privileged, &j.ExternallyTriggerable, &j.SecretsInherit,
			&j.Steps, &j.Dependencies, &j.Findings)
	}, query, args...)
	if err != nil {
		return JobsPage{}, err
	}
	keys := make([]string, 0, len(items))
	for _, j := range items {
		keys = append(keys, j.NodeKey)
	}
	bands, err := s.severityByNode(AnalysisModeRegular, keys)
	if err != nil {
		return JobsPage{}, err
	}
	for i := range items {
		items[i].Severity = bands[items[i].NodeKey]
	}
	return JobsPage{Items: items, Total: total}, nil
}

// unitScopeFilter bounds a unit to the view's scope, the way the node listing reads it.
func unitScopeFilter(scope string) string {
	switch scope {
	case ScopeExposure:
		return ` AND EXISTS (SELECT 1 FROM abom_exposure x
		                      WHERE x.repository_row_id = n.repository_row_id)`
	case ScopeOwned:
		return ` AND n.row_id IN (` + ownedNodes + `)`
	case ScopeSupplyChain:
		return ` AND n.row_id IN (` + scopeNodes(declaredRoots, AnalysisModeRegular) + `)`
	}
	return ""
}

// unitFindingsFlag is the one flag these units can answer; the rest describe a dependency.
func unitFindingsFlag(flags []string) string {
	for _, f := range flags {
		if f == "findings" {
			return ` AND EXISTS (SELECT 1 FROM abom_finding_node fn
			                      JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
			                      WHERE fn.node_row_id = n.row_id)`
		}
	}
	return ""
}

func unitOrdering(sort, order, direction string) string {
	if sort != "findings" {
		return ` ORDER BY ` + order + direction + `, n.node_key`
	}
	worst := `COALESCE((SELECT MAX(` + bandRankSQL("f.security_severity") + `)
		FROM abom_finding_node fn JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		WHERE fn.node_row_id = n.row_id), -1)`
	return ` ORDER BY ` + worst + direction + `, findings` + direction + `, n.node_key`
}
