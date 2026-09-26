package store

import (
	"fmt"
	"slices"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// bandSQL reads a score column through the language's thresholds, so SQL and Go cannot disagree
// about which band a finding is in. NULL and zero fall through to unknown, as BandOf reads them.
func bandSQL(column string) string {
	var b strings.Builder
	b.WriteString("(CASE")
	for _, f := range viewspec.BandFloors() {
		fmt.Fprintf(&b, " WHEN %s >= %g THEN '%s'", column, f.Floor, f.Band)
	}
	fmt.Fprintf(&b, " WHEN %s > 0 THEN '%s' ELSE '%s' END)",
		column, viewspec.BandLow, viewspec.BandUnknown)
	return b.String()
}

// bandClause keeps a finding whose score falls in one of the bands.
func bandClause(column string, bands []string) (string, []any) {
	list, args := inList(bands)
	return bandSQL(column) + " IN " + list, args
}

// advisoryBandSQL reads the severity an advisory was published at into the words a policy asks
// with. `moderate` is GitHub's other spelling of medium; anything else it did not rate.
func advisoryBandSQL(column string) string {
	return fmt.Sprintf(`(CASE LOWER(COALESCE(%s, ''))
	  WHEN 'critical' THEN '%s' WHEN 'high' THEN '%s' WHEN 'medium' THEN '%s'
	  WHEN 'moderate' THEN '%s' WHEN 'low' THEN '%s' ELSE '%s' END)`, column,
		viewspec.BandCritical, viewspec.BandHigh, viewspec.BandMedium, viewspec.BandMedium,
		viewspec.BandLow, viewspec.AdvisoryUnrated)
}

// advisoryBandClause keeps an advisory published at one of the severities.
func advisoryBandClause(column string, severities []string) (string, []any) {
	list, args := inList(severities)
	return advisoryBandSQL(column) + " IN " + list, args
}

// hopsCondition narrows a route to direct or transitive. Both, or neither, is no narrowing.
func hopsCondition(column string, reached []string) string {
	direct := slices.Contains(reached, viewspec.ReachedDirect)
	transitive := slices.Contains(reached, viewspec.ReachedTransitive)
	switch {
	case direct && !transitive:
		return column + " = 1"
	case transitive && !direct:
		return column + " > 1"
	}
	return ""
}

// bandRankSQL ranks a score's band on the language's scale, so a worst band is a MAX.
func bandRankSQL(column string) string {
	var b strings.Builder
	b.WriteString("(CASE " + bandSQL(column))
	for _, band := range viewspec.Bands() {
		fmt.Fprintf(&b, " WHEN '%s' THEN %d", band, viewspec.BandRank(band))
	}
	b.WriteString(" END)")
	return b.String()
}

// SeverityCounts is how many findings sit in each band. A findings count without it counts 36
// critical findings in the same column as 11,998 medium ones.
type SeverityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

func (c *SeverityCounts) add(band string, n int) {
	switch band {
	case viewspec.BandCritical:
		c.Critical += n
	case viewspec.BandHigh:
		c.High += n
	case viewspec.BandMedium:
		c.Medium += n
	case viewspec.BandLow:
		c.Low += n
	default:
		c.Unknown += n
	}
}

func (c SeverityCounts) total() int { return c.Critical + c.High + c.Medium + c.Low + c.Unknown }

// bandSums aggregates a score column into the bands, in the order targets scans them.
func bandSums(column string) string {
	sums := make([]string, 0, 5)
	for _, band := range viewspec.Bands() {
		sums = append(sums, fmt.Sprintf("COALESCE(SUM(%s = '%s'), 0)", bandSQL(column), band))
	}
	return strings.Join(sums, ", ")
}

// targets are the fields in Bands order, for scanning bandSums.
func (c *SeverityCounts) targets() []any {
	return []any{&c.Critical, &c.High, &c.Medium, &c.Low, &c.Unknown}
}

// worstRank is the rank of the most severe band holding a finding, -1 when none does.
func (c SeverityCounts) worstRank() int {
	for _, band := range viewspec.Bands() {
		if c.of(band) > 0 {
			return viewspec.BandRank(band)
		}
	}
	return -1
}

func (c SeverityCounts) of(band string) int {
	switch band {
	case viewspec.BandCritical:
		return c.Critical
	case viewspec.BandHigh:
		return c.High
	case viewspec.BandMedium:
		return c.Medium
	case viewspec.BandLow:
		return c.Low
	}
	return c.Unknown
}

// severityByNode counts each listed node's findings by band. Read for the rows a page holds rather
// than beside every candidate row, because five correlated counts per row is the shape that made
// the frontier the slowest query in the system.
func (s *Store) severityByNode(mode string, keys []string) (map[string]SeverityCounts, error) {
	out := map[string]SeverityCounts{}
	if len(keys) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(keys))
	for _, k := range keys {
		args = append(args, k)
	}
	return out, s.bandCounts(out, `
		SELECT n.node_key, `+bandSQL("f.security_severity")+`, COUNT(*)
		FROM abom_node_live n
		JOIN abom_finding_node fn ON fn.node_row_id = n.row_id
		JOIN `+findingSource(mode)+` f ON f.row_id = fn.finding_row_id
		WHERE n.node_key IN (`+placeholders(len(keys))+`)
		GROUP BY 1, 2`, args...)
}

// severityByRepository counts each listed repository's findings by band.
func (s *Store) severityByRepository(names []string) (map[string]SeverityCounts, error) {
	out := map[string]SeverityCounts{}
	if len(names) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(names))
	for _, n := range names {
		args = append(args, n)
	}
	return out, s.bandCounts(out, `
		SELECT r.repository_full_name, `+bandSQL("f.security_severity")+`, COUNT(*)
		FROM abom_finding_regular f
		JOIN repository r ON r.row_id = f.repository_row_id
		WHERE r.repository_full_name IN (`+placeholders(len(names))+`)
		GROUP BY 1, 2`, args...)
}

func (s *Store) bandCounts(into map[string]SeverityCounts, query string, args ...any) error {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("counting findings by severity: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, band string
		var n int
		if err := rows.Scan(&key, &band, &n); err != nil {
			return fmt.Errorf("scanning a severity count: %w", err)
		}
		c := into[key]
		c.add(band, n)
		into[key] = c
	}
	return rows.Err()
}
