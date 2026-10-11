package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// ActionSummary is one action or workflow as the inventory lists it.
type ActionSummary struct {
	NodeKey string `json:"nodeKey"`
	Kind    string `json:"kind"`
	Owner   string `json:"owner"`
	Repo    string `json:"repo"`
	// Origin separates what the organization owns from what it merely depends on.
	Origin string `json:"origin"`
	// Owned is whether the declaration covers the row's repository, which Origin cannot say: a
	// starting point nobody declared is seeded and not yours.
	Owned bool `json:"owned"`
	// DependentRepos are the starting points that run this, directly or through another action,
	// and the steps, jobs and workflow files below are theirs. All come from the graph, never from
	// an advisory. A level is nil where reach has not counted it yet, which is not none.
	DependentRepos     *int `json:"dependentRepos"`
	DependentSteps     *int `json:"dependentSteps"`
	DependentJobs      *int `json:"dependentJobs"`
	DependentWorkflows *int `json:"dependentWorkflows"`
	// DependentGroups counts the values of the grouping property among those repositories; nil
	// where no grouping property is configured.
	DependentGroups *int `json:"dependentGroups"`
	// DistinctRefs counts the different references pointing here. One action used at forty
	// different commits is forty upgrade decisions, which a total reference count hides.
	DistinctRefs *int `json:"distinctRefs"`
	Findings     int  `json:"findings"`
	// Severity is the same findings by band, summing to Findings.
	Severity SeverityCounts `json:"severity"`
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
func (s *Store) SearchNodes(search string, limit int, sc RepositoryScope) ([]NodeMatch, error) {
	in, err := s.reachInScope(sc, false)
	if err != nil {
		return nil, err
	}
	reach, reachArgs := in.count()
	keep, keepArgs := in.keep()
	where := `WHERE n.node_kind NOT IN ('step', 'job')` + keep
	args := append(append([]any{}, reachArgs...), keepArgs...)
	if search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(search)+"%")
	}

	query := `
		SELECT n.node_key, n.node_kind, COALESCE(r.repository_full_name, ''),
		       COALESCE(r.origin, ''), ` + reach + `
		FROM abom_node_live n
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

// ErrReachNotDerived reports that the reach breakdown has not been written since the rollup was.
//
// Reach is recomputed wholesale, so an upgraded corpus has the table and none of the rows. Bounding
// an answer to a grouping then would report that grouping as depending on nothing, which is an
// absent measurement wearing the face of a measurement of zero.
var ErrReachNotDerived = errors.New(
	"reach has not been derived per repository since the last rebuild, so an answer cannot be " +
		"bounded to a grouping or to what you own; run expand-all to refresh the aggregates")

// reachIn is a grouping resolved once for every reach expression a query bounds to it: a restriction
// on the repositories reaching a node, and its arguments. The zero value bounds nothing.
type reachIn struct {
	bound string
	args  []any
	asked bool
	// grouped is set when a grouping, not only ownership, bounds the reaching repositories.
	grouped bool
}

// reachInScope bounds reach to the grouping, and to the declared repositories when owned: a declared
// scope lists what they hold or reach, and a count spanning every starting point would not be theirs.
func (s *Store) reachInScope(sc RepositoryScope, owned bool) (reachIn, error) {
	if !sc.asked() && !owned {
		return reachIn{}, nil
	}
	if err := s.reachIsDerived(); err != nil {
		return reachIn{}, err
	}
	bound, args, err := s.restrict(sc, "rr.repository_full_name")
	if owned {
		bound += ` AND ` + ownedFullName("rr.repository_full_name")
	}
	return reachIn{bound: bound, args: args, asked: true, grouped: sc.asked()}, err
}

// reachIsDerived refuses when the rollup asserts reach the breakdown cannot account for.
func (s *Store) reachIsDerived() error {
	var stale bool
	if err := s.db.QueryRow(`
		SELECT EXISTS(SELECT 1 FROM abom_blast_radius WHERE dependent_repos > 0)
		   AND NOT EXISTS(SELECT 1 FROM abom_node_reach)`).Scan(&stale); err != nil {
		return fmt.Errorf("checking the reach breakdown: %w", err)
	}
	if stale {
		return ErrReachNotDerived
	}
	return nil
}

// keep filters rows to the nodes the scope's repositories reach.
//
// Driven from the pair table rather than tested per node: the planner turns this into a covering
// index scan feeding a primary key lookup, where the same condition written as a correlated
// subquery makes it scan every node and evaluate the subquery for each.
// A declared scope's rows are its own filter's, so ownership alone bounds counts and not rows.
func (r reachIn) keep() (string, []any) {
	if !r.grouped {
		return "", nil
	}
	return ` AND n.row_id IN (SELECT nr.node_row_id FROM abom_node_reach nr
		          JOIN repository rr ON rr.row_id = nr.repository_row_id
		         WHERE true` + r.bound + `)`, r.args
}

// over aggregates the breakdown of the node's reach within the scope, correlated to `n`, among the
// reaching repositories `also` admits.
func (r reachIn) over(aggregate, also string) string {
	return `(SELECT ` + aggregate + ` FROM abom_node_reach nr
		  JOIN repository rr ON rr.row_id = nr.repository_row_id
		 WHERE nr.node_row_id = n.row_id` + r.bound + also + `)`
}

// count is how many repositories reach the node in scope. Unbounded it is the stored rollup;
// bounded it counts the breakdown behind it, because a count carried over from the organization
// under a filtered row list is the defect this exists to prevent. One walk writes both.
func (r reachIn) count() (string, []any) {
	if !r.asked {
		return `COALESCE(b.dependent_repos, 0)`, nil
	}
	return r.over(`COUNT(*)`, ""), r.args
}

// levels selects what in the starting points depends on the node below the repository, and how
// many groups of them: the stored totals when nothing narrows the list, else the sums over the
// repositories in the grouping. Groups are the values of the grouping property among the owned
// repositories reaching it, each repository counted under its first value as the Explorer groups it.
func (r reachIn) levels(property string) (string, []any) {
	var parts []string
	var args []any
	for _, level := range []string{"steps", "jobs", "workflows"} {
		stored := `b.dependent_` + level
		if !r.asked {
			parts = append(parts, measured(stored)+` AS dependent_`+level)
			continue
		}
		parts = append(parts, `(CASE WHEN `+stored+` IS NULL THEN NULL ELSE `+
			r.over(`COALESCE(SUM(nr.`+level+`), 0)`, "")+` END) AS dependent_`+level)
		args = append(args, r.args...)
	}
	if property == "" {
		return strings.Join(parts, ", ") + `, NULL AS dependent_groups`, args
	}
	parts = append(parts, `(CASE WHEN `+reachKnown+` THEN `+r.over(`COUNT(DISTINCT (
			SELECT MIN(p.value) FROM abom_repository_property p
			 WHERE p.repository_full_name = rr.repository_full_name COLLATE NOCASE AND p.property_name = ?))`,
		` AND `+ownedFullName("rr.repository_full_name"))+` END) AS dependent_groups`)
	// The property is bound inside the aggregate, ahead of the scope's own arguments.
	return strings.Join(parts, ", "), append(append(args, property), r.args...)
}

// measured reads a stored level: zero where reach ran and wrote no row because nothing references
// the node, and NULL where it has not run since the node arrived or the row predates the level.
func measured(column string) string {
	return `COALESCE(` + column + `, CASE WHEN b.node_row_id IS NULL AND ` + reachKnown + ` THEN 0 END)`
}

// runsOrHolds is what the Actions unit lists: what a starting point runs or holds, and whatever
// reach has not measured yet. Anything else is used only by other repositories, or by nothing.
// Total, so that its negation counts the rest rather than reading NULL as neither.
var runsOrHolds = `(n.node_kind NOT IN ('action', 'composite_action', 'docker_image')
	OR COALESCE(r.origin, '') = 'seed' OR NOT ` + reachKnown + ` OR COALESCE(b.dependent_repos, 0) > 0)`

// InventoryQuery narrows and orders the inventory, and cuts one page out of what matched.
type InventoryQuery struct {
	Search   string
	Kinds    []string
	Origins  []string
	MinReach int
	// Scope is the view's own scope term, `owned` or `exposure`.
	Scope    string
	Flags    []string
	RefStyle string
	// At keeps what some workflow names at exactly one of these references, verbatim.
	At []string
	// The findings terms, carrying the meanings the set path gives them: a node matches when it
	// carries a finding that satisfies all of them at once. `repositories` is the repository the
	// *finding* sits in, as it is there — "which dependencies does this repository use" is `scope`.
	Rules              []string
	NotRules           []string
	Tags               []string
	NotTags            []string
	Provider           string
	Severity           string
	SecuritySeverities []string
	Sources            []string
	Repositories       []string
	// unsourced narrows to findings no evaluation recorded a source for, which is how a page
	// narrowed by source counts the rows it could not answer for.
	unsourced bool
	// unprovided reads the provider term as "none was recorded", for the same reason.
	unprovided bool
	// Mode narrows to how the databases behind this page were built. Empty is `regular`, which is
	// every database on record, so a listing that does not ask counts what it always counted.
	Mode string
	// Sort names a column from inventorySorts. Empty is the default order, widest reach first.
	Sort   string
	Desc   bool
	Offset int
	Limit  int
	// Grouping is the repository-property scope, which is a different question from Scope.
	Grouping RepositoryScope
	// GroupProperty names the property whose values divide starting points into groups. It is
	// configuration, never a term: empty counts no groups rather than guessing a property.
	GroupProperty string
}

// InventoryPage is one page of the inventory and the size of the match it was cut from.
type InventoryPage struct {
	Items []ActionSummary `json:"items"`
	// Total is how many rows the predicate matched before paging.
	Total int `json:"total"`
	// Elsewhere names the kinds outside this view that the findings terms also matched.
	// Without it, a findings question can answer 0 in the visible kinds while matching elsewhere.
	Elsewhere []KindCount `json:"elsewhere,omitempty"`
	// Unsourced counts the rows that carry a finding every other term matched with no recorded
	// source. Counted only when a source is asked, which is the only time they are left out.
	Unsourced int `json:"unsourced"`
	// Unprovided counts the rows whose matching finding recorded no provider, when one is asked.
	Unprovided int `json:"unprovided"`
	// GroupProperty is the property the groups were counted by, empty where none is configured.
	GroupProperty string `json:"groupProperty"`
	// Unlisted counts the actions the other terms matched that no starting point runs or holds.
	Unlisted int `json:"unlisted"`
}

// KindCount is how many nodes of one kind a predicate matched.
type KindCount struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// inventorySorts maps a sort name to the expression it orders by.
//
// A whitelist rather than an escape, for two reasons: a column name reaching SQL from a query
// string is an injection unless it is checked against what exists, and a sort that is silently
// dropped shows the reader an order they did not ask for.
var inventorySorts = map[string]string{
	"nodeKey": "n.node_key",
	"kind":    "n.node_kind",
	// In the words the column shows: owned, a starting point nobody declared, upstream, external.
	"origin":             `(CASE WHEN ` + ownedNode + ` = 1 THEN 0 WHEN r.origin = 'seed' THEN 1 WHEN r.origin = 'upstream' THEN 2 ELSE 3 END)`,
	"dependentRepos":     "dependent_repos",
	"dependentSteps":     "dependent_steps",
	"dependentJobs":      "dependent_jobs",
	"dependentWorkflows": "dependent_workflows",
	"dependentGroups":    "dependent_groups",
	"distinctRefs":       "distinct_refs",
	// Ordered by severityOrdering, which no single expression can say.
	"findings":   "",
	"advisories": "advisories",
	"analyzed":   "analyzed",
}

// InventorySortNames lists what may be sorted on, so the interface offers exactly what is applied.
func InventorySortNames() []string { return sortNames(inventorySorts) }

// ActionsInventoryPage lists one page of actions and workflows, ordered across the whole match.
//
// Ordering and cutting both happen here because they cannot be separated: a page taken by reach
// and then sorted by findings in the browser answers "the most findings among the widest-reaching
// rows", which is a different question from the one the column header asks.
func (s *Store) ActionsInventoryPage(q InventoryQuery) (InventoryPage, error) {
	plan, err := s.inventoryPageQuery(q)
	if err != nil {
		return InventoryPage{}, err
	}
	// The unit lists what starting points run or hold; a grouping already keeps only what it reaches.
	unlisted := ""
	if !q.Grouping.asked() {
		unlisted = plan.where + ` AND NOT ` + runsOrHolds
		plan.where += ` AND ` + runsOrHolds
	}

	total, err := s.inventoryTotal(plan.where, plan.whereArgs)
	if err != nil {
		return InventoryPage{}, err
	}

	out, err := s.inventoryPageItems(q, plan)
	if err != nil {
		return InventoryPage{}, err
	}

	elsewhere, err := s.inventoryElsewhere(q)
	if err != nil {
		return InventoryPage{}, err
	}
	page := InventoryPage{Items: out, Total: total, Elsewhere: elsewhere, GroupProperty: q.GroupProperty}
	if unlisted != "" {
		if page.Unlisted, err = s.inventoryTotal(unlisted, plan.whereArgs); err != nil {
			return InventoryPage{}, err
		}
	}
	if err := s.attachInventoryUnknownCounts(q, &page); err != nil {
		return InventoryPage{}, err
	}
	return page, nil
}

type inventoryPageQuery struct {
	reach, levels        string
	where, ordering      string
	reachArgs, levelArgs []any
	whereArgs, orderArgs []any
}

type inventoryWhere struct {
	clause string
	args   []any
}

func (s *Store) inventoryPageQuery(q InventoryQuery) (inventoryPageQuery, error) {
	if err := s.prepareScope(q.Scope, q.Mode); err != nil {
		return inventoryPageQuery{}, err
	}
	order, ok := inventorySorts[q.Sort]
	if q.Sort != "" && !ok {
		return inventoryPageQuery{}, fmt.Errorf(
			"store: cannot sort the inventory by %q; it sorts by one of %v",
			q.Sort, InventorySortNames())
	}

	in, err := s.reachInScope(q.Grouping, declaresScope(q.Scope))
	if err != nil {
		return inventoryPageQuery{}, err
	}
	reach, reachArgs := in.count()
	levels, levelArgs := in.levels(q.GroupProperty)
	keep, keepArgs := in.keep()

	where := buildInventoryWhere(q, keep, keepArgs, reach, reachArgs)
	if q.RefStyle != "" || len(q.At) > 0 {
		table, err := s.declaredEdgeTable(q.Mode)
		if err != nil {
			return inventoryPageQuery{}, err
		}
		declared, declaredArgs := inventoryDeclarationFilter(q, table)
		where.clause, where.args = where.clause+declared, append(where.args, declaredArgs...)
	}
	ordering, orderArgs := inventoryOrdering(q, order, ok, reach, reachArgs)
	// What a row shows says where reach has not been measured; what filters and orders reads zero.
	shown := measured("b.dependent_repos")
	if in.asked {
		shown = `(CASE WHEN ` + shown + ` IS NULL THEN NULL ELSE ` + reach + ` END)`
	}
	return inventoryPageQuery{
		reach: shown, levels: levels, where: where.clause, ordering: ordering,
		reachArgs: reachArgs, levelArgs: levelArgs, whereArgs: where.args, orderArgs: orderArgs,
	}, nil
}

func buildInventoryWhere(
	q InventoryQuery, keep string, keepArgs []any, reach string, reachArgs []any,
) inventoryWhere {
	// The three default kinds match columns about being depended upon. Workflows and jobs are
	// consumers with their own units. Naming a kind still reaches past these defaults.
	where := `WHERE n.node_kind IN ('action', 'composite_action', 'docker_image')`
	if len(q.Kinds) > 0 {
		where = `WHERE n.node_kind IN (` + placeholders(len(q.Kinds)) + `)`
	}
	where += keep + nodeModeFilter(q.Mode) + visibleWorkflowNode
	// Tracked apart from the select-list arguments, because the count runs the same WHERE without
	// the select list and the two argument orders would otherwise have to be kept in step by hand.
	var whereArgs []any
	for _, k := range q.Kinds {
		whereArgs = append(whereArgs, k)
	}
	whereArgs = append(whereArgs, keepArgs...)
	if q.Search != "" {
		where += ` AND n.node_key LIKE ? ESCAPE '\'`
		whereArgs = append(whereArgs, "%"+escapeLike(q.Search)+"%")
	}
	if len(q.Origins) > 0 {
		where += ` AND ` + originClause(q.Origins)
		for _, o := range q.Origins {
			if o != OwnedOrigin {
				whereArgs = append(whereArgs, o)
			}
		}
	}
	where += inventoryScopeFilter(q)
	findings, findingArgs := inventoryFindingFilter(q)
	where += findings
	whereArgs = append(whereArgs, findingArgs...)
	where += inventoryFlagFilter(q.Flags, q.Mode)
	// A floor of zero is not a question about zero, so it must not drop the rows whose reach has
	// never been measured.
	if q.MinReach > 0 {
		where += ` AND (` + reach + `) >= ?`
		whereArgs = append(whereArgs, reachArgs...)
		whereArgs = append(whereArgs, q.MinReach)
	}
	return inventoryWhere{clause: where, args: whereArgs}
}

