package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
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
func (s *Store) SearchNodes(search string, limit int, sc RepositoryScope) ([]NodeMatch, error) {
	reach, reachArgs, err := s.reachWithin(sc)
	if err != nil {
		return nil, err
	}
	keep, keepArgs, err := s.reachedByScope(sc)
	if err != nil {
		return nil, err
	}
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
		"bounded to a grouping; run expand-all to refresh the aggregates")

// reachedByScope filters rows to the nodes the scope's repositories reach.
//
// Driven from the pair table rather than tested per node: the planner turns this into a covering
// index scan feeding a primary key lookup, where the same condition written as a correlated
// subquery makes it scan every node and evaluate the subquery for each.
func (s *Store) reachedByScope(sc RepositoryScope) (string, []any, error) {
	if !sc.asked() {
		return "", nil, nil
	}
	if err := s.reachIsDerived(); err != nil {
		return "", nil, err
	}
	bound, args, err := s.restrict(sc, "rr.repository_full_name")
	if err != nil {
		return "", nil, err
	}
	return ` AND n.row_id IN (SELECT nr.node_row_id FROM abom_node_reach nr
		          JOIN repository rr ON rr.row_id = nr.repository_row_id
		         WHERE 1` + bound + `)`, args, nil
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

// reachWithin returns the expression counting how many repositories reach the node in scope, and
// the arguments binding it.
//
// Unbounded it is the stored rollup. Bounded it counts the breakdown behind that rollup, because a
// count carried over from the organization under a filtered row list is the defect this exists to
// prevent. The two agree by construction: one walk writes both.
func (s *Store) reachWithin(sc RepositoryScope) (string, []any, error) {
	if !sc.asked() {
		return `COALESCE(b.dependent_repos, 0)`, nil, nil
	}
	bound, args, err := s.restrict(sc, "rr.repository_full_name")
	if err != nil {
		return "", nil, err
	}
	return `(SELECT COUNT(*) FROM abom_node_reach nr
		          JOIN repository rr ON rr.row_id = nr.repository_row_id
		         WHERE nr.node_row_id = n.row_id` + bound + `)`, args, nil
}

// directRefsWithin counts the references pointing at the node from the scope.
//
// Unbounded this is the stored count, which includes references made by upstream repositories —
// they are how a composite action's own dependencies are attributed. Bounded, only references
// written in the grouping's own files count, because those are the ones it can change.
func (s *Store) directRefsWithin(sc RepositoryScope, mode string) (string, []any, error) {
	if !sc.asked() {
		return `COALESCE(b.direct_dependents, 0)`, nil, nil
	}
	bound, args, err := s.restrict(sc, "er.repository_full_name")
	if err != nil {
		return "", nil, err
	}
	return `(SELECT COUNT(*) FROM ` + edgeSource(mode) + ` e
		          JOIN repository er ON er.row_id = e.repository_row_id
		         WHERE e.dst_node_row_id = n.row_id` + bound + `)`, args, nil
}

// InventoryQuery narrows and orders the inventory, and cuts one page out of what matched.
type InventoryQuery struct {
	Search   string
	Kinds    []string
	Origins  []string
	MinReach int
	// Scope is the view's own scope term, `owned` or `exposure`. Closure carries the declaration
	// the owned scope reads, because `internal/graph` derives it and this package cannot import it.
	Scope    string
	Closure  []string
	Flags    []string
	RefStyle string
	// The findings terms, carrying the meanings the set path gives them: a node matches when it
	// carries a finding that satisfies all of them at once. `repositories` is the repository the
	// *finding* sits in, as it is there — "which dependencies does this repository use" is `scope`.
	Rules        []string
	NotRules     []string
	Provider     string
	Severity     string
	Repositories []string
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
}

// InventoryPage is one page of the inventory and the size of the match it was cut from.
type InventoryPage struct {
	Items []ActionSummary `json:"items"`
	// Total is how many rows the predicate matched, before the page. A page that does not carry
	// it reads as the whole answer: the interface paged within a fetch cap of 500 against a match
	// of 1,585, and sorting that page by findings named a row with 39 while the real answer had 50.
	Total int `json:"total"`
	// Elsewhere names the kinds outside this view that the findings terms also matched.
	//
	// Without it, a findings question answers 0 and the zero reads as "nothing is wrong". On the
	// crawled organization every one of the 277 `actions/unpinned-tag` findings sits on a `step`
	// or `job` node, so the most important rule there is answers 0 among the dependency kinds.
	Elsewhere []KindCount `json:"elsewhere,omitempty"`
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
	"nodeKey":          "n.node_key",
	"kind":             "n.node_kind",
	"origin":           "origin",
	"dependentRepos":   "dependent_repos",
	"directDependents": "direct_dependents",
	"distinctRefs":     "distinct_refs",
	"findings":         "findings",
	"advisories":       "advisories",
	"analyzed":         "analyzed",
}

// InventorySortNames lists what may be sorted on, so the interface offers exactly what is applied.
func InventorySortNames() []string { return sortNames(inventorySorts) }

// ActionsInventoryPage lists one page of actions and workflows, ordered across the whole match.
//
// Ordering and cutting both happen here because they cannot be separated: a page taken by reach
// and then sorted by findings in the browser answers "the most findings among the widest-reaching
// rows", which is a different question from the one the column header asks.
func (s *Store) ActionsInventoryPage(q InventoryQuery) (InventoryPage, error) {
	order, ok := inventorySorts[q.Sort]
	if q.Sort != "" && !ok {
		return InventoryPage{}, fmt.Errorf(
			"store: cannot sort the inventory by %q; it sorts by one of %v",
			q.Sort, InventorySortNames())
	}

	reach, reachArgs, err := s.reachWithin(q.Grouping)
	if err != nil {
		return InventoryPage{}, err
	}
	direct, directArgs, err := s.directRefsWithin(q.Grouping, q.Mode)
	if err != nil {
		return InventoryPage{}, err
	}
	keep, keepArgs, err := s.reachedByScope(q.Grouping)
	if err != nil {
		return InventoryPage{}, err
	}

	// The three kinds whose columns this listing measures: reach, references and advisories are all
	// about being depended upon. Workflows and jobs are consumers and have their own units, because
	// listing them here drew three inapplicable measurements as zero for 71% of the rows. Not a
	// cage: naming a kind still reaches past these three.
	where := `WHERE n.node_kind IN ('action', 'composite_action', 'docker_image')`
	if len(q.Kinds) > 0 {
		where = `WHERE n.node_kind IN (` + placeholders(len(q.Kinds)) + `)`
	}
	where += keep + nodeModeFilter(q.Mode)
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
		where += ` AND COALESCE(r.origin, 'external') IN (` + placeholders(len(q.Origins)) + `)`
		for _, o := range q.Origins {
			whereArgs = append(whereArgs, o)
		}
	}
	scope, scopeArgs := inventoryScopeFilter(q)
	where += scope
	whereArgs = append(whereArgs, scopeArgs...)
	findings, findingArgs := inventoryFindingFilter(q)
	where += findings
	whereArgs = append(whereArgs, findingArgs...)
	where += inventoryFlagFilter(q.Flags, q.Mode)
	if style := inventoryRefFilter(q.RefStyle, q.Mode); style != "" {
		where += style
	}
	// A floor of zero is not a question about zero, so it must not drop the rows whose reach has
	// never been measured.
	if q.MinReach > 0 {
		where += ` AND (` + reach + `) >= ?`
		whereArgs = append(whereArgs, reachArgs...)
		whereArgs = append(whereArgs, q.MinReach)
	}

	total, err := s.inventoryTotal(where, whereArgs)
	if err != nil {
		return InventoryPage{}, err
	}

	// node_key breaks every tie, or two pages of an unstable order repeat a row and skip another.
	direction := " ASC"
	if q.Desc {
		direction = " DESC"
	}
	ordering := ` ORDER BY dependent_repos DESC, n.node_key`
	if ok {
		ordering = ` ORDER BY ` + order + direction + `, n.node_key`
	}

	query := `
		SELECT n.node_key, n.node_kind, COALESCE(n.owner,''), COALESCE(n.repo,''),
		       COALESCE(r.origin, '') AS origin,
		       ` + reach + ` AS dependent_repos, ` + direct + ` AS direct_dependents,
		       (SELECT COUNT(DISTINCT e.ref_value) FROM ` + edgeSource(q.Mode) + ` e
		         WHERE e.dst_node_row_id = n.row_id
		           AND e.ref_value IS NOT NULL AND e.ref_value <> '') AS distinct_refs,
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
		` + where + ordering

	args := append([]any{}, reachArgs...)
	args = append(args, directArgs...)
	args = append(args, whereArgs...)
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}

	out, err := queryAll(s, "listing actions", func(r *sql.Rows) (ActionSummary, error) {
		var a ActionSummary
		return a, r.Scan(&a.NodeKey, &a.Kind, &a.Owner, &a.Repo, &a.Origin,
			&a.DependentRepos, &a.DirectDependents, &a.DistinctRefs, &a.Findings,
			&a.Advisories, &a.Analyzed)
	}, query, args...)
	if err != nil {
		return InventoryPage{}, err
	}

	for i := range out {
		refs, err := s.referencesTo(out[i].NodeKey, q.Mode)
		if err != nil {
			return InventoryPage{}, err
		}
		out[i].Refs = refs
	}

	elsewhere, err := s.inventoryElsewhere(q)
	if err != nil {
		return InventoryPage{}, err
	}
	return InventoryPage{Items: out, Total: total, Elsewhere: elsewhere}, nil
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
		 WHERE n.node_kind NOT IN ('action', 'composite_action', 'workflow',
		                           'reusable_workflow', 'docker_image')`+clause+`
		 GROUP BY n.node_kind
		 ORDER BY 2 DESC, 1`, args...)
}

// inventoryScopeFilter bounds the listing to the view's scope.
//
// The owned scope reads the declaration's closure. Declaring nothing shows nothing rather than
// falling through to the whole corpus, which would answer a question about the operator's own
// supply chain with somebody else's — the same rule the graph follows.
func inventoryScopeFilter(q InventoryQuery) (string, []any) {
	switch q.Scope {
	case ScopeExposure:
		return " AND n.node_key IN " + onExposureRoute, nil
	case ScopeOwned:
		if len(q.Closure) == 0 {
			return " AND 0", nil
		}
		list, names := inList(q.Closure)
		return ` AND (LOWER(r.repository_full_name) IN ` + list +
				` OR LOWER(n.owner || '/' || n.repo) IN ` + list + `)`,
			append(append([]any{}, names...), names...)
	}
	return "", nil
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
	if q.Provider != "" {
		// Not `IS NOT DISTINCT FROM`: a NULL provider is unknown, and unknown is not a match.
		where, whereArgs = append(where, "f.provider = ?"), append(whereArgs, q.Provider)
	}
	if q.Severity != "" {
		where, whereArgs = append(where, "f.severity = ?"), append(whereArgs, q.Severity)
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
	if len(q.NotRules) > 0 {
		clause, a := ruleClause("x.rule_id", q.NotRules)
		out += ` AND NOT EXISTS (SELECT 1 FROM ` + findingSource(q.Mode) + ` x
		                          JOIN abom_finding_node xn ON xn.finding_row_id = x.row_id
		                         WHERE xn.node_row_id = n.row_id AND ` + clause + `)`
		args = append(args, a...)
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

// inventoryRefFilter selects by how something is referenced.
//
// Lexical only, and a different question from which reference was analyzed: a node referenced both
// by tag and by commit matches either.
func inventoryRefFilter(style, mode string) string {
	commit := `LENGTH(e.ref_value) = 40 AND e.ref_value GLOB '[0-9a-f]*'`
	switch style {
	case "sha":
		return ` AND EXISTS (SELECT 1 FROM ` + edgeSource(mode) + ` e
		                     WHERE e.dst_node_row_id = n.row_id AND ` + commit + `)`
	case "tag":
		return ` AND EXISTS (SELECT 1 FROM ` + edgeSource(mode) + ` e
		                     WHERE e.dst_node_row_id = n.row_id
		                       AND e.ref_value IS NOT NULL AND e.ref_value <> ''
		                       AND NOT (` + commit + `))`
	}
	return ""
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
	Origin          string `json:"origin"`
	Workflows       int    `json:"workflows"`
	Dependencies    int    `json:"dependencies"`
	Findings        int    `json:"findings"`
	Exposures       int    `json:"exposures"`
	DirectExposures int    `json:"directExposures"`
}

// RepositoriesQuery narrows and orders the repositories unit, and cuts one page out of the match.
type RepositoriesQuery struct {
	Search string
	// Scope is `owned` or `exposure`, read against Closure the same way the node listing reads it.
	Scope    string
	Closure  []string
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping RepositoryScope
}

// RepositoriesPage is one page of repositories and the size of the match it was cut from.
type RepositoriesPage struct {
	Items []RepositorySummary `json:"items"`
	Total int                 `json:"total"`
}

// repositoryScopeFilter narrows the repositories listing the way inventoryScopeFilter narrows the
// node listing, so the same term means the same thing in both units.
//
// `owned` with nothing declared matches nothing rather than everything: falling through would
// answer a question about the operator's own estate with somebody else's.
func repositoryScopeFilter(q RepositoriesQuery) (string, []any) {
	switch q.Scope {
	case ScopeExposure:
		return ` AND EXISTS (SELECT 1 FROM abom_exposure x
		                      WHERE x.repository_row_id = r.row_id)`, nil
	case ScopeOwned:
		if len(q.Closure) == 0 {
			return " AND 0", nil
		}
		list, names := inList(q.Closure)
		return ` AND LOWER(r.repository_full_name) IN ` + list, names
	}
	return "", nil
}

// repositorySorts is the whitelist, for the reason inventorySorts is one.
var repositorySorts = map[string]string{
	"fullName":        "r.repository_full_name",
	"workflows":       "workflows",
	"dependencies":    "dependencies",
	"findings":        "findings",
	"exposures":       "exposures",
	"directExposures": "direct_exposures",
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
		return RepositoriesPage{}, err
	}
	where += bound
	args = append(args, bindings...)

	scoped, scopeArgs := repositoryScopeFilter(q)
	where += scoped
	args = append(args, scopeArgs...)

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
		// The name breaks every tie, or two pages of an unstable order repeat a row and skip one.
		ordering = ` ORDER BY ` + order + direction + `, r.repository_full_name`
	}

	query := `
		SELECT r.repository_full_name, r.origin,
		       (SELECT COUNT(*) FROM abom_node_live n
		         WHERE n.repository_row_id = r.row_id AND n.node_kind = 'workflow') AS workflows,
		       (SELECT COUNT(*) FROM abom_edge_regular e
		         WHERE e.repository_row_id = r.row_id) AS dependencies,
		       (SELECT COUNT(*) FROM abom_finding_regular f
		         WHERE f.repository_row_id = r.row_id) AS findings,
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
		var r RepositorySummary
		return r, row.Scan(&r.FullName, &r.Origin, &r.Workflows, &r.Dependencies,
			&r.Findings, &r.Exposures, &r.DirectExposures)
	}, query, args...)
	if err != nil {
		return RepositoriesPage{}, err
	}
	return RepositoriesPage{Items: items, Total: total}, nil
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

// RuleCount is how often one rule fired within a scope.
type RuleCount struct {
	RuleID string `json:"ruleId"`
	Count  int    `json:"count"`
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
	out := RepositoryDetail{FullName: fullName}

	var rowID int64
	if err := s.db.QueryRow(
		`SELECT row_id, origin FROM repository WHERE repository_full_name = ?`, fullName).
		Scan(&rowID, &out.Origin); err != nil {
		return out, fmt.Errorf("resolving repository %q: %w", fullName, err)
	}

	rules, err := s.db.Query(`
		SELECT rule_id, COUNT(*) FROM abom_finding_regular
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
