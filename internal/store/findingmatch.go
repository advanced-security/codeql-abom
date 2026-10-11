package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// FindingSelector names a set of findings, and through them the nodes carrying them.
//
// This answers a set, never a graph: finding predicates do not imply the edge closure among the
// matching nodes.
type FindingSelector struct {
	// Rules to include, by exact query id or by a `prefix/*` pattern. A rule id *is* a CodeQL
	// query id, and query ids are hierarchical, so a pack has to be nameable without listing it.
	Rules []string
	// Exclude drops a node that carries any of these at all, rather than dropping the finding and
	// keeping the node. "A but not C" describes the action or workflow, not the result set.
	Exclude []string
	// Tags match a finding whose rule declared any of them in the SARIF that reported it, exactly:
	// a CWE report cannot let cwe-82 answer for cwe-829.
	Tags []string
	// NotTags drops a node carrying any finding whose rule declared one of them, as Exclude does.
	NotTags []string
	// Provider is the SARIF tool driver name, verbatim. It never matches a finding nobody
	// attributed: relabeling those would invent the provenance the column exists to hold.
	Provider string
	Severity string
	// SecuritySeverities are bands of the rule's own score; a finding in any of them matches.
	SecuritySeverities []string
	// Sources narrows to findings whose rule's query came from one of these, as an evaluation that
	// reported the finding recorded it. A finding none recorded a source for matches none.
	Sources []string
	// Repositories narrows to what a gate is asking about. Compared case-insensitively because
	// GitHub account names are case-insensitive.
	Repositories []string
	// Properties narrows by what the organization says a repository is. Several keys are an AND and
	// several values of one key are an OR, which is a plain join because the store holds one row
	// per value. Matched against the repository the finding was *found* in, the same column the
	// repository filter uses: a node inside an external composite action belongs to no repository
	// and so to no grouping, and must not be swept in by one.
	Properties []viewspec.PropertyMatch
	// ExcludeProperties drops a repository matching any of them, and beats Properties, so a scope
	// cannot be both asked for and refused and quietly resolve to asked for.
	ExcludeProperties []viewspec.PropertyMatch
	// Mode narrows to how the database that reported a finding was built. Empty is `regular`,
	// which is what every gate on record already evaluates, so an existing policy keeps refusing
	// exactly what it refused.
	Mode string
	// AdvisorySeverities asks about live advisories rather than findings, at the severities GitHub
	// published them.
	AdvisorySeverities []string
	// Reached charges a match to the seed repositories reaching it, directly or transitively,
	// rather than to where a finding was found. `repositories` and the property terms then narrow
	// the reaching repository, which is the one a gate is asking about.
	Reached []string
}

// MatchedNode is one node the selector chose, carrying enough to explain why it was chosen.
type MatchedNode struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Repositories are where the findings were found, which is not always where the node lives.
	// Every finding names a repository; not every node does.
	Repositories []string `json:"repositories"`
	Findings     int      `json:"findings"`
	Rules        []string `json:"rules"`
	// Advisories are the live advisories naming the node, when the predicate asked about advisories.
	Advisories []string `json:"advisories,omitempty"`
}

// FindingMatch is the whole answer, nodes and total together.
//
// The total is here rather than left to the caller because a finding can sit on more than one node,
// such as a step and the workflow holding it.
type FindingMatch struct {
	Nodes []MatchedNode `json:"nodes"`
	// Findings counts each finding once however many nodes carry it.
	Findings int `json:"findings"`
	// Unsourced counts the findings every other term matched whose source no evaluation recorded.
	// Counted only when a source is asked, which is the only time they are left out.
	Unsourced int `json:"unsourced"`
	// Unprovided counts the findings every other term matched that recorded no provider. Counted
	// only when a provider is asked, for the same reason.
	Unprovided int `json:"unprovided"`
	// Population is what was counted: findings, or live advisories.
	Population string `json:"population"`
	// Advisories counts each matched advisory once, when the population is advisories.
	Advisories int `json:"advisories"`

	// What was matched, by identity, so a superset can count its members' union once each.
	findingIDs  []int64
	advisoryIDs []string
}

// What a predicate asks about. One at a time: see viewspec.Spec.SetConflict.
const (
	PopulationFindings   = "findings"
	PopulationAdvisories = "advisories"
)

// asked reports whether the selector names anything at all.
func (f FindingSelector) asked() bool {
	return len(f.Rules) > 0 || len(f.Exclude) > 0 || len(f.Tags) > 0 || len(f.NotTags) > 0 ||
		f.Provider != "" || f.Severity != "" ||
		len(f.SecuritySeverities) > 0 ||
		len(f.Sources) > 0 || len(f.Properties) > 0 || len(f.ExcludeProperties) > 0 || f.Mode != "" ||
		len(f.AdvisorySeverities) > 0 || len(f.Reached) > 0
}