// originClause keeps a node of any origin asked for. Owned is the declaration's answer about the
// node's repository rather than a provenance, so it is its own arm, and seed keeps every starting
// point whether or not it is yours. Every other origin binds one argument, in the order given.
func originClause(origins []string) string {
	arms, provenance := []string{}, 0
	for _, o := range origins {
		if o == OwnedOrigin {
			arms = append(arms, ownedNode+` = 1`)
			continue
		}
		provenance++
	}
	if provenance > 0 {
		arms = append(arms, `COALESCE(r.origin, 'external') IN (`+placeholders(provenance)+`)`)
	}
	return `(` + strings.Join(arms, ` OR `) + `)`
}

// inventoryDeclarationFilter keeps what a line names, at the commits in use, by how it is
// referenced and at which reference. table is a declaredEdgeTable.
//
// Driven from the declarations, so the set is computed once rather than tested per node. Lexical,
// and a different question from which reference was analyzed: a node referenced both by tag and
// by commit matches either.
func inventoryDeclarationFilter(q InventoryQuery, table string) (string, []any) {
	commit := `LENGTH(d.ref_value) = 40 AND d.ref_value GLOB '[0-9a-f]*'`
	var conditions []string
	var args []any
	switch q.RefStyle {
	case "sha":
		conditions = append(conditions, commit)
	case "tag":
		conditions = append(conditions, `d.ref_value IS NOT NULL AND d.ref_value <> '' AND NOT (`+commit+`)`)
	}
	if len(q.At) > 0 {
		conditions = append(conditions, `d.ref_value IN (`+placeholders(len(q.At))+`)`)
		for _, ref := range q.At {
			args = append(args, ref)
		}
	}
	if len(conditions) == 0 {
		return "", nil
	}
	return ` AND n.row_id IN (SELECT d.dst_node_row_id FROM ` + table + ` d
	                           WHERE ` + strings.Join(conditions, " AND ") + `)`, args
}

