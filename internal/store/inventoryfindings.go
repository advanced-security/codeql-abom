package store

import (
	"database/sql"
	"fmt"
	"strings"
)

type FindingReviewQuery struct {
	InventoryQuery
	AssetKind string
	AssetKey  string
	FindingID int64
	Text      string
}

type FindingReview struct {
	ID                int64    `json:"id"`
	RuleID            string   `json:"ruleId"`
	Title             string   `json:"title"`
	Repository        string   `json:"repository"`
	FilePath          string   `json:"filePath"`
	StartLine         int      `json:"startLine"`
	Message           string   `json:"message"`
	Severity          string   `json:"severity"`
	SecuritySeverity  *float64 `json:"securitySeverity"`
	Provider          string   `json:"provider"`
	QueryRunID        int64    `json:"queryRunId"`
	Commit            string   `json:"commit"`
	RecordedAt        string   `json:"recordedAt"`
	Linked            bool     `json:"linked"`
	HelpAvailable     bool     `json:"helpAvailable"`
	EvidenceAvailable bool     `json:"evidenceAvailable"`
}

type FindingsPage struct {
	Items            []FindingReview `json:"items"`
	Total            int             `json:"total"`
	UnlinkedExcluded int             `json:"unlinkedExcluded"`
}

func (s *Store) InventoryFindingsPage(q FindingReviewQuery) (FindingsPage, error) {
	page := FindingsPage{Items: []FindingReview{}}
	plan, err := s.findingReviewPlan(q)
	if err != nil {
		return page, err
	}
	page.UnlinkedExcluded = plan.unlinkedExcluded
	page.Total, err = s.countMatched(plan.from, []string{plan.where}, "", plan.args)
	if err != nil {
		return page, err
	}
	page.Items, err = s.findingReviewItems(q, plan)
	return page, err
}

type findingReviewPlan struct {
	from             string
	where            string
	args             []any
	unlinkedExcluded int
}

