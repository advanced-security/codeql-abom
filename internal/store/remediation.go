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
func (s *Store) RemediationPriority(limit int) ([]RemediationItem, error) {
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
		JOIN abom_node n ON n.row_id = e.node_row_id
		LEFT JOIN abom_advisory_package p ON p.advisory_row_id = a.row_id
		WHERE a.withdrawn_at IS NULL
		GROUP BY e.ghsa_id, a.cve_id, a.severity, a.summary,
		         a.cvss_v3_score, a.epss_percentage`)
	if err != nil {
		return nil, fmt.Errorf("ranking remediation: %w", err)
	}
	defer rows.Close()

	var out []RemediationItem
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

// sortRemediation puts the most work first, breaking ties towards advisories that can actually
// be fixed and then towards those the affected repositories can fix themselves.
func sortRemediation(items []RemediationItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
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