func inventoryOrdering(
	q InventoryQuery, order string, ok bool, reach string, reachArgs []any,
) (string, []any) {
	// node_key breaks every tie, or two pages of an unstable order repeat a row and skip another.
	direction := " ASC"
	if q.Desc {
		direction = " DESC"
	}
	switch {
	case q.Sort == "findings":
		return severityOrdering(q.Mode, direction, reach, reachArgs)
	case ok:
		return ` ORDER BY ` + order + direction + `, n.node_key`, nil
	}
	return ` ORDER BY dependent_repos DESC, n.node_key`, nil
}

func (s *Store) inventoryPageItems(q InventoryQuery, plan inventoryPageQuery) ([]ActionSummary, error) {
	query := `
		SELECT n.node_key, n.node_kind, COALESCE(n.owner,''), COALESCE(n.repo,''),
		       COALESCE(r.origin, '') AS origin, ` + ownedNode + ` AS owned,
		       ` + plan.reach + ` AS dependent_repos, ` + plan.levels + `,
		       ` + measured("b.refs_in_use") + ` AS distinct_refs,
		       ` + nodeFindingCount(q.Mode, "n.row_id") + ` AS findings,
		       (SELECT COUNT(DISTINCT a.ghsa_id)
		          FROM abom_advisory a
		          JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		         WHERE a.withdrawn_at IS NULL
		           AND n.owner IS NOT NULL AND n.repo IS NOT NULL
		           AND p.package_name = n.owner || '/' || n.repo) AS advisories,
		       EXISTS (SELECT 1 FROM abom_frontier f
		                WHERE f.node_key = n.node_key AND f.status = 'done') AS analyzed
		FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		` + plan.where + plan.ordering

	args := append([]any{}, plan.reachArgs...)
	args = append(args, plan.levelArgs...)
	args = append(args, plan.whereArgs...)
	args = append(args, plan.orderArgs...)
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}

	out, err := queryAll(s, "listing actions", func(r *sql.Rows) (ActionSummary, error) {
		var a ActionSummary
		return a, r.Scan(&a.NodeKey, &a.Kind, &a.Owner, &a.Repo, &a.Origin, &a.Owned,
			&a.DependentRepos, &a.DependentSteps, &a.DependentJobs, &a.DependentWorkflows,
			&a.DependentGroups, &a.DistinctRefs, &a.Findings,
			&a.Advisories, &a.Analyzed)
	}, query, args...)
	if err != nil {
		return nil, err
	}
	if err := s.attachInventoryPageDetails(out, q.Mode); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) attachInventoryPageDetails(out []ActionSummary, mode string) error {
	for i := range out {
		refs, err := s.referencesTo(out[i].NodeKey, mode)
		if err != nil {
			return err
		}
		out[i].Refs = refs
	}
	keys := make([]string, 0, len(out))
	for _, a := range out {
		keys = append(keys, a.NodeKey)
	}
	bands, err := s.severityByNode(mode, keys)
	if err != nil {
		return err
	}
	for i := range out {
		out[i].Severity = bands[out[i].NodeKey]
	}
	return nil
}

