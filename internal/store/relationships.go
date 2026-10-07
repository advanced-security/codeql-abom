package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// RelationshipSummary is one declared relationship as a list shows it: what declares it, what it
// points at, and what has been derived about it.
//
// The facts are pointers because each is tri-state. A relationship whose route was never walked is
// not a relationship on no route, and reporting `false` for both would make "nothing here reaches
// an advisory" indistinguishable from "nobody looked".
type RelationshipSummary struct {
	// EdgeKey is the source location that declares the relationship, and the identity a
	// selection or a removal names.
	EdgeKey string `json:"edgeKey"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	// Ref is the reference verbatim, which is what a reader has to act on.
	Ref        string `json:"ref"`
	RefStyle   string `json:"refStyle,omitempty"`
	Boundary   string `json:"boundary,omitempty"`
	Repository string `json:"repository"`
	FilePath   string `json:"filePath"`
	StartLine  int    `json:"startLine"`

	// Findings and Rules are what was reported against the line that declares this, which is the
	// step or the job itself. No unit lists either kind, so this is the only row they reach.
	Findings int      `json:"findings"`
	Rules    []string `json:"rules"`
	// Severity is the same findings by band.
	Severity SeverityCounts `json:"severity"`

	AdvisoryRoute *bool `json:"advisoryRoute"`
	FindingRoute  *bool `json:"findingRoute"`
	OwnedDepth    *int  `json:"ownedDepth"`

	// Locked are the commits current locks select for this line: the repository's own lock
	// through the workflow named, or a caller's through the pin it reached the composite by.
	Locked []RelationshipLock `json:"locked"`

	// Commits are the commits in use that declared this, sorted. A line whose commits declared
	// different things is one row per thing declared. Empty where no run recorded a commit.
	Commits []string `json:"commits"`
}

// RelationshipLock is one commit a current lock selects for one declaring line.
type RelationshipLock struct {
	Commit   string `json:"commit"`
	Consumer string `json:"consumer"`
	Workflow string `json:"workflow,omitempty"`
	Via      string `json:"via,omitempty"`
}

// RelationshipsQuery narrows and orders the relationships unit, and cuts one page out of the match.
type RelationshipsQuery struct {
	Search    string
	EdgeKinds []string
	Boundary  string
	// RefStyle is the term's spelling — `sha`, `tag` or `any` — not the column's.
	RefStyle string
	// Rules and NotRules match what fired on the declaring line, not on either endpoint.
	Rules    []string
	NotRules []string
	// Scope is `owned` or `exposure`, read the way every other unit reads it.
	Scope string
	// Mode narrows to how the database that observed these `uses:` lines was built. Empty is
	// `regular`, which is every edge on record.
	Mode     string
	Sort     string
	Desc     bool
	Offset   int
	Limit    int
	Grouping RepositoryScope
}

// RelationshipsPage is one page of relationships and the size of the match it was cut from.
type RelationshipsPage struct {
	Items []RelationshipSummary `json:"items"`
	// Total counts declarations, and Lines the lines they were declared on.
	Total int `json:"total"`
	Lines int `json:"lines"`
	// WithoutDeclaration counts the lines in scope no commit in use has a recorded declaration
	// for, which are not listed: only a history commit declared them, or the commit in use was
	// read before sightings kept what each commit declared.
	WithoutDeclaration int `json:"withoutDeclaration"`
}

// relationshipSorts is the whitelist, for the reason inventorySorts is one: a column name arriving
// from a query string must never reach the statement.
var relationshipSorts = map[string]string{
	"source":     "src.node_key",
	"target":     "dst.node_key",
	"kind":       "e.edge_kind",
	"ref":        "e.ref_value",
	"refStyle":   "e.ref_style",
	"boundary":   "e.boundary",
	"repository": "r.repository_full_name",
	"ownedDepth": "e.owned_depth",
	"findings":   "findings",
}

// rulesOnLine names them, sorted, so a row reads the same on every page.
func rulesOnLine(mode string) string {
	return `(SELECT GROUP_CONCAT(rule_id) FROM
	                      (SELECT DISTINCT f.rule_id FROM abom_finding_node fn
	                         JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
	                        WHERE fn.node_row_id = e.src_node_row_id
	                        ORDER BY f.rule_id))`
}

// relationshipRuleFilter narrows to the relationships one rule fired on, or away from them.
func relationshipRuleFilter(rules, notRules []string, mode string) (string, []any) {
	clause, args := "", []any{}
	matched := `EXISTS (SELECT 1 FROM abom_finding_node fn
	                     JOIN ` + findingSource(mode) + ` f ON f.row_id = fn.finding_row_id
	                    WHERE fn.node_row_id = e.src_node_row_id AND LOWER(f.rule_id) IN `
	if len(rules) > 0 {
		list, names := inList(rules)
		clause += " AND " + matched + list + ")"
		args = append(args, names...)
	}
	if len(notRules) > 0 {
		list, names := inList(notRules)
		clause += " AND NOT " + matched + list + ")"
		args = append(args, names...)
	}
	return clause, args
}

// relationshipRefFilter narrows by reference style, translating the reader's word into the stored
// one.
//
// The language says `sha` because that is what a reader calls a pinned reference; the derivation
// says `commit` because that is what the reference is. Reading the stored column rather than
// re-deriving the lexical test is the point of storing it: a listing and the graph agree by
// construction instead of by two copies of one regular expression.
func relationshipRefFilter(style string) (string, []any) {
	switch style {
	case RefStyleSHA:
		return " AND e.ref_style = ?", []any{EdgeRefCommit}
	case RefStyleTag:
		return " AND e.ref_style = ?", []any{EdgeRefTag}
	}
	return "", nil
}

// relationshipScopeFilter narrows the listing the way the node listing is narrowed, so one word
// means one thing across units. target says whether the declaration's target may place it there,
// which a line with no declaration on record cannot.
func relationshipScopeFilter(q RelationshipsQuery, target bool) (string, []any) {
	switch q.Scope {
	case ScopeExposure:
		if !target {
			return ` AND src.node_key IN ` + onExposureRoute, nil
		}
		return ` AND (src.node_key IN ` + onExposureRoute +
			` OR dst.node_key IN ` + onExposureRoute + `)`, nil
	case ScopeOwned:
		// A line is yours when it is written in what you hold, whatever it names.
		return ` AND src.row_id IN (` + ownedNodes + `)`, nil
	case ScopeSupplyChain:
		// The line belongs to the scope when what declares it runs there.
		return ` AND src.row_id IN (` + scopeNodes(declaredRoots, q.Mode) + `)`, nil
	}
	return "", nil
}

// relationshipLineWhere is the part of the predicate a line answers by itself, whatever it
// declared: its kind, what fired on it, and the scope and repositories it sits in. Without a
// target, a search is tested against what declares the line, the one end it still has.
func (s *Store) relationshipLineWhere(q RelationshipsQuery, target bool) (string, []any, error) {
	where, args := "WHERE 1 = 1", []any{}
	where += edgeKindFilter(q.EdgeKinds)
	fired, firedArgs := relationshipRuleFilter(q.Rules, q.NotRules, q.Mode)
	where, args = where+fired, append(args, firedArgs...)
	scoped, scopeArgs := relationshipScopeFilter(q, target)
	where, args = where+scoped, append(args, scopeArgs...)
	if !target && q.Search != "" {
		where += ` AND src.node_key LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(q.Search)+"%")
	}
	// The repository grouping, read the same way every other unit reads it.
	bound, bindings, err := s.restrict(q.Grouping, "r.repository_full_name")
	if err != nil {
		return "", nil, err
	}
	return where + bound, append(args, bindings...), nil
}