// asksFindings reports whether the selector names anything only a finding has.
func (f FindingSelector) asksFindings() bool {
	return len(f.Rules) > 0 || len(f.Exclude) > 0 || len(f.Tags) > 0 || len(f.NotTags) > 0 ||
		f.Provider != "" || f.Severity != "" ||
		len(f.SecuritySeverities) > 0 || len(f.Sources) > 0 || f.Mode != ""
}

// propertyClause tests one key against every value asked for it, against the repository named by
// `column`. Values of one key are an OR; the caller ANDs the clauses of different keys.
func propertyClause(column string, name string, values []string) (string, []any) {
	list, args := inList(values)
	return `EXISTS (SELECT 1 FROM abom_repository_property p
	                 WHERE p.repository_full_name = ` + column + ` COLLATE NOCASE
	                   AND p.property_name = ?
	                   AND p.value IN ` + list + `)`, append([]any{name}, args...)
}

// valuesByKey groups matches by key, in a stable order so two runs produce one query.
func valuesByKey(matches []viewspec.PropertyMatch) ([]string, map[string][]string) {
	by := map[string][]string{}
	names := []string{}
	for _, m := range matches {
		if _, seen := by[m.Name]; !seen {
			names = append(names, m.Name)
		}
		by[m.Name] = append(by[m.Name], m.Value)
	}
	sort.Strings(names)
	return names, by
}

// ruleClause turns include patterns into SQL. A trailing `*` is a prefix; anything else is exact.
func ruleClause(column string, patterns []string) (string, []any) {
	terms := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns))
	for _, p := range patterns {
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			terms = append(terms, column+" LIKE ? ESCAPE '\\'")
			args = append(args, escapeLike(prefix)+"%")
			continue
		}
		terms = append(terms, column+" = ?")
		args = append(args, p)
	}
	return "(" + strings.Join(terms, " OR ") + ")", args
}

// NodesMatching answers which nodes carry findings the selector names.
//
// An empty selector matches nothing rather than everything: a gate whose policy failed to parse
// would otherwise fail every pull request instead of none, which is the loudest possible way to be
// wrong about the safest possible default.
func (s *Store) NodesMatching(sel FindingSelector) (FindingMatch, error) {
	if len(sel.AdvisorySeverities) > 0 {
		return s.advisoriesMatching(sel)
	}
	out := FindingMatch{Nodes: []MatchedNode{}, Population: PopulationFindings}
	if !sel.asked() {
		return out, nil
	}

	query := buildFindingMatchQuery(sel)
	if err := s.attachMatchedNodes(&out, query); err != nil {
		return out, err
	}

	// Listed rather than summed, because one finding can sit on two nodes.
	var err error
	if out.findingIDs, err = s.matchedFindingIDs(query.from, query.where, query.exclusion, query.args); err != nil {
		return out, err
	}
	out.Findings = len(out.findingIDs)
	if len(sel.Sources) > 0 {
		if out.Unsourced, err = s.countMatched(
			query.from, query.unsourced, query.exclusion, query.unsourcedArgs); err != nil {
			return out, err
		}
	}
	if sel.Provider != "" {
		if out.Unprovided, err = s.countMatched(
			query.from, query.unprovided, query.exclusion, query.unprovidedArgs); err != nil {
			return out, err
		}
	}
	return out, nil
}

type findingMatchQuery struct {
	repository     string
	from           string
	where          []string
	args           []any
	unprovided     []string
	unprovidedArgs []any
	unsourced      []string
	unsourcedArgs  []any
	exclusion      string
	exclusionArgs  []any
}

func buildFindingMatchQuery(sel FindingSelector) findingMatchQuery {
	query := newFindingMatchQuery(sel)
	query.applyRules(sel.Rules)
	query.applyTags(sel.Tags)
	query.applyProvider(sel.Provider)
	query.applySeverity(sel.Severity)
	query.applySecuritySeverities(sel.SecuritySeverities)
	query.applyRepositories(sel.Repositories)
	query.applyProperties(sel.Properties)
	query.applyExcludedProperties(sel.ExcludeProperties)
	query.applyExclusions(sel)
	query.prepareUnsourced(sel.Mode)
	query.applySources(sel.Mode, sel.Sources)
	query.appendExclusionArgs()
	return query
}