func (s *Store) attachInventoryUnknownCounts(q InventoryQuery, page *InventoryPage) error {
	if len(q.Sources) > 0 {
		unknown := q
		unknown.Sources, unknown.unsourced, unknown.Limit, unknown.Offset = nil, true, 1, 0
		counted, err := s.ActionsInventoryPage(unknown)
		if err != nil {
			return err
		}
		page.Unsourced = counted.Total
	}
	if q.Provider != "" {
		unknown := q
		unknown.Provider, unknown.unprovided, unknown.Limit, unknown.Offset = "", true, 1, 0
		counted, err := s.ActionsInventoryPage(unknown)
		if err != nil {
			return err
		}
		page.Unprovided = counted.Total
	}
	return nil
}

// severityOrdering puts what carries a finding first, then what something reaches, then the worst
// band, then reach. Severity outranks reach, and a finding nothing reaches sorts after every one
// something does: its want of a route is the most important fact about it.
//
// Reach is the expression rather than its alias: inside an expression SQLite reads the alias as
// the blast-radius column, which is NULL for most workflows and sorted them apart from zero.
func severityOrdering(mode, direction, reach string, reachArgs []any) (string, []any) {
	worst := `(SELECT MAX(` + bandRankSQL("f.security_severity") + `)
		  FROM abom_finding_node fn JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
		 WHERE fn.node_row_id = n.row_id)`
	return ` ORDER BY (` + worst + ` IS NOT NULL)` + direction +
			`, ((` + reach + `) > 0)` + direction + `, ` + worst + direction +
			`, (` + reach + `)` + direction + `, n.node_key`,
		append(append([]any{}, reachArgs...), reachArgs...)
}