// relationshipsWhere is the predicate, shared by the count and the page so the total cannot
// describe a different match from the rows.
func (s *Store) relationshipsWhere(q RelationshipsQuery) (string, []any, error) {
	where, args, err := s.relationshipLineWhere(q, true)
	if err != nil {
		return "", nil, err
	}
	if clause := boundaryFilter(q.Boundary); clause != "" {
		where += clause
	}
	style, styleArgs := relationshipRefFilter(q.RefStyle)
	where, args = where+style, append(args, styleArgs...)
	if q.Search != "" {
		// Either end, because a reader searching `checkout` wants the relationships that use it
		// as readily as the ones declared inside it.
		where += ` AND (src.node_key LIKE ? ESCAPE '\' OR dst.node_key LIKE ? ESCAPE '\')`
		like := "%" + escapeLike(q.Search) + "%"
		args = append(args, like, like)
	}
	return where, args, nil
}

// relationshipRows is the one join both statements read: each line as what it declared at the
// commits in use, from declaredEdgeTable. The endpoints are looked up by row id rather than by
// scanning the node table.
func relationshipRows(table string) string {
	return `
	FROM ` + table + ` e
	JOIN abom_node_live src ON src.row_id = e.src_node_row_id
	JOIN abom_node_live dst ON dst.row_id = e.dst_node_row_id
	LEFT JOIN repository r ON r.row_id = e.repository_row_id
	`
}

