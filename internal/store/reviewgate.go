package store

import (
	"fmt"
)

// The review rung is earned by a comparison: somebody has seen what this exact version would add
// to the alerts developers already see, under the CodeQL that would run it, before it bites.
//
// Over any scope somebody chose rather than the declared one. The declared scope proves the query
// was looked at where it will bite, and waits on what a scope means wherever it is reused; this
// proves it was looked at at all, which no rung said before.

// noRunRefusal is a version nothing has run, so nothing has been compared.
const noRunRefusal = "no run of this version has been compared with the alerts code scanning " +
	"holds; run it over regular databases, then compare it"

// reviewEvidence is one run of a version, and one comparison of it when there is one.
type reviewEvidence struct {
	run        int64
	mode       string
	comparison int64
	state      string
	cli        string
	compared   int
}

// ReviewRefusals is what advancing each version to review would be refused with, for the catalog.
type ReviewRefusals map[string]string

// For is the refusal for one version, which is the one for a version nothing ran when it has none.
func (r ReviewRefusals) For(queryID, version string) string {
	if refusal, ok := r[queryID+"\x00"+version]; ok {
		return refusal
	}
	return noRunRefusal
}

// ReviewRefusal says why a version may not be advanced to review under the CodeQL cli, or is empty
// when a comparison has earned it.
func (s *Store) ReviewRefusal(queryID, version, cli string) (string, error) {
	evidence, err := s.reviewEvidence(queryID, version)
	if err != nil {
		return "", err
	}
	return reviewRefusal(evidence[queryID+"\x00"+version], cli), nil
}

// ReviewRefusals reads every version's refusal at once, so a catalog asks the store one question.
func (s *Store) ReviewRefusals(cli string) (ReviewRefusals, error) {
	evidence, err := s.reviewEvidence("", "")
	if err != nil {
		return nil, err
	}
	out := ReviewRefusals{}
	for key, e := range evidence {
		out[key] = reviewRefusal(e, cli)
	}
	return out, nil
}

// reviewEvidence reads the runs of one version, or of every version when none is named, newest
// run and newest comparison first.
func (s *Store) reviewEvidence(queryID, version string) (map[string][]reviewEvidence, error) {
	rows, err := s.db.Query(`
		SELECT q.query_id, q.query_version, r.row_id, r.analysis_mode,
		       COALESCE(c.row_id, 0), COALESCE(c.state, ''), COALESCE(c.cli_version, ''),
		       (SELECT COUNT(*) FROM abom_comparison_member m
		        WHERE m.comparison_id = c.row_id AND m.status = ?)
		FROM abom_analysis_query q
		JOIN abom_analysis_run r ON r.row_id = q.run_id
		LEFT JOIN abom_comparison c ON c.run_id = r.row_id
		WHERE (? = '' OR (q.query_id = ? AND q.query_version = ?))
		ORDER BY r.row_id DESC, c.row_id DESC`,
		ComparisonCompared, queryID, queryID, version)
	if err != nil {
		return nil, fmt.Errorf("reading what earns review: %w", err)
	}
	defer rows.Close()
	out := map[string][]reviewEvidence{}
	for rows.Next() {
		var id, v string
		var e reviewEvidence
		if err := rows.Scan(&id, &v, &e.run, &e.mode, &e.comparison, &e.state, &e.cli,
			&e.compared); err != nil {
			return nil, fmt.Errorf("scanning what earns review: %w", err)
		}
		out[id+"\x00"+v] = append(out[id+"\x00"+v], e)
	}
	return out, rows.Err()
}

// reviewRefusal decides from a version's runs, newest first. Any comparison that earns review is
// enough; failing that, the refusal names the nearest miss, since that is what somebody would fix.
func reviewRefusal(evidence []reviewEvidence, cli string) string {
	if cli == "" {
		return "this server cannot name the CodeQL it runs, so no comparison can be shown to " +
			"describe what it would report"
	}
	if len(evidence) == 0 {
		return noRunRefusal
	}
	var otherCLI, nothing, unfinished *reviewEvidence
	regular := false
	for i := range evidence {
		e := &evidence[i]
		regular = regular || e.mode == AnalysisModeRegular
		switch {
		case e.comparison == 0:
		case e.state != ComparisonComplete:
			if unfinished == nil {
				unfinished = e
			}
		case e.cli != cli:
			if otherCLI == nil {
				otherCLI = e
			}
		case e.compared == 0:
			if nothing == nil {
				nothing = e
			}
		default:
			return ""
		}
	}
	switch {
	case otherCLI != nil:
		return fmt.Sprintf("the comparison of run %d is of results CodeQL %s produced, and this "+
			"server runs %s; run it again and compare that", otherCLI.run, cliText(otherCLI.cli), cli)
	case nothing != nil:
		return fmt.Sprintf("the comparison of run %d compared no repository, so it says nothing "+
			"about the alerts this version would add", nothing.run)
	case unfinished != nil:
		return fmt.Sprintf("the comparison of run %d is %s; compare the run again",
			unfinished.run, unfinished.state)
	case !regular:
		return fmt.Sprintf("run %d of this version analyzed %s databases, which a comparison does "+
			"not read; run it over regular databases, then compare it", evidence[0].run, evidence[0].mode)
	}
	return fmt.Sprintf("run %d of this version has not been compared with the alerts code "+
		"scanning holds", evidence[0].run)
}

func cliText(cli string) string {
	if cli == "" {
		return "nobody recorded"
	}
	return cli
}