func newFindingMatchQuery(sel FindingSelector) findingMatchQuery {
	repository, reach := "fr.repository_full_name", ""
	if len(sel.Reached) > 0 {
		repository = "rr.repository_full_name"
		reach = `
		JOIN abom_node_reach nr ON nr.node_row_id IN (n.row_id, n.container_node_row_id)`
		if hops := hopsCondition("nr.hops", sel.Reached); hops != "" {
			reach += " AND " + hops
		}
		reach += `
		JOIN repository rr ON rr.row_id = nr.repository_row_id`
	}
	return findingMatchQuery{
		repository: repository,
		from: findingSource(sel.Mode) + ` f
		JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		JOIN abom_node_live n ON n.row_id = fn.node_row_id
		LEFT JOIN repository fr ON fr.row_id = f.repository_row_id` + reach,
		where:      []string{"true"},
		unprovided: []string{"true", "COALESCE(f.provider, '') = ''"},
	}
}

func (q *findingMatchQuery) both(clause string, args ...any) {
	q.where, q.args = append(q.where, clause), append(q.args, args...)
	q.unprovided = append(q.unprovided, clause)
	q.unprovidedArgs = append(q.unprovidedArgs, args...)
}

func (q *findingMatchQuery) applyRules(rules []string) {
	if len(rules) > 0 {
		clause, args := ruleClause("f.rule_id", rules)
		q.both(clause, args...)
	}
}

func (q *findingMatchQuery) applyTags(tags []string) {
	if len(tags) > 0 {
		clause, args := tagClause("f", tags)
		q.both(clause, args...)
	}
}

func (q *findingMatchQuery) applyProvider(provider string) {
	if provider != "" {
		// Not `IS NOT DISTINCT FROM`: a NULL provider is unknown, and unknown is not a match.
		q.where = append(q.where, "f.provider = ?")
		q.args = append(q.args, provider)
	}
}

func (q *findingMatchQuery) applySeverity(severity string) {
	if severity != "" {
		q.both("f.severity = ?", severity)
	}
}

func (q *findingMatchQuery) applySecuritySeverities(severities []string) {
	if len(severities) > 0 {
		clause, args := bandClause("f.security_severity", severities)
		q.both(clause, args...)
	}
}

func (q *findingMatchQuery) applyRepositories(repositories []string) {
	if len(repositories) == 0 {
		return
	}
	lowered := make([]string, 0, len(repositories))
	for _, name := range repositories {
		lowered = append(lowered, strings.ToLower(name))
	}
	list, names := inList(lowered)
	q.both("LOWER("+q.repository+") IN "+list, names...)
}

func (q *findingMatchQuery) applyProperties(properties []viewspec.PropertyMatch) {
	names, by := valuesByKey(properties)
	for _, name := range names {
		clause, args := propertyClause(q.repository, name, by[name])
		q.both(clause, args...)
	}
}

func (q *findingMatchQuery) applyExcludedProperties(properties []viewspec.PropertyMatch) {
	names, by := valuesByKey(properties)
	for _, name := range names {
		clause, args := propertyClause(q.repository, name, by[name])
		q.both("NOT "+clause, args...)
	}
}

func (q *findingMatchQuery) applyExclusions(sel FindingSelector) {
	var terms []string
	var args []any
	if len(sel.Exclude) > 0 {
		clause, a := ruleClause("x.rule_id", sel.Exclude)
		terms, args = append(terms, clause), append(args, a...)
	}
	if len(sel.NotTags) > 0 {
		clause, a := tagClause("x", sel.NotTags)
		terms, args = append(terms, clause), append(args, a...)
	}
	if len(terms) == 0 {
		return
	}
	q.exclusion = `
		  AND NOT EXISTS (SELECT 1 FROM ` + findingSource(sel.Mode) + ` x
		                    JOIN abom_finding_node xn ON xn.finding_row_id = x.row_id
		                   WHERE xn.node_row_id = n.row_id AND (` + strings.Join(terms, " OR ") + `))`
	q.exclusionArgs = args
}

func (q *findingMatchQuery) prepareUnsourced(mode string) {
	// The rest of the question, kept apart so the findings no source was recorded for are counted
	// against exactly what the source term narrowed.
	q.unsourced = append(append([]string{}, q.where...), "NOT "+recordedSource("f", mode))
	q.unsourcedArgs = append(append([]any{}, q.args...), q.exclusionArgs...)
}

func (q *findingMatchQuery) applySources(mode string, sources []string) {
	if len(sources) > 0 {
		clause, args := sourceClause("f", mode, sources)
		q.where = append(q.where, clause)
		q.args = append(q.args, args...)
		q.unprovided = append(q.unprovided, clause)
		q.unprovidedArgs = append(q.unprovidedArgs, args...)
	}
}

func (q *findingMatchQuery) appendExclusionArgs() {
	q.args = append(q.args, q.exclusionArgs...)
	q.unprovidedArgs = append(q.unprovidedArgs, q.exclusionArgs...)
}