// relationshipsPageQuery builds the page statement over a declaredEdgeTable, exposed to the cost
// test so the plan can be asserted rather than the clock.
func (s *Store) relationshipsPageQuery(q RelationshipsQuery, table string) (string, []any, error) {
	where, args, err := s.relationshipsWhere(q)
	if err != nil {
		return "", nil, err
	}

	// The identity breaks every tie, or two pages of an unstable order repeat a row and skip
	// another; a line declaring two things is two rows, so its identity is what it declared too.
	identity := `e.edge_key, dst.node_key, e.ref_value`
	ordering := ` ORDER BY src.node_key, dst.node_key, e.file_path, e.start_line, ` + identity
	if column, ok := relationshipSorts[q.Sort]; ok {
		direction := " ASC"
		if q.Desc {
			direction = " DESC"
		}
		ordering = ` ORDER BY ` + column + direction + `, ` + identity
	}

	query := `
		SELECT e.edge_key, e.edge_kind, src.node_key, dst.node_key,
		       COALESCE(e.ref_value, ''), COALESCE(e.ref_style, ''), COALESCE(e.boundary, ''),
		       COALESCE(r.repository_full_name, ''), COALESCE(e.file_path, ''),
		       COALESCE(e.start_line, 0), ` + nodeFindingCount(q.Mode, "e.src_node_row_id") +
		` AS findings, ` + rulesOnLine(q.Mode) + `,
		       e.advisory_route, e.finding_route, e.owned_depth, COALESCE(e.commits, '')` +
		relationshipRows(table) + where + ordering
	if q.Limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}
	return query, args, nil
}

// relationshipsCount sizes the match: the declarations, the lines they sit on, and the lines in
// scope that are not listed because no commit in use has a recorded declaration for them.
func (s *Store) relationshipsCount(q RelationshipsQuery, table string) (RelationshipsPage, error) {
	where, args, err := s.relationshipsWhere(q)
	if err != nil {
		return RelationshipsPage{}, err
	}
	lines, lineArgs, err := s.relationshipLineWhere(q, false)
	if err != nil {
		return RelationshipsPage{}, err
	}
	var out RelationshipsPage
	err = s.DB().QueryRow(`
		SELECT COUNT(*), COUNT(DISTINCT e.row_id),
		       (SELECT COUNT(*) FROM `+edgeSource(q.Mode)+` e
		          JOIN abom_node_live src ON src.row_id = e.src_node_row_id
		          LEFT JOIN repository r ON r.row_id = e.repository_row_id
		        `+lines+` AND e.row_id NOT IN (SELECT row_id FROM `+table+`))`+
		relationshipRows(table)+where,
		append(lineArgs, args...)...).Scan(
		&out.Total, &out.Lines, &out.WithoutDeclaration)
	if err != nil {
		return RelationshipsPage{}, fmt.Errorf("counting relationships: %w", err)
	}
	return out, nil
}

// RelationshipsPage lists one page of declared relationships, ordered across the whole match.
//
// A unit of its own because a relationship is not a node: its identity is a source location, its
// facts are about a route rather than about either end, and a grid of nodes has nowhere to put
// either. Containment is absent by construction — it is drawn rather than declared, so there is no
// row here for it to occupy and no total for it to inflate.
//
// A row is what a line declared at the commits in use, never what the row holds: that is the last
// commit analyzed, which may be one nothing runs any more.
func (s *Store) RelationshipsPage(q RelationshipsQuery) (RelationshipsPage, error) {
	if q.Sort != "" {
		if _, ok := relationshipSorts[q.Sort]; !ok {
			return RelationshipsPage{}, fmt.Errorf(
				"store: cannot sort relationships by %q; they sort by one of %v",
				q.Sort, sortNames(relationshipSorts))
		}
	}
	table, err := s.declaredEdgeTable(q.Mode)
	if err != nil {
		return RelationshipsPage{}, err
	}
	page, err := s.relationshipsCount(q, table)
	if err != nil {
		return RelationshipsPage{}, err
	}

	query, pageArgs, err := s.relationshipsPageQuery(q, table)
	if err != nil {
		return RelationshipsPage{}, err
	}

	items, err := queryAll(s, "listing relationships",
		func(row *sql.Rows) (RelationshipSummary, error) {
			var r RelationshipSummary
			var rules sql.NullString
			var commits string
			if err := row.Scan(&r.EdgeKey, &r.Kind, &r.Source, &r.Target, &r.Ref, &r.RefStyle,
				&r.Boundary, &r.Repository, &r.FilePath, &r.StartLine, &r.Findings, &rules,
				&r.AdvisoryRoute, &r.FindingRoute, &r.OwnedDepth, &commits); err != nil {
				return r, err
			}
			// An empty slice rather than null: the interface reads it as a set, and null is not one.
			r.Rules = []string{}
			if rules.String != "" {
				r.Rules = strings.Split(rules.String, ",")
			}
			r.Commits = declaredCommits(commits)
			return r, nil
		}, query, pageArgs...)
	if err != nil {
		return RelationshipsPage{}, err
	}
	sources := make([]string, 0, len(items))
	for _, r := range items {
		sources = append(sources, r.Source)
	}
	bands, err := s.severityByNode(q.Mode, sources)
	if err != nil {
		return RelationshipsPage{}, err
	}
	for i := range items {
		items[i].Severity = bands[items[i].Source]
	}
	if err := s.attachRelationshipLocks(items); err != nil {
		return RelationshipsPage{}, err
	}
	page.Items = items
	return page, nil
}
