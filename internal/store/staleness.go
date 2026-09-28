package store

import "fmt"

// StaleFinding is a finding no tracked run of its owning suite reports any more.
//
// Reported, never marked: `plans/db/finding-lifecycle.md` sequences the tombstone behind a report
// somebody has read, because a sweep whose scope is only visible after it runs is one nobody will
// run twice.
type StaleFinding struct {
	RowID       int64
	Repository  string
	RuleID      string
	FilePath    string
	StartLine   int
	Fingerprint string
}

// StalenessReport says what it looked at before it says what it found.
//
// The counts are not decoration. No sighting exists for any finding stored before the relation
// did, so a reader has to be able to tell "nothing is stale" from "nothing has been re-analyzed
// yet" — and on a corpus mid-recrawl the answer is the second one.
type StalenessReport struct {
	// Sightings is every finding-to-run row this report can draw on: a run of the repository's
	// own checkout. A closure run looked at another database, so what it did or did not report
	// is not evidence about a finding regular analysis found.
	Sightings int
	// RepositoriesWithSightings counts repositories where some tracked run attributed something,
	// which is the population the sweep is entitled to draw a conclusion about.
	RepositoriesWithSightings int
	// FindingsWithoutSighting counts findings nothing has yet claimed to have reported. They are
	// never stale: absence in a run that could not have reported it is not absence.
	FindingsWithoutSighting int
	Stale                   []StaleFinding
}

// StaleFindings reports findings that no run of their owning suite reports at any tracked commit.
//
// Regular analysis throughout: the report is drawn over `abom_finding_regular`, and only runs of
// that shape are evidence about it. An extended run reads a different database, so its silence
// about a finding the repository's own checkout reports says nothing at all.
//
// A run that sighted nothing is excluded. It is indistinguishable from a run whose sightings were
// never recorded, so it is not evidence of absence.
func (s *Store) StaleFindings() (StalenessReport, error) {
	var out StalenessReport
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM abom_finding_sighting sg
		JOIN abom_query_run q ON q.row_id = sg.query_run_id
		WHERE q.analysis_mode = 'regular'`).Scan(&out.Sightings); err != nil {
		return out, fmt.Errorf("counting sightings: %w", err)
	}
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM abom_finding_regular f
		WHERE NOT EXISTS (
			SELECT 1 FROM abom_finding_sighting sg
			JOIN abom_query_run q ON q.row_id = sg.query_run_id
			WHERE sg.finding_row_id = f.row_id AND q.analysis_mode = 'regular')`,
	).Scan(&out.FindingsWithoutSighting); err != nil {
		return out, fmt.Errorf("counting unsighted findings: %w", err)
	}

	rows, err := s.db.Query(`
		WITH
		-- The suite that owns a rule. A finding is only ever judged by runs of its own suite, so
		-- a repository analyzed by the graph suite alone loses nothing.
		owning AS (
			SELECT DISTINCT sq.suite, sq.query_id FROM abom_suite_query sq
		),
		-- The newest run per repository, commit and query. Older runs of the same commit are
		-- superseded; runs at other commits are not, which is the whole correction this makes. A held
		-- evaluation sights only what somebody converted, so it is never the newest word.
		newest AS (
			SELECT r.row_id, r.repository, o.suite
			FROM abom_query_run r
			JOIN owning o ON o.query_id = r.query_id
			WHERE r.db_commit IS NOT NULL
			  AND r.analysis_mode = 'regular'
			  AND r.row_id = (
				SELECT MAX(r2.row_id) FROM abom_query_run r2
				WHERE r2.repository = r.repository
				  AND r2.db_commit = r.db_commit
				  AND r2.query_id = r.query_id
				  AND r2.analysis_mode = 'regular'
				  AND r2.held = 0)
		),
		-- A run that attributed nothing is not evidence that anything went away.
		attributing AS (
			SELECT n.row_id, n.repository, n.suite FROM newest n
			WHERE EXISTS (
				SELECT 1 FROM abom_finding_sighting sg WHERE sg.query_run_id = n.row_id)
		)
		SELECT f.row_id, rp.repository_full_name, f.rule_id, COALESCE(f.file_path, ''),
		       COALESCE(f.start_line, 0), f.fingerprint
		FROM abom_finding_regular f
		JOIN repository rp ON rp.row_id = f.repository_row_id
		JOIN owning o ON o.query_id = f.rule_id
		WHERE EXISTS (
			SELECT 1 FROM attributing a
			WHERE a.repository = rp.repository_full_name AND a.suite = o.suite)
		  AND NOT EXISTS (
			SELECT 1 FROM abom_finding_sighting sg
			JOIN attributing a ON a.row_id = sg.query_run_id
			WHERE sg.finding_row_id = f.row_id AND a.suite = o.suite)
		  -- Somebody converted it from what a run held, which is a report nothing supersedes.
		  AND NOT EXISTS (
			SELECT 1 FROM abom_finding_sighting sg
			JOIN abom_query_run h ON h.row_id = sg.query_run_id
			WHERE sg.finding_row_id = f.row_id AND h.held = 1 AND h.analysis_mode = 'regular')
		ORDER BY rp.repository_full_name, f.rule_id, f.file_path, f.start_line`)
	if err != nil {
		return out, fmt.Errorf("reading stale findings: %w", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var f StaleFinding
		if err := rows.Scan(&f.RowID, &f.Repository, &f.RuleID, &f.FilePath, &f.StartLine,
			&f.Fingerprint); err != nil {
			return out, fmt.Errorf("scanning a stale finding: %w", err)
		}
		out.Stale = append(out.Stale, f)
		seen[f.Repository] = true
	}
	if err := rows.Err(); err != nil {
		return out, err
	}

	if err := s.db.QueryRow(`
		WITH newest AS (
			SELECT r.row_id, r.repository
			FROM abom_query_run r
			WHERE r.db_commit IS NOT NULL
			  AND r.analysis_mode = 'regular'
			  AND r.row_id = (
				SELECT MAX(r2.row_id) FROM abom_query_run r2
				WHERE r2.repository = r.repository
				  AND r2.db_commit = r.db_commit
				  AND r2.query_id = r.query_id
				  AND r2.analysis_mode = 'regular'
				  AND r2.held = 0)
		)
		SELECT COUNT(DISTINCT n.repository) FROM newest n
		WHERE EXISTS (SELECT 1 FROM abom_finding_sighting sg WHERE sg.query_run_id = n.row_id)`,
	).Scan(&out.RepositoriesWithSightings); err != nil {
		return out, fmt.Errorf("counting repositories with sightings: %w", err)
	}
	return out, nil
}