// inventoryElsewhere names the kinds outside this view that the findings terms also matched.
//
// Only the findings terms, and only when the reader did not name the kinds themselves: a reader
// who asked for `step` is already looking there. Silent when there is nothing to disclose, because
// a sentence that always appears is one nobody reads by the time it matters.
func (s *Store) inventoryElsewhere(q InventoryQuery) ([]KindCount, error) {
	if len(q.Kinds) > 0 {
		return nil, nil
	}
	clause, args := inventoryFindingFilter(q)
	if clause == "" {
		return nil, nil
	}
	return queryAll(s, "counting what matched outside the view", func(r *sql.Rows) (KindCount, error) {
		var k KindCount
		return k, r.Scan(&k.Kind, &k.Count)
	}, `
		SELECT n.node_kind, COUNT(*)
		  FROM abom_node_live n
		 WHERE n.node_kind NOT IN ('action', 'composite_action', 'docker_image')`+clause+`
		 GROUP BY n.node_kind
		 ORDER BY 2 DESC, 1`, args...)
}

// inventoryScopeFilter bounds the listing to the view's scope, as every unit and the graph read it.
func inventoryScopeFilter(q InventoryQuery) string {
	switch q.Scope {
	case ScopeExposure:
		return " AND n.node_key IN " + onExposureRoute
	case ScopeOwned:
		return " AND n.row_id IN (" + ownedNodes + ")"
	case ScopeSupplyChain:
		return " AND n.row_id IN (" + scopeNodes(declaredRoots, q.Mode) + ")"
	}
	return ""
}

// inventoryFindingFilter narrows to nodes carrying a finding the predicate names.
//
// One EXISTS for every positive term rather than one each, because the set path ANDs them against
// a single finding row: read as separate questions, `rules=pinning&severity=error` would match a
// node carrying an unrelated error and an unrelated pinning warning, which is not what was asked.
//
// `notRules` removes the node, not the finding. "What is wrong apart from pinning" is not answered
// by a row that still carries a pinning finding.
func inventoryFindingFilter(q InventoryQuery) (string, []any) {
	out, args := "", []any{}
	where, whereArgs := []string{}, []any{}
	if len(q.Rules) > 0 {
		clause, a := ruleClause("f.rule_id", q.Rules)
		where, whereArgs = append(where, clause), append(whereArgs, a...)
	}
	if len(q.Tags) > 0 {
		clause, a := tagClause("f", q.Tags)
		where, whereArgs = append(where, clause), append(whereArgs, a...)
	}
	if q.Provider != "" {
		// Not `IS NOT DISTINCT FROM`: a NULL provider is unknown, and unknown is not a match.
		where, whereArgs = append(where, "f.provider = ?"), append(whereArgs, q.Provider)
	}
	if q.unprovided {
		where = append(where, "COALESCE(f.provider, '') = ''")
	}
	if q.Severity != "" {
		where, whereArgs = append(where, "f.severity = ?"), append(whereArgs, q.Severity)
	}
	if len(q.SecuritySeverities) > 0 {
		clause, a := bandClause("f.security_severity", q.SecuritySeverities)
		where, whereArgs = append(where, clause), append(whereArgs, a...)
	}
	if len(q.Sources) > 0 {
		clause, a := sourceClause("f", q.Mode, q.Sources)
		where, whereArgs = append(where, clause), append(whereArgs, a...)
	}
	if q.unsourced {
		where = append(where, "NOT "+recordedSource("f", q.Mode))
	}
	if len(q.Repositories) > 0 {
		lowered := make([]string, 0, len(q.Repositories))
		for _, name := range q.Repositories {
			lowered = append(lowered, strings.ToLower(name))
		}
		list, names := inList(lowered)
		where = append(where, `EXISTS (SELECT 1 FROM repository fr
		                                WHERE fr.row_id = f.repository_row_id
		                                  AND LOWER(fr.repository_full_name) IN `+list+`)`)
		whereArgs = append(whereArgs, names...)
	}
	if len(where) > 0 {
		out += ` AND EXISTS (SELECT 1 FROM ` + findingSource(q.Mode) + ` f
		                      JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		                     WHERE fn.node_row_id = n.row_id AND ` +
			strings.Join(where, " AND ") + `)`
		args = append(args, whereArgs...)
	}
	if len(q.NotRules) > 0 || len(q.NotTags) > 0 {
		var terms []string
		if len(q.NotRules) > 0 {
			clause, a := ruleClause("x.rule_id", q.NotRules)
			terms, args = append(terms, clause), append(args, a...)
		}
		if len(q.NotTags) > 0 {
			clause, a := tagClause("x", q.NotTags)
			terms, args = append(terms, clause), append(args, a...)
		}
		out += ` AND NOT EXISTS (SELECT 1 FROM ` + findingSource(q.Mode) + ` x
		                          JOIN abom_finding_node xn ON xn.finding_row_id = x.row_id
		                         WHERE xn.node_row_id = n.row_id AND (` + strings.Join(terms, " OR ") + `))`
	}
	return out, args
}

