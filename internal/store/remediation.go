package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// severityRank orders advisories for remediation.
//
// This is ABOM's own ordering heuristic, not a judgement about any advisory: the severity it
// ranks is the one the advisory itself publishes.
var severityRank = map[string]int{
	"critical": 4,
	"high":     3,
	"medium":   2,
	"moderate": 2,
	"low":      1,
}

// RemediationItem is one advisory ranked by how much work it represents.
type RemediationItem struct {
	GHSAID   string `json:"ghsaId"`
	CVEID    string `json:"cveId"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	// DirectRepos can fix the reference themselves; TransitiveRepos wait on an intermediary.
	DirectRepos     int      `json:"directRepos"`
	TransitiveRepos int      `json:"transitiveRepos"`
	Actions         []string `json:"actions"`
	CVSSv3Score     float64  `json:"cvssV3Score"`
	HasCVSS         bool     `json:"hasCvss"`
	EPSSPercentage  float64  `json:"epssPercentage"`
	HasEPSS         bool     `json:"hasEpss"`
	// Fixable reports whether any affected range has a published patch. An advisory with no
	// fix cannot be remediated by bumping a version, however widely it reaches.
	Fixable bool `json:"fixable"`
	// Priority is severity rank multiplied by repositories reached.
	Priority int `json:"priority"`
}

// RemediationPriority ranks advisories by published severity against how many repositories
// under review reach them.
//
// The two inputs are kept visible in the result rather than collapsed into an opaque number,
// because the ranking is a scheduling aid and a reader has to be able to disagree with it.
// Scores that describe an advisory rather than its reach here — CVSS and EPSS — are reported
// alongside but deliberately left out of the arithmetic, so the number means exactly what it
// says: severity times reach.
func (s *Store) RemediationPriority(limit int, sc RepositoryScope) ([]RemediationItem, error) {
	// Before the GROUP BY, so the counts are recomputed for the grouping rather than narrowed
	// after the fact while still spanning the organization.
	bound, bindings, err := s.restrict(sc, "er.repository_full_name")
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT e.ghsa_id, COALESCE(a.cve_id,''), COALESCE(a.severity,''),
		       COALESCE(a.summary,''), a.cvss_v3_score, a.epss_percentage,
		       COUNT(DISTINCT CASE WHEN e.hops = 1 THEN e.repository_row_id END),
		       COUNT(DISTINCT CASE WHEN e.hops > 1 THEN e.repository_row_id END),
		       GROUP_CONCAT(DISTINCT n.node_key),
		       MAX(CASE WHEN p.first_patched_version IS NOT NULL
		                 AND p.first_patched_version <> '' THEN 1 ELSE 0 END)
		FROM abom_exposure e
		JOIN abom_advisory a ON a.ghsa_id = e.ghsa_id
		JOIN abom_node_live n ON n.row_id = e.node_row_id
		JOIN repository er ON er.row_id = e.repository_row_id
		LEFT JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		WHERE a.withdrawn_at IS NULL`+bound+`
		GROUP BY e.ghsa_id, a.cve_id, a.severity, a.summary,
		         a.cvss_v3_score, a.epss_percentage`, bindings...)
	if err != nil {
		return nil, fmt.Errorf("ranking remediation: %w", err)
	}
	defer rows.Close()

	out := []RemediationItem{}
	for rows.Next() {
		var it RemediationItem
		var cvss, epss sql.NullFloat64
		var actions sql.NullString
		var fixable int
		if err := rows.Scan(&it.GHSAID, &it.CVEID, &it.Severity, &it.Summary,
			&cvss, &epss, &it.DirectRepos, &it.TransitiveRepos, &actions, &fixable); err != nil {
			return nil, fmt.Errorf("scanning remediation row: %w", err)
		}
		// A zero CVSS score means unscored, not harmless.
		if cvss.Valid && cvss.Float64 > 0 {
			it.CVSSv3Score, it.HasCVSS = cvss.Float64, true
		}
		if epss.Valid {
			it.EPSSPercentage, it.HasEPSS = epss.Float64, true
		}
		if actions.Valid && actions.String != "" {
			it.Actions = strings.Split(actions.String, ",")
		}
		it.Fixable = fixable == 1
		it.Priority = severityRank[strings.ToLower(it.Severity)] *
			(it.DirectRepos + it.TransitiveRepos)
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortRemediation(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AdvisoriesQuery cuts one page out of the advisories unit.
type AdvisoriesQuery struct {
	Offset   int
	Limit    int
	Grouping RepositoryScope
}

// AdvisoriesPage is one page of advisories, the size of the match, and what the match excludes.
type AdvisoriesPage struct {
	Items []RemediationItem `json:"items"`
	Total int               `json:"total"`
	// Unreached counts live advisories that name no action anything under review uses.
	//
	// The exclusion is the invariant working — those advisories name actions that appear only in
	// upstream repositories' own CI, which is nobody's supply chain — but it is why a coverage
	// strip counting 11 sat above a table holding 3, with a critical among the absent and nothing
	// on screen accounting for it. Silence reads as completeness.
	Unreached int `json:"unreached"`
}

// AdvisoriesPage lists advisories that reach something under review, worst first.
//
// Ordered across the whole match before the page is cut, because priority is severity times reach
// and the worst advisory on a page is not the worst advisory.
func (s *Store) AdvisoriesPage(q AdvisoriesQuery) (AdvisoriesPage, error) {
	all, err := s.RemediationPriority(0, q.Grouping)
	if err != nil {
		return AdvisoriesPage{}, err
	}
	unreached, err := s.advisoriesReachingNothing()
	if err != nil {
		return AdvisoriesPage{}, err
	}

	page := AdvisoriesPage{Total: len(all), Unreached: unreached, Items: []RemediationItem{}}
	if q.Offset < len(all) {
		end := len(all)
		if q.Limit > 0 && q.Offset+q.Limit < end {
			end = q.Offset + q.Limit
		}
		page.Items = all[q.Offset:end]
	}
	return page, nil
}

// advisoriesReachingNothing counts live advisories with no exposure row at all.
func (s *Store) advisoriesReachingNothing() (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM abom_advisory a
		 WHERE a.withdrawn_at IS NULL
		   AND NOT EXISTS (SELECT 1 FROM abom_exposure e WHERE e.ghsa_id = a.ghsa_id)`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting advisories that reach nothing: %w", err)
	}
	return n, nil
}

// sortRemediation orders by priority, then by whether a fix exists, then by direct reach.
func sortRemediation(out []RemediationItem) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		if a.Fixable != b.Fixable {
			return a.Fixable
		}
		if a.DirectRepos != b.DirectRepos {
			return a.DirectRepos > b.DirectRepos
		}
		return a.GHSAID < b.GHSAID
	})
}
