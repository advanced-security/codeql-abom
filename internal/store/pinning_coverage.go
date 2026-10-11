package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// Pinning scope levels, coarsest first. Each is a grouping of the same reference set.
const (
	PinningByOwner     = "owner"
	PinningByRepo      = "repo"
	PinningByWorkflow  = "workflow"
	PinningByReference = "reference"
)

// PinningFilter narrows the references every level is computed from.
//
// Filtering has to happen here rather than over already-aggregated rows, or a parent would stop
// equalling the sum of its children the moment a filter was applied.
type PinningFilter struct {
	Search string
	// RefStyle is `commit`, `tag`, or empty for both. Lexical: how the reference is written.
	RefStyle    string
	FlaggedOnly bool
	// Scope bounds every level to one grouping's repositories. A pinning row describes a file in
	// a repository somebody owns, so the sources are the answer here rather than their closure.
	Scope RepositoryScope
}

// PinningRow is one scope's reference pinning, measured two independent ways.
//
// The two are deliberately not combined into a single "correctly pinned" figure:
//
//   - Commit and Floating describe how the reference is *written*. Whether a string is a
//     40-character hex commit id is an observation, in the same class as quoting the reference
//     verbatim.
//   - Flagged is how many references `actions/unpinned-tag` objected to. That is the security
//     verdict, and it belongs to the query.
//
// They disagree, and the disagreement is informative rather than an error: the rule leaves
// GitHub-owned actions alone, so thousands of floating references to actions/* are never
// flagged. Merging the two would turn a query's narrow judgement into a broad one.
type PinningRow struct {
	Scope    string `json:"scope"`
	ScopeKey string `json:"scopeKey"`
	// Label is the name relative to the parent, so a tree does not repeat the path at every level.
	Label string `json:"label"`
	// Ref and Kind are carried only by a leaf, which is one target at one reference.
	Ref  string `json:"ref,omitempty"`
	Kind string `json:"kind,omitempty"`
	// Pinnable counts references that carry a ref and could therefore name a commit: one per
	// thing a line declares at the commits in use, so a line whose commits declared two
	// references counts two.
	Pinnable int `json:"pinnable"`
	Commit   int `json:"commit"`
	Floating int `json:"floating"`
	// Flagged is the count of `actions/unpinned-tag` findings attributed to those references.
	Flagged int `json:"flagged"`
	// FloatingUnflagged is floating references the rule did not object to, almost all of them
	// GitHub-owned. Naming it keeps the gap between the two measures visible.
	FloatingUnflagged int  `json:"floatingUnflagged"`
	HasChildren       bool `json:"hasChildren"`
	// Owned marks a row whose every reference is in a repository the declared scope covers. A
	// mark, never a filter: the row is present either way.
	Owned bool `json:"owned"`
	// Yours and FlaggedSplit divide the totals. Both halves are always reported, so the
	// actionable figure is stated without the corpus figure disappearing.
	Yours        OwnedSplit `json:"yours"`
	FlaggedSplit OwnedSplit `json:"flaggedSplit"`
}

// pinnableReferences is the one reference set every level groups: what each line declared at the
// commits in use, read from a declaredEdgeTable, so a line whose commits declared two references
// counts both and a reference only a history commit declared counts nothing. Binds the origin.
//
// A reference is pinnable when it carries a ref at all; local `./` uses cannot be pinned and
// counting them would understate every scope. Upstream repositories are excluded: they were
// fetched only to resolve what the corpus depends on, and their pinning is not its to answer for.
func pinnableReferences(table string) string {
	return `ref AS (
	SELECT SUBSTR(rep.repository_full_name, 1,
	              INSTR(rep.repository_full_name, '/') - 1) AS owner_key,
	       rep.repository_full_name AS repo_key,
	       rep.repository_full_name || '/' || e.file_path AS workflow_key,
	       e.file_path AS file_path,
	       dn.node_key AS target_key,
	       dn.node_kind AS target_kind,
	       e.ref_value AS ref_value,
	       CASE WHEN LENGTH(e.ref_value) = 40
	             AND e.ref_value GLOB '[0-9a-f]*' THEN 1 ELSE 0 END AS is_commit,
	       CASE WHEN f.row_id IS NULL THEN 0 ELSE 1 END AS flagged,
	       CASE WHEN ` + ownedFullName("rep.repository_full_name") + ` THEN 1 ELSE 0 END AS owned
	FROM ` + table + ` e
	JOIN abom_node_live dn ON dn.row_id = e.dst_node_row_id
	JOIN repository rep ON rep.row_id = e.repository_row_id
	LEFT JOIN abom_finding_regular f
	       ON f.repository_row_id = e.repository_row_id
	      AND f.file_path = e.file_path
	      AND f.start_line = e.start_line
	      AND f.rule_id = 'actions/unpinned-tag'
	WHERE e.ref_value IS NOT NULL AND e.ref_value <> ''
	  AND dn.is_external = 1
	  AND rep.origin = ?)`
}

// measures are the same five columns at every level, so a parent and its children are directly
// comparable.
const pinningMeasures = `COUNT(*),
	COALESCE(SUM(is_commit), 0),
	COALESCE(SUM(1 - is_commit), 0),
	COALESCE(SUM(flagged), 0),
	COALESCE(SUM(CASE WHEN flagged = 0 AND is_commit = 0 THEN 1 ELSE 0 END), 0),
	COALESCE(SUM(owned), 0),
	COALESCE(SUM(owned * flagged), 0)`