func (s *Store) attachMatchedNodes(out *FindingMatch, query findingMatchQuery) error {
	rows, err := s.db.Query(`
		SELECT n.node_key, n.node_kind,
		       GROUP_CONCAT(DISTINCT `+query.repository+`),
		       COUNT(DISTINCT f.row_id), GROUP_CONCAT(DISTINCT f.rule_id)
		FROM `+query.from+`
		WHERE `+strings.Join(query.where, " AND ")+query.exclusion+`
		GROUP BY n.node_key, n.node_kind
		ORDER BY n.node_key`, query.args...)
	if err != nil {
		return fmt.Errorf("matching findings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m MatchedNode
		var rules, repos sql.NullString
		if err := rows.Scan(&m.Key, &m.Kind, &repos, &m.Findings, &rules); err != nil {
			return fmt.Errorf("scanning a match: %w", err)
		}
		m.Rules = splitSorted(rules)
		m.Repositories = splitSorted(repos)
		out.Nodes = append(out.Nodes, m)
	}
	return rows.Err()
}

func (s *Store) countMatched(from string, where []string, exclusion string, args []any) (int, error) {
	var n int
	if err := s.db.QueryRow(`
		SELECT COUNT(DISTINCT f.row_id) FROM `+from+`
		WHERE `+strings.Join(where, " AND ")+exclusion,
		args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting matched findings: %w", err)
	}
	return n, nil
}

func (s *Store) matchedFindingIDs(from string, where []string, exclusion string, args []any) ([]int64, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT f.row_id FROM `+from+`
		WHERE `+strings.Join(where, " AND ")+exclusion, args...)
	if err != nil {
		return nil, fmt.Errorf("listing matched findings: %w", err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// advisoriesMatching answers which nodes a live advisory names, and who reaches them, at the
// severities asked. It reads the exposure the Remediation screen reads, so the two cannot disagree
// about who an advisory reaches.
func (s *Store) advisoriesMatching(sel FindingSelector) (FindingMatch, error) {
	out := FindingMatch{Nodes: []MatchedNode{}, Population: PopulationAdvisories}
	if sel.asksFindings() {
		return out, fmt.Errorf("a selector asks about findings or about advisories, not both")
	}
	clause, args := advisoryBandClause("a.severity", sel.AdvisorySeverities)
	where := []string{"a.withdrawn_at IS NULL", clause}
	if hops := hopsCondition("e.hops", sel.Reached); hops != "" {
		where = append(where, hops)
	}
	if len(sel.Repositories) > 0 {
		lowered := make([]string, 0, len(sel.Repositories))
		for _, name := range sel.Repositories {
			lowered = append(lowered, strings.ToLower(name))
		}
		list, names := inList(lowered)
		where, args = append(where, "LOWER(er.repository_full_name) IN "+list), append(args, names...)
	}
	for _, terms := range []struct {
		matches []viewspec.PropertyMatch
		negate  string
	}{{sel.Properties, ""}, {sel.ExcludeProperties, "NOT "}} {
		names, by := valuesByKey(terms.matches)
		for _, name := range names {
			c, a := propertyClause("er.repository_full_name", name, by[name])
			where, args = append(where, terms.negate+c), append(args, a...)
		}
	}

	rows, err := s.db.Query(`
		SELECT n.node_key, n.node_kind,
		       GROUP_CONCAT(DISTINCT er.repository_full_name), GROUP_CONCAT(DISTINCT e.ghsa_id)
		FROM abom_exposure e
		JOIN abom_advisory a ON a.ghsa_id = e.ghsa_id
		JOIN abom_node_live n ON n.row_id = e.node_row_id
		JOIN repository er ON er.row_id = e.repository_row_id
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY n.node_key, n.node_kind
		ORDER BY n.node_key`, args...)
	if err != nil {
		return out, fmt.Errorf("matching advisories: %w", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		m := MatchedNode{Rules: []string{}}
		var repos, advisories sql.NullString
		if err := rows.Scan(&m.Key, &m.Kind, &repos, &advisories); err != nil {
			return out, fmt.Errorf("scanning an advisory match: %w", err)
		}
		m.Repositories = splitSorted(repos)
		m.Advisories = splitSorted(advisories)
		for _, id := range m.Advisories {
			if !seen[id] {
				seen[id] = true
				out.advisoryIDs = append(out.advisoryIDs, id)
			}
		}
		out.Nodes = append(out.Nodes, m)
	}
	out.Advisories = len(out.advisoryIDs)
	return out, rows.Err()
}

// splitSorted turns a GROUP_CONCAT into a stable list. It promises no order, and a caller
// comparing two runs would see a difference that is not one.
func splitSorted(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return []string{}
	}
	parts := strings.Split(v.String, ",")
	sort.Strings(parts)
	return parts
}