// inventoryFlagFilter narrows to what a row carries.
//
// `unanalyzed` asks about the dependency, not about its findings: a row nothing has queued is a
// coverage gap whether or not a rule ever looked at it.
func inventoryFlagFilter(flags []string, mode string) string {
	out := ""
	for _, f := range flags {
		switch f {
		case "findings":
			out += ` AND EXISTS (SELECT 1 FROM abom_finding_node fn
			                     JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
			                     WHERE fn.node_row_id = n.row_id)`
		case "advisory":
			out += ` AND EXISTS (SELECT 1 FROM abom_advisory a
			                     JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
			                    WHERE a.withdrawn_at IS NULL
			                      AND n.owner IS NOT NULL AND n.repo IS NOT NULL
			                      AND p.package_name = n.owner || '/' || n.repo)`
		case viewspec.FlagUnanalyzed:
			out += ` AND NOT EXISTS (SELECT 1 FROM abom_frontier f
			                         WHERE f.node_key = n.node_key AND f.status = 'done')`
		}
	}
	return out
}

// inventoryTotal counts the match without the per-row subqueries, which the count does not need.
func (s *Store) inventoryTotal(where string, args []any) (int, error) {
	var n int
	err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id
		`+where, args...).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting the inventory: %w", err)
	}
	return n, nil
}

// ActionsInventory lists actions and workflows unpaged. Steps and jobs are implementation detail
// and repositories have their own listing.
func (s *Store) ActionsInventory(search string, limit int, sc RepositoryScope) ([]ActionSummary, error) {
	page, err := s.ActionsInventoryPage(InventoryQuery{Search: search, Limit: limit, Grouping: sc})
	return page.Items, err
}

// RepositorySummary is one repository as the inventory lists it.
type RepositorySummary struct {
	FullName string `json:"fullName"`
	// Origin separates repositories under review from those fetched as dependencies.
	Origin       string `json:"origin"`
	Workflows    int    `json:"workflows"`
	Dependencies int    `json:"dependencies"`
	Findings     int    `json:"findings"`
	// Severity is the same findings by band.
	Severity        SeverityCounts  `json:"severity"`
	Exposures       int             `json:"exposures"`
	DirectExposures int             `json:"directExposures"`
	Properties      []PropertyValue `json:"properties"`
	// CodeScanning is whether its Actions code is scanned, by which setup, and whether a database
	// exists to pull.
	CodeScanning RepositoryCodeScanning `json:"codeScanning"`
}

// RepositoriesQuery narrows and orders the repositories unit, and cuts one page out of the match.
type RepositoriesQuery struct {
	Search string
	// Scope is `owned` or `exposure`, read the way the node listing reads it.
	Scope    string
	Flags    []string
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping RepositoryScope
	// CodeScanning keeps repositories whose Actions code scanning class is one of these.
	CodeScanning []string
}

// RepositoriesPage is one page of repositories and the size of the match it was cut from.
type RepositoriesPage struct {
	Items []RepositorySummary `json:"items"`
	Total int                 `json:"total"`
}

// repositoryScopeFilter narrows the repositories listing the way inventoryScopeFilter narrows the
// node listing: under `owned`, the declared repositories; under `supply-chain`, those and the
// repositories holding what they run.
func repositoryScopeFilter(q RepositoriesQuery) string {
	switch q.Scope {
	case ScopeExposure:
		return ` AND EXISTS (SELECT 1 FROM abom_exposure x
		                      WHERE x.repository_row_id = r.row_id)`
	case ScopeOwned:
		return ` AND r.row_id IN (` + declaredRoots + `)`
	case ScopeSupplyChain:
		return ` AND r.row_id IN (` + scopeRepositories(declaredRoots, AnalysisModeRegular) + `)`
	}
	return ""
}

// repositorySorts is the whitelist, for the reason inventorySorts is one.
var repositorySorts = map[string]string{
	"fullName":        "r.repository_full_name",
	"workflows":       "workflows",
	"dependencies":    "dependencies",
	"findings":        "",
	"exposures":       "exposures",
	"directExposures": "direct_exposures",
	"actionsScanning": setupSortSQL("actions_scanning", []string{"off", "unknown", "advanced", "default", "both"},
		"no-actions-code", "not-owned"),
	"setup": setupSortSQL("setup_kind", []string{"conflict", "none", "unreadable", "not-read", "advanced", "default",
		"unknown"}, "not-owned"),
	"actionsDatabase": setupSortSQL("actions_database", []string{"absent", "not-read", "present"}, "not-owned"),
}

// RepositoriesPage lists one page of repositories under review, ordered across the whole match.
//
// A separate unit rather than a kind in the node listing: reach, references, findings and advisories
// hang off the action node `owner/name`, never off `repo:owner/name`, so a repository in the node
// grid draws six absent measurements as zeroes. Its columns have to be its own.
func (s *Store) RepositoriesPage(q RepositoriesQuery) (RepositoriesPage, error) {
	order, sorted := repositorySorts[q.Sort]
	if q.Sort != "" && !sorted {
		return RepositoriesPage{}, fmt.Errorf(
			"store: cannot sort repositories by %q; they sort by one of %v",
			q.Sort, sortNames(repositorySorts))
	}

	where, args, err := s.repositoriesWhere(q)
	if err != nil {
		return RepositoriesPage{}, err
	}

	var total int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM repository r `+where, args...).Scan(&total); err != nil {
		return RepositoriesPage{}, fmt.Errorf("counting repositories: %w", err)
	}

	ordering := ` ORDER BY findings DESC, r.repository_full_name`
	if sorted {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		if q.Sort == "findings" {
			ordering = ` ORDER BY worst_severity` + direction + `, findings` + direction + `, r.repository_full_name`
		} else {
			// The name breaks every tie, or two pages of an unstable order repeat a row and skip one. A
			// value a column does not measure sorts as NULL, after every value it does either way.
			ordering = ` ORDER BY ` + order + direction + ` NULLS LAST, findings DESC, r.repository_full_name`
		}
	}

	query := `
		SELECT r.repository_full_name, r.origin,
		       (SELECT COUNT(*) FROM abom_node_live n
		         WHERE n.repository_row_id = r.row_id AND n.node_kind = 'workflow') AS workflows,
		       (SELECT COUNT(*) FROM abom_edge_regular e
		         WHERE e.repository_row_id = r.row_id) AS dependencies,
		       (SELECT COUNT(*) FROM abom_finding_regular f
		         WHERE f.repository_row_id = r.row_id) AS findings,
		       COALESCE((SELECT MAX(` + bandRankSQL("f.security_severity") + `) FROM abom_finding_regular f
		         WHERE f.repository_row_id = r.row_id), -1) AS worst_severity,
		       (SELECT COUNT(*) FROM abom_exposure x
		         WHERE x.repository_row_id = r.row_id) AS exposures,
		       (SELECT COUNT(*) FROM abom_exposure x
		         WHERE x.repository_row_id = r.row_id AND x.hops = 1) AS direct_exposures
		FROM repository r
		` + where + ordering
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}

	items, err := queryAll(s, "listing repositories", func(row *sql.Rows) (RepositorySummary, error) {
		r := RepositorySummary{Properties: []PropertyValue{}}
		var worst int
		return r, row.Scan(&r.FullName, &r.Origin, &r.Workflows, &r.Dependencies,
			&r.Findings, &worst, &r.Exposures, &r.DirectExposures)
	}, query, args...)
	if err != nil {
		return RepositoriesPage{}, err
	}
	names := make([]string, 0, len(items))
	for _, r := range items {
		names = append(names, r.FullName)
	}
	bands, err := s.severityByRepository(names)
	if err != nil {
		return RepositoriesPage{}, err
	}
	properties, err := s.propertiesByRepository(names)
	if err != nil {
		return RepositoriesPage{}, err
	}
	scanning, err := s.codeScanningByRepository(names)
	if err != nil {
		return RepositoriesPage{}, err
	}
	for i := range items {
		items[i].Severity = bands[items[i].FullName]
		if values := properties[strings.ToLower(items[i].FullName)]; values != nil {
			items[i].Properties = values
		}
		items[i].CodeScanning = scanning[strings.ToLower(items[i].FullName)]
		if items[i].CodeScanning.Languages == nil {
			items[i].CodeScanning.Languages = []string{}
		}
	}
	return RepositoriesPage{Items: items, Total: total}, nil
}

