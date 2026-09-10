package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// FindingSelector names a set of findings, and through them the nodes carrying them.
//
// This answers a **set**, never a graph. Measured 2026-09-10: applied as node filters the finding
// predicates leave 74% to 99% of their own match with no edges, because the graph is bipartite by
// kind — a workflow's edges run to actions, which mostly have no findings. So the gate and the
// snapshot Action get the set, and the Explorer goes on drawing its own picture.
type FindingSelector struct {
	// Rules to include, by exact query id or by a `prefix/*` pattern. A rule id *is* a CodeQL
	// query id, and query ids are hierarchical, so a pack has to be nameable without listing it.
	Rules []string
	// Exclude drops a node that carries any of these at all, rather than dropping the finding and
	// keeping the node. "A but not C" describes the action or workflow, not the result set.
	Exclude []string
	// Provider is the SARIF tool driver name, verbatim. It never matches a finding nobody
	// attributed: relabeling those would invent the provenance the column exists to hold.
	Provider string
	Severity string
	// Repositories narrows to what a gate is asking about. Compared case-insensitively: seven
	// accounts in the reference corpus spell themselves both ways, and a case-sensitive compare
	// once misclassified 211 edges.
	Repositories []string
}

// MatchedNode is one node the selector chose, carrying enough to explain why it was chosen.
type MatchedNode struct {
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Repositories are where the findings were *found*, which is not always where the node lives:
	// a step inside an external composite action belongs to no repository, and 93 of the 179 nodes
	// matching `actions/code-injection/*` on the reference corpus are exactly that. Every finding
	// names a repository; not every node does.
	Repositories []string `json:"repositories"`
	Findings     int      `json:"findings"`
	Rules        []string `json:"rules"`
}

// FindingMatch is the whole answer, nodes and total together.
//
// The total is here rather than left to the caller because summing `MatchedNode.Findings` is
// wrong and looks right: **113 of the reference corpus's 10,858 linked findings sit on two nodes**
// — a step and the workflow holding it — so the sum reported 10,971, more findings than exist.
type FindingMatch struct {
	Nodes []MatchedNode `json:"nodes"`
	// Findings counts each finding once however many nodes carry it.
	Findings int `json:"findings"`
}

// asked reports whether the selector names anything at all.
func (f FindingSelector) asked() bool {
	return len(f.Rules) > 0 || len(f.Exclude) > 0 || f.Provider != "" || f.Severity != ""
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
	out := FindingMatch{Nodes: []MatchedNode{}}
	if !sel.asked() {
		return out, nil
	}

	where := []string{"1"}
	args := []any{}
	if len(sel.Rules) > 0 {
		clause, a := ruleClause("f.rule_id", sel.Rules)
		where = append(where, clause)
		args = append(args, a...)
	}
	if sel.Provider != "" {
		// Not `IS NOT DISTINCT FROM`: a NULL provider is unknown, and unknown is not a match.
		where = append(where, "f.provider = ?")
		args = append(args, sel.Provider)
	}
	if sel.Severity != "" {
		where = append(where, "f.severity = ?")
		args = append(args, sel.Severity)
	}
	if len(sel.Repositories) > 0 {
		lowered := make([]string, 0, len(sel.Repositories))
		for _, name := range sel.Repositories {
			lowered = append(lowered, strings.ToLower(name))
		}
		list, names := inList(lowered)
		where = append(where, "LOWER(fr.repository_full_name) IN "+list)
		args = append(args, names...)
	}

	exclusion := ""
	if len(sel.Exclude) > 0 {
		clause, a := ruleClause("x.rule_id", sel.Exclude)
		exclusion = `
		  AND NOT EXISTS (SELECT 1 FROM abom_finding x
		                    JOIN abom_finding_node xn ON xn.finding_row_id = x.row_id
		                   WHERE xn.node_row_id = n.row_id AND ` + clause + `)`
		args = append(args, a...)
	}

	rows, err := s.db.Query(`
		SELECT n.node_key, n.node_kind,
		       GROUP_CONCAT(DISTINCT fr.repository_full_name),
		       COUNT(*), GROUP_CONCAT(DISTINCT f.rule_id)
		FROM abom_finding f
		JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		JOIN abom_node_live n ON n.row_id = fn.node_row_id
		LEFT JOIN repository fr ON fr.row_id = f.repository_row_id
		WHERE `+strings.Join(where, " AND ")+exclusion+`
		GROUP BY n.node_key, n.node_kind
		ORDER BY n.node_key`, args...)
	if err != nil {
		return out, fmt.Errorf("matching findings: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var m MatchedNode
		var rules, repos sql.NullString
		if err := rows.Scan(&m.Key, &m.Kind, &repos, &m.Findings, &rules); err != nil {
			return out, fmt.Errorf("scanning a match: %w", err)
		}
		m.Rules = splitSorted(rules)
		m.Repositories = splitSorted(repos)
		out.Nodes = append(out.Nodes, m)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	// Counted again rather than summed, because one finding can sit on two nodes.
	if err := s.db.QueryRow(`
		SELECT COUNT(DISTINCT f.row_id)
		FROM abom_finding f
		JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		JOIN abom_node_live n ON n.row_id = fn.node_row_id
		LEFT JOIN repository fr ON fr.row_id = f.repository_row_id
		WHERE `+strings.Join(where, " AND ")+exclusion,
		args...).Scan(&out.Findings); err != nil {
		return out, fmt.Errorf("counting matched findings: %w", err)
	}
	return out, nil
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