func (s *Store) findingReviewPlan(q FindingReviewQuery) (findingReviewPlan, error) {
	if q.AssetKind != "" && q.AssetKind != "node" && q.AssetKind != "repository" ||
		(q.AssetKind == "") != (q.AssetKey == "") {
		return findingReviewPlan{}, fmt.Errorf("finding review needs both an asset and its kind: node or repository")
	}
	query := buildFindingMatchQuery(FindingSelector{
		Rules: q.Rules, Exclude: q.NotRules, Tags: q.Tags, NotTags: q.NotTags,
		Provider: q.Provider, Severity: q.Severity,
		SecuritySeverities: q.SecuritySeverities, Sources: q.Sources,
		Repositories: q.Repositories, Mode: q.Mode,
	})
	plan := findingReviewPlan{
		from: findingSource(q.Mode) + ` f
		LEFT JOIN abom_finding_node fn ON fn.finding_row_id = f.row_id
		LEFT JOIN abom_node_live n ON n.row_id = fn.node_row_id
		LEFT JOIN repository fr ON fr.row_id = f.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = COALESCE(n.container_node_row_id, n.row_id)`,
		where: strings.Join(query.where, " AND ") + query.exclusion, args: query.args,
	}
	if q.FindingID > 0 {
		plan.add("f.row_id = ?", q.FindingID)
	}
	if q.Text != "" {
		text := "%" + escapeLike(q.Text) + "%"
		plan.add(`(f.message LIKE ? ESCAPE '\' OR f.rule_id LIKE ? ESCAPE '\' OR f.file_path LIKE ? ESCAPE '\')`, text, text, text)
	}
	if q.AssetKind == "repository" {
		plan.add("LOWER(fr.repository_full_name) = ?", strings.ToLower(q.AssetKey))
		if err := s.reviewRepositoryScope(&plan, q); err != nil {
			return plan, err
		}
		q.Scope, q.Search, q.Grouping = "", "", RepositoryScope{}
	}
	if q.AssetKind == "node" {
		plan.add("n.node_key = ?", q.AssetKey)
		var kind string
		err := s.db.QueryRow("SELECT node_kind FROM abom_node_live WHERE node_key = ?", q.AssetKey).Scan(&kind)
		if err != nil && err != sql.ErrNoRows {
			return plan, err
		}
		if kind == "workflow" || kind == "reusable_workflow" || kind == "job" {
			bound, args, err := s.restrict(q.Grouping, "fr.repository_full_name")
			if err != nil {
				return plan, err
			}
			plan.where += bound
			plan.args = append(plan.args, args...)
			q.Grouping = RepositoryScope{}
		}
	}
	nodeFiltered := q.Scope != "" || q.Search != "" || len(q.Kinds) > 0 || len(q.Origins) > 0 ||
		q.MinReach > 0 || q.RefStyle != "" || q.Grouping.asked() || len(q.NotRules) > 0 ||
		len(q.NotTags) > 0
	for _, flag := range q.Flags {
		nodeFiltered = nodeFiltered || flag != "findings"
	}
	if nodeFiltered {
		if err := s.reviewNodeScope(&plan, q); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (s *Store) reviewNodeScope(plan *findingReviewPlan, q FindingReviewQuery) error {
	if q.AssetKey == "" {
		var err error
		plan.unlinkedExcluded, err = s.countMatched(plan.from, []string{plan.where,
			`NOT EXISTS (SELECT 1 FROM abom_finding_node missing
			JOIN abom_node_live present ON present.row_id = missing.node_row_id
			WHERE missing.finding_row_id = f.row_id)`}, "", plan.args)
		if err != nil {
			return err
		}
	}
	selection := q.InventoryQuery
	selection.Sort = ""
	if len(selection.Kinds) == 0 {
		selection.Kinds = []string{"org", "repo", "workflow", "job", "step", "action", "composite_action", "reusable_workflow", "docker_image"}
	}
	selected, err := s.inventoryPageQuery(selection)
	if err != nil {
		return err
	}
	plan.add(`n.row_id IN (SELECT n.row_id FROM abom_node_live n
		LEFT JOIN repository r ON r.row_id = n.repository_row_id
		LEFT JOIN abom_blast_radius b ON b.node_row_id = n.row_id `+selected.where+`)`, selected.whereArgs...)
	return nil
}

func (p *findingReviewPlan) add(clause string, args ...any) {
	p.where += " AND " + clause
	p.args = append(p.args, args...)
}

func (s *Store) reviewRepositoryScope(plan *findingReviewPlan, q FindingReviewQuery) error {
	if err := s.prepareScope(q.Scope, q.Mode); err != nil {
		return err
	}
	if q.Search != "" {
		plan.add(`fr.repository_full_name LIKE ? ESCAPE '\'`, "%"+escapeLike(q.Search)+"%")
	}
	switch q.Scope {
	case ScopeOwned:
		plan.add("f.repository_row_id IN (" + declaredRoots + ")")
	case ScopeSupplyChain:
		plan.add("f.repository_row_id IN (" + scopeRepositories(declaredRoots, q.Mode) + ")")
	case ScopeExposure:
		plan.add(`EXISTS (SELECT 1 FROM abom_exposure exposed WHERE exposed.repository_row_id = f.repository_row_id)`)
	}
	bound, args, err := s.restrict(q.Grouping, "fr.repository_full_name")
	plan.where += bound
	plan.args = append(plan.args, args...)
	return err
}

func (s *Store) findingReviewItems(q FindingReviewQuery, plan findingReviewPlan) ([]FindingReview, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := max(q.Offset, 0)
	mode := q.Mode
	if mode == "" {
		mode = AnalysisModeRegular
	}
	from := plan.from + ` LEFT JOIN abom_query_run qr ON qr.row_id = (
		SELECT sight.query_run_id FROM abom_finding_sighting sight
		JOIN abom_query_run evaluated ON evaluated.row_id = sight.query_run_id
		WHERE sight.finding_row_id = f.row_id AND evaluated.analysis_mode = ?
		ORDER BY evaluated.ran_at DESC, evaluated.row_id DESC LIMIT 1)
		LEFT JOIN abom_query_run_rule documented ON documented.query_run_id = qr.row_id AND documented.rule_id = f.rule_id
		LEFT JOIN abom_finding_sighting recorded ON recorded.finding_row_id = f.row_id AND recorded.query_run_id = qr.row_id`
	return queryAll(s, "listing findings for review", func(rows *sql.Rows) (FindingReview, error) {
		var item FindingReview
		return item, rows.Scan(&item.ID, &item.RuleID, &item.Repository, &item.FilePath,
			&item.StartLine, &item.Message, &item.Severity, &item.SecuritySeverity, &item.Provider,
			&item.QueryRunID, &item.Commit, &item.RecordedAt, &item.Linked, &item.Title, &item.HelpAvailable, &item.EvidenceAvailable)
	}, `SELECT f.row_id, f.rule_id, COALESCE(fr.repository_full_name, ''),
		COALESCE(f.file_path, ''), COALESCE(f.start_line, 0), COALESCE(f.message, ''),
		COALESCE(f.severity, ''), f.security_severity, COALESCE(f.provider, ''),
		COALESCE(qr.row_id, 0), COALESCE(qr.db_commit, ''), COALESCE(qr.ran_at, ''),
		MAX(n.row_id IS NOT NULL), COALESCE(json_extract(documented.documentation_json, '$.title'), ''),
		COALESCE(NULLIF(json_extract(documented.documentation_json, '$.helpMarkdown'), ''), json_extract(documented.documentation_json, '$.helpText'), '') <> '',
		recorded.evidence_json IS NOT NULL
		FROM `+from+` WHERE `+plan.where+`
		GROUP BY f.row_id
		ORDER BY CASE WHEN f.security_severity >= 9 THEN 0 WHEN f.security_severity >= 7 THEN 1
		WHEN f.security_severity >= 4 THEN 2 WHEN f.security_severity > 0 THEN 3 ELSE 4 END,
		MAX(COALESCE(b.dependent_repos, 0)) DESC, f.rule_id, fr.repository_full_name,
		f.file_path, f.start_line, f.row_id LIMIT ? OFFSET ?`,
		append(append([]any{mode}, plan.args...), limit, offset)...)
}