func (s *Store) propertiesByRepository(names []string) (map[string][]PropertyValue, error) {
	out := map[string][]PropertyValue{}
	if len(names) == 0 {
		return out, nil
	}
	list, args := inList(names)
	// The column is COLLATE NOCASE and inList lowercases, so this matches the lowercased keys below.
	rows, err := s.db.Query(`SELECT repository_full_name, property_name, value
		FROM abom_repository_property WHERE repository_full_name IN `+list+`
		ORDER BY repository_full_name, property_name, value`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading repository properties: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var repository string
		var value PropertyValue
		if err := rows.Scan(&repository, &value.Name, &value.Value); err != nil {
			return nil, fmt.Errorf("scanning a repository property: %w", err)
		}
		key := strings.ToLower(repository)
		out[key] = append(out[key], value)
	}
	return out, rows.Err()
}

// repositoriesWhere is the unit's population as a WHERE clause over `repository r`, shared by the
// page and by a run so the two cannot come to list different repositories.
func (s *Store) repositoriesWhere(q RepositoriesQuery) (string, []any, error) {
	if err := s.prepareScope(q.Scope, AnalysisModeRegular); err != nil {
		return "", nil, err
	}
	// Upstream repositories are not the organization's to remediate, and listing them alongside
	// would imply otherwise.
	where := `WHERE r.origin = ?`
	args := []any{OriginSeed}
	if q.Search != "" {
		where += ` AND r.repository_full_name LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q.Search)+"%")
	}
	bound, bindings, err := s.restrict(q.Grouping, "r.repository_full_name")
	if err != nil {
		return "", nil, err
	}
	where += bound
	args = append(args, bindings...)

	where += repositoryScopeFilter(q)
	if slices.Contains(q.Flags, viewspec.FlagFindings) {
		where += ` AND EXISTS (SELECT 1 FROM abom_finding_regular f WHERE f.repository_row_id = r.row_id)`
	}
	if len(q.CodeScanning) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(q.CodeScanning)), ", ")
		where += ` AND r.row_id IN (SELECT repository_row_id FROM abom_setup_class WHERE class IN (` + marks + `))`
		for _, c := range q.CodeScanning {
			args = append(args, c)
		}
	}
	return where, args, nil
}

// listedRepositories names every repository the unit lists for q, unpaged and in name order.
func (s *Store) listedRepositories(q RepositoriesQuery) ([]string, error) {
	where, args, err := s.repositoriesWhere(q)
	if err != nil {
		return nil, err
	}
	return queryAll(s, "listing repositories", func(row *sql.Rows) (string, error) {
		var name string
		return name, row.Scan(&name)
	}, `SELECT r.repository_full_name FROM repository r `+where+
		` ORDER BY r.repository_full_name`, args...)
}

// sortNames lists a sort whitelist, so a refusal can say what is on offer.
func sortNames(sorts map[string]string) []string {
	out := make([]string, 0, len(sorts))
	for name := range sorts {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// RepositoriesOverview lists repositories under review, most findings first.
//
// Upstream repositories are excluded: they are not the organization's to remediate, and listing
// them alongside would imply otherwise.
func (s *Store) RepositoriesOverview(search string, limit int, sc RepositoryScope) (
	[]RepositorySummary, error) {
	page, err := s.RepositoriesPage(RepositoriesQuery{Search: search, Limit: limit, Grouping: sc})
	return page.Items, err
}

// RuleCount is how often one rule fired within a scope, and where the queries that reported it
// came from. Unsourced counts the findings no evaluation recorded a source for.
type RuleCount struct {
	RuleID    string   `json:"ruleId"`
	Count     int      `json:"count"`
	Sources   []string `json:"sources"`
	Unsourced int      `json:"unsourced"`
	// Severity is the same findings by band. One rule usually lands in one band, and a version
	// that changed its score lands in two.
	Severity SeverityCounts `json:"severity"`
}

// mostSevereFirst orders rules by their worst band, keeping the order they arrived in within one.
func mostSevereFirst(rules []RuleCount) {
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Severity.worstRank() > rules[j].Severity.worstRank()
	})
}

// RepositoryWorkflow is one workflow as a repository's detail page lists it. The workflows *unit*
// has its own row type; this is the short form that hangs off RepositoryDetail.
type RepositoryWorkflow struct {
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
	FindingsByRule []RuleCount          `json:"findingsByRule"`
	Workflows      []RepositoryWorkflow `json:"workflows"`
	Exposures      []Exposure           `json:"exposures"`
}

// RepositoryDetailOf returns one repository's posture.
func (s *Store) RepositoryDetailOf(fullName string) (RepositoryDetail, error) {
	out := RepositoryDetail{FullName: fullName, FindingsByRule: []RuleCount{}}

	var rowID int64
	if err := s.db.QueryRow(
		`SELECT row_id, origin FROM repository WHERE repository_full_name = ?`, fullName).
		Scan(&rowID, &out.Origin); err != nil {
		return out, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}

	rules, err := s.db.Query(`
		SELECT rule_id, COUNT(*), GROUP_CONCAT(sources), COUNT(*) - COUNT(sources), `+
		bandSums("score")+`
		FROM (SELECT f.rule_id, `+findingSourcesIn(AnalysisModeRegular)+` AS sources, f.security_severity AS score
		      FROM abom_finding_regular f WHERE f.repository_row_id = ?)
		GROUP BY rule_id ORDER BY 2 DESC, rule_id`, rowID)
	if err != nil {
		return out, fmt.Errorf("reading findings for %q: %w", fullName, err)
	}
	for rules.Next() {
		var rc RuleCount
		var sources sql.NullString
		if err := rules.Scan(append([]any{&rc.RuleID, &rc.Count, &sources, &rc.Unsourced},
			rc.Severity.targets()...)...); err != nil {
			rules.Close()
			return out, err
		}
		rc.Sources = distinctSources(sources)
		out.FindingsByRule = append(out.FindingsByRule, rc)
	}
	rules.Close()
	if err := rules.Err(); err != nil {
		return out, err
	}
	mostSevereFirst(out.FindingsByRule)

	flows, err := s.db.Query(`
		SELECT n.node_key, COALESCE(n.path,''),
		       (SELECT COUNT(*) FROM abom_edge_regular e
		         JOIN abom_node_live src ON src.row_id = e.src_node_row_id
		        WHERE src.container_node_row_id = n.row_id OR src.row_id = n.row_id),
		       (SELECT COUNT(*) FROM abom_finding_node fn
		         JOIN abom_finding_regular f ON f.row_id = fn.finding_row_id
		         WHERE fn.node_row_id = n.row_id)
		FROM abom_node_live n
		WHERE n.repository_row_id = ? AND n.node_kind IN ('workflow', 'composite_action')
		ORDER BY n.path`, rowID)
	if err != nil {
		return out, fmt.Errorf("reading workflows for %q: %w", fullName, err)
	}
	for flows.Next() {
		var wf RepositoryWorkflow
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

	all, err := s.ExposuresByRepository(0, RepositoryScope{})
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

// RepositoriesFor names every repository the unit lists for q, unpaged and in name order: the
// population a preview of default setup writes covers.
func (s *Store) RepositoriesFor(q RepositoriesQuery) ([]string, error) {
	return s.listedRepositories(q)
}