// splitOwned divides a row's measures by the owned count read with them, so the halves are taken
// from the same references as the whole and cannot drift from it.
func (p *PinningRow) splitOwned(yours, yoursFlagged int) {
	p.Yours = OwnedSplit{Yours: yours, Elsewhere: p.Pinnable - yours}
	p.FlaggedSplit = OwnedSplit{Yours: yoursFlagged, Elsewhere: p.Flagged - yoursFlagged}
	p.Owned = p.Pinnable > 0 && yours == p.Pinnable
}

// where builds the filter clause shared by every level.
//
// `bound` is the grouping restriction, already resolved by the store, and is applied here with
// everything else so it lands before the GROUP BY and the measures are recomputed rather than
// narrowed after the fact.
func (f PinningFilter) where(parentColumn, parent, bound string, boundArgs []any) (string, []any) {
	clauses := []string{}
	args := []any{}

	// An absent parent means the whole level rather than a parent whose key is empty, which is
	// what lets a level be read flat as well as expanded.
	if parentColumn != "" && parent != "" {
		clauses = append(clauses, parentColumn+" = ?")
		args = append(args, parent)
	}
	if bound != "" {
		clauses = append(clauses, bound)
		args = append(args, boundArgs...)
	}
	if f.Search != "" {
		// Matched against the whole path so filtering by an action name still narrows the
		// owners and repositories above it, rather than emptying the level being looked at.
		clauses = append(clauses,
			`(workflow_key || ' ' || target_key || '@' || ref_value) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	switch f.RefStyle {
	case "commit":
		clauses = append(clauses, "is_commit = 1")
	case "tag":
		clauses = append(clauses, "is_commit = 0")
	}
	if f.FlaggedOnly {
		clauses = append(clauses, "flagged = 1")
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// PinningLevel returns one level of the pinning tree, widest gap first.
func (s *Store) PinningLevel(scope, parent string, f PinningFilter, limit int) ([]PinningRow, error) {
	bound, boundArgs, err := s.restrictClause(f.Scope, "repo_key")
	if err != nil {
		return nil, err
	}

	var key, label, parentColumn, group string
	switch scope {
	case PinningByRepo:
		key, label, parentColumn, group = "repo_key", "SUBSTR(repo_key, INSTR(repo_key, '/') + 1)", "owner_key", "repo_key"
	case PinningByWorkflow:
		key, label, parentColumn, group = "workflow_key", "file_path", "repo_key", "workflow_key, file_path"
	case PinningByReference:
		key, label, parentColumn, group = "target_key", "target_key", "workflow_key", "target_key, ref_value"
	default:
		scope = PinningByOwner
		key, label, parentColumn, group = "owner_key", "owner_key", "", "owner_key"
	}

	leaf := scope == PinningByReference
	selected := key + ", " + label
	if leaf {
		selected += ", ref_value, MIN(target_kind)"
	} else {
		selected += ", '', ''"
	}

	table, err := s.declaredEdgeTable(AnalysisModeRegular)
	if err != nil {
		return nil, err
	}
	where, args := f.where(parentColumn, parent, bound, boundArgs)
	query := `WITH ` + pinnableReferences(table) + `
		SELECT ` + selected + `, ` + pinningMeasures + `
		FROM ref` + where + `
		GROUP BY ` + group + `
		ORDER BY SUM(flagged) DESC, ` + key
	args = append([]any{OriginSeed}, args...)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	return queryAll(s, "reading pinning level "+scope, func(r *sql.Rows) (PinningRow, error) {
		p := PinningRow{Scope: scope, HasChildren: !leaf}
		var yours, yoursFlagged int
		if err := r.Scan(&p.ScopeKey, &p.Label, &p.Ref, &p.Kind, &p.Pinnable, &p.Commit,
			&p.Floating, &p.Flagged, &p.FloatingUnflagged, &yours, &yoursFlagged); err != nil {
			return p, err
		}
		p.splitOwned(yours, yoursFlagged)
		return p, nil
	}, query, args...)
}

// PinningTotals is the same measurement across everything the filter admits.
func (s *Store) PinningTotals(f PinningFilter) (PinningRow, error) {
	bound, boundArgs, err := s.restrictClause(f.Scope, "repo_key")
	if err != nil {
		return PinningRow{}, err
	}
	table, err := s.declaredEdgeTable(AnalysisModeRegular)
	if err != nil {
		return PinningRow{}, err
	}
	where, args := f.where("", "", bound, boundArgs)
	args = append([]any{OriginSeed}, args...)

	row := s.db.QueryRow(`WITH `+pinnableReferences(table)+`
		SELECT `+pinningMeasures+` FROM ref`+where, args...)

	total := PinningRow{Scope: "all"}
	var yours, yoursFlagged int
	if err := row.Scan(&total.Pinnable, &total.Commit, &total.Floating, &total.Flagged,
		&total.FloatingUnflagged, &yours, &yoursFlagged); err != nil {
		return PinningRow{}, fmt.Errorf("reading pinning totals: %w", err)
	}
	total.splitOwned(yours, yoursFlagged)
	return total, nil
}
