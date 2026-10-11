package store

import (
	"database/sql"
	"fmt"
	"strings"
)

// checkDDL holds the checker's results, which nothing that walks, derives or reads the graph reads.
const checkDDL = `
-- One run of one check by one build: the stamped version, its revision and the reach mark version.
CREATE TABLE IF NOT EXISTS abom_check_run (
  row_id     INTEGER PRIMARY KEY,
  check_no   INTEGER NOT NULL,
  build      TEXT NOT NULL,
  started_at TEXT NOT NULL,
  wall_ms    INTEGER NOT NULL DEFAULT 0
);

-- Each comparison a run made. disagreed is NULL when nothing was compared: unread is not agreement.
-- population is NULL for a part that read everything it could, and bound unless it drew at random.
CREATE TABLE IF NOT EXISTS abom_check_part (
  run_row_id INTEGER NOT NULL REFERENCES abom_check_run(row_id),
  check_no   INTEGER NOT NULL,
  part       TEXT NOT NULL,
  compared   INTEGER NOT NULL,
  disagreed  INTEGER,
  population INTEGER,
  bound      REAL,
  PRIMARY KEY (run_row_id, part)
);

CREATE INDEX IF NOT EXISTS idx_check_part_latest ON abom_check_part(check_no, part, run_row_id);

-- What a part did not compare, and why.
CREATE TABLE IF NOT EXISTS abom_check_skip (
  run_row_id INTEGER NOT NULL REFERENCES abom_check_run(row_id),
  part       TEXT NOT NULL,
  reason     TEXT NOT NULL,
  count      INTEGER NOT NULL,
  PRIMARY KEY (run_row_id, part, reason)
) WITHOUT ROWID;

-- The queue: one row however many runs saw it, so first_build says when it started. A sampled row
-- closes only when a later run read its subject again and agreed, recorded in settled_run_row_id.
CREATE TABLE IF NOT EXISTS abom_check_disagreement (
  row_id           INTEGER PRIMARY KEY,
  check_no         INTEGER NOT NULL,
  part             TEXT NOT NULL,
  subject          TEXT NOT NULL,
  reading          TEXT NOT NULL,
  class            TEXT NOT NULL,
  claim            TEXT NOT NULL DEFAULT '',
  independent      TEXT NOT NULL DEFAULT '',
  first_build      TEXT NOT NULL,
  first_seen_at    TEXT NOT NULL,
  first_run_row_id INTEGER NOT NULL,
  last_build       TEXT NOT NULL,
  last_seen_at     TEXT NOT NULL,
  last_run_row_id  INTEGER NOT NULL,
  sampled          INTEGER NOT NULL DEFAULT 0,
  settled_run_row_id INTEGER,
  UNIQUE (check_no, subject, reading)
);
`

// MigrateChecks creates only the checker's tables, and adds their newer columns: Migrate writes
// tables a serving process reads.
func (s *Store) MigrateChecks() error {
	if _, err := s.db.Exec(checkDDL); err != nil {
		return fmt.Errorf("creating the check tables: %w", err)
	}
	return s.addColumns(func(table string) bool { return strings.HasPrefix(table, "abom_check_") })
}

// CheckRun is one run of one check by one build.
type CheckRun struct {
	ID         int64       `json:"id"`
	Check      int         `json:"check"`
	Build      string      `json:"build"`
	StartedAt  string      `json:"startedAt"`
	WallMillis int64       `json:"wallMillis"`
	Parts      []CheckPart `json:"parts"`
}

// CheckPart is one named comparison a run made.
type CheckPart struct {
	Part     string `json:"part"`
	Compared int    `json:"compared"`
	// Disagreed is null when nothing was compared: unread is not agreement.
	Disagreed *int        `json:"disagreed"`
	Skipped   []CheckSkip `json:"skipped"`
	// Population is how many subjects a sampled part was drawn from; null for a part that read
	// everything it could, which is not a sample.
	Population *int `json:"population"`
	// Bound is the most of the population that can disagree at 95% confidence, given what the part
	// compared; null unless the part's subjects were drawn at random.
	Bound *float64 `json:"bound"`
	// Disagreements go to the queue, which is where a reader finds them.
	Disagreements []Disagreement `json:"-"`
	// Agreed are the subjects a sampled part read and found agreeing, which is what settles one of
	// its disagreements: a sample that did not read a subject again says nothing about it.
	Agreed []CheckSubject `json:"-"`
}

// CheckSubject names what one disagreement is about, as the queue keys it within a check.
type CheckSubject struct {
	Subject string `json:"subject"`
	Reading string `json:"reading"`
}

// CheckSkip is how many comparisons a part did not make, and why.
type CheckSkip struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// Disagreement is one claim an independent reading contradicts, with what reproduces it.
type Disagreement struct {
	Check   int    `json:"check"`
	Part    string `json:"part"`
	Subject string `json:"subject"`
	// Reading names the table or reading compared; check, subject and reading identify it.
	Reading string `json:"reading"`
	// Class is assigned by rule, never by judgement.
	Class       string `json:"class"`
	Claim       string `json:"claim"`
	Independent string `json:"independent"`
	FirstBuild  string `json:"firstBuild"`
	FirstSeenAt string `json:"firstSeenAt"`
	LastBuild   string `json:"lastBuild"`
	LastSeenAt  string `json:"lastSeenAt"`
}

// DisagreementPage is one page of the open queue and how many it was cut from.
type DisagreementPage struct {
	Items []Disagreement `json:"items"`
	Total int            `json:"total"`
}

// CheckTrendPoint is one check under one build: its runs, last counts, and what it first saw.
type CheckTrendPoint struct {
	Check     int    `json:"check"`
	Build     string `json:"build"`
	Runs      int    `json:"runs"`
	FirstRun  string `json:"firstRun"`
	LastRun   string `json:"lastRun"`
	Compared  int    `json:"compared"`
	Disagreed *int   `json:"disagreed"`
	Skipped   int    `json:"skipped"`
	Started   int    `json:"started"`
}

// RecordCheckRun stores a run, its parts and skips, and files each disagreement into the queue.
func (s *Store) RecordCheckRun(run CheckRun) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("recording a check run: %w", err)
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRow(`INSERT INTO abom_check_run (check_no, build, started_at, wall_ms)
		VALUES (?, ?, ?, ?) RETURNING row_id`, run.Check, run.Build, run.StartedAt,
		run.WallMillis).Scan(&id); err != nil {
		return 0, fmt.Errorf("recording check %d's run: %w", run.Check, err)
	}
	for _, p := range run.Parts {
		var disagreed sql.NullInt64
		if p.Compared > 0 || len(p.Disagreements) > 0 {
			disagreed = sql.NullInt64{Int64: int64(len(p.Disagreements)), Valid: true}
		}
		if _, err := tx.Exec(`INSERT INTO abom_check_part (run_row_id, check_no, part, compared,
			disagreed, population, bound) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, run.Check, p.Part,
			p.Compared, disagreed, p.Population, p.Bound); err != nil {
			return 0, fmt.Errorf("recording part %q of check %d: %w", p.Part, run.Check, err)
		}
		for _, k := range p.Skipped {
			if _, err := tx.Exec(`INSERT INTO abom_check_skip (run_row_id, part, reason, count)
				VALUES (?, ?, ?, ?) ON CONFLICT(run_row_id, part, reason) DO UPDATE
				SET count = abom_check_skip.count + excluded.count`,
				id, p.Part, k.Reason, k.Count); err != nil {
				return 0, fmt.Errorf("recording what part %q skipped: %w", p.Part, err)
			}
		}
		for _, d := range p.Disagreements {
			if _, err := tx.Exec(`INSERT INTO abom_check_disagreement (check_no, part, subject,
				reading, class, claim, independent, first_build, first_seen_at, first_run_row_id,
				last_build, last_seen_at, last_run_row_id, sampled)
				VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8, ?9, ?10, ?8, ?9, ?10, ?11)
				ON CONFLICT (check_no, subject, reading) DO UPDATE SET
				  part = excluded.part, class = excluded.class, claim = excluded.claim,
				  independent = excluded.independent, last_build = excluded.last_build,
				  last_seen_at = excluded.last_seen_at, last_run_row_id = excluded.last_run_row_id,
				  sampled = excluded.sampled`,
				run.Check, p.Part, d.Subject, d.Reading, d.Class, d.Claim, d.Independent,
				run.Build, run.StartedAt, id, p.Population != nil); err != nil {
				return 0, fmt.Errorf("filing a disagreement about %q: %w", d.Subject, err)
			}
		}
		for _, a := range p.Agreed {
			if _, err := tx.Exec(`UPDATE abom_check_disagreement SET settled_run_row_id = ?
				WHERE check_no = ? AND subject = ? AND reading = ? AND sampled = 1
				  AND last_run_row_id < ?`, id, run.Check, a.Subject, a.Reading, id); err != nil {
				return 0, fmt.Errorf("settling the disagreement about %q: %w", a.Subject, err)
			}
		}
	}
	return id, tx.Commit()
}

// LatestCheckRuns reads each check's most recent run, by check number.
func (s *Store) LatestCheckRuns() (map[int]CheckRun, error) {
	runs, err := queryAll(s, "reading the latest check runs", func(r *sql.Rows) (CheckRun, error) {
		var c CheckRun
		return c, r.Scan(&c.ID, &c.Check, &c.Build, &c.StartedAt, &c.WallMillis)
	}, `SELECT row_id, check_no, build, started_at, wall_ms FROM abom_check_run
	     WHERE row_id IN (SELECT MAX(row_id) FROM abom_check_run GROUP BY check_no)`)
	if err != nil {
		return nil, err
	}
	out := make(map[int]CheckRun, len(runs))
	for _, run := range runs {
		if run.Parts, err = s.checkParts(run.ID); err != nil {
			return nil, err
		}
		out[run.Check] = run
	}
	return out, nil
}

// checkParts reads one run's parts with what each skipped, in the order they were recorded.
func (s *Store) checkParts(run int64) ([]CheckPart, error) {
	parts, err := queryAll(s, "reading a check run's parts", func(r *sql.Rows) (CheckPart, error) {
		p := CheckPart{Skipped: []CheckSkip{}}
		var disagreed, population sql.NullInt64
		var bound sql.NullFloat64
		if err := r.Scan(&p.Part, &p.Compared, &disagreed, &population, &bound); err != nil {
			return p, err
		}
		if disagreed.Valid {
			n := int(disagreed.Int64)
			p.Disagreed = &n
		}
		if population.Valid {
			n := int(population.Int64)
			p.Population = &n
		}
		if bound.Valid {
			p.Bound = &bound.Float64
		}
		return p, nil
	}, `SELECT part, compared, disagreed, population, bound FROM abom_check_part
	     WHERE run_row_id = ? ORDER BY rowid`, run)
	if err != nil {
		return nil, err
	}
	for i := range parts {
		if parts[i].Skipped, err = queryAll(s, "reading what a check part skipped",
			func(r *sql.Rows) (CheckSkip, error) {
				var k CheckSkip
				return k, r.Scan(&k.Reason, &k.Count)
			}, `SELECT reason, count FROM abom_check_skip WHERE run_row_id = ? AND part = ?
			     ORDER BY reason`, run, parts[i].Part); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

// CheckTrend reads, per check and build in the order each build first ran it, its last run there.
func (s *Store) CheckTrend() ([]CheckTrendPoint, error) {
	return queryAll(s, "reading the per-build trend", func(r *sql.Rows) (CheckTrendPoint, error) {
		var p CheckTrendPoint
		var disagreed sql.NullInt64
		if err := r.Scan(&p.Check, &p.Build, &p.Runs, &p.FirstRun, &p.LastRun, &p.Compared,
			&disagreed, &p.Skipped, &p.Started); err != nil {
			return p, err
		}
		if disagreed.Valid {
			n := int(disagreed.Int64)
			p.Disagreed = &n
		}
		return p, nil
	}, `
		WITH per_build AS (
			SELECT check_no, build, COUNT(*) AS runs, MIN(started_at) AS first_run,
			       MAX(started_at) AS last_run, MAX(row_id) AS last_row_id, MIN(row_id) AS first_row_id
			  FROM abom_check_run GROUP BY check_no, build
		)
		SELECT b.check_no, b.build, b.runs, b.first_run, b.last_run,
		       COALESCE((SELECT SUM(p.compared) FROM abom_check_part p
		                  WHERE p.run_row_id = b.last_row_id), 0),
		       (SELECT SUM(p.disagreed) FROM abom_check_part p WHERE p.run_row_id = b.last_row_id),
		       COALESCE((SELECT SUM(k.count) FROM abom_check_skip k
		                  WHERE k.run_row_id = b.last_row_id), 0),
		       (SELECT COUNT(*) FROM abom_check_disagreement d
		         WHERE d.check_no = b.check_no AND d.first_build = b.build)
		  FROM per_build b
		 ORDER BY b.check_no, b.first_row_id`)
}

// openDisagreement: no later run comparing the part stopped seeing it; a skip closes nothing. A
// sampled part reads only some of its subjects, so its disagreement stays open until a later run
// read that subject again and agreed.
const openDisagreement = `CASE WHEN d.sampled = 1
	THEN COALESCE(d.settled_run_row_id, 0) < d.last_run_row_id
	ELSE d.last_run_row_id >= (
	SELECT MAX(p.run_row_id) FROM abom_check_part p
	 WHERE p.check_no = d.check_no AND p.part = d.part AND p.disagreed IS NOT NULL) END`

// OpenDisagreements lists one page of the open queue, by check and then by when each started.
func (s *Store) OpenDisagreements(offset, limit int) (DisagreementPage, error) {
	out := DisagreementPage{Items: []Disagreement{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_check_disagreement d WHERE ` +
		openDisagreement).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("counting open disagreements: %w", err)
	}
	items, err := queryAll(s, "listing open disagreements", func(r *sql.Rows) (Disagreement, error) {
		var d Disagreement
		return d, r.Scan(&d.Check, &d.Part, &d.Subject, &d.Reading, &d.Class, &d.Claim,
			&d.Independent, &d.FirstBuild, &d.FirstSeenAt, &d.LastBuild, &d.LastSeenAt)
	}, `SELECT check_no, part, subject, reading, class, claim, independent, first_build,
	           first_seen_at, last_build, last_seen_at
	      FROM abom_check_disagreement d WHERE `+openDisagreement+`
	     ORDER BY check_no, first_run_row_id, part, subject, reading LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return out, err
	}
	out.Items = items
	return out, nil
}

// OpenSubjects lists what a check's open disagreements are about, which a sampled check reads again.
func (s *Store) OpenSubjects(check int) ([]CheckSubject, error) {
	return queryAll(s, "listing a check's open disagreements", func(r *sql.Rows) (CheckSubject, error) {
		var c CheckSubject
		return c, r.Scan(&c.Subject, &c.Reading)
	}, `SELECT d.subject, d.reading FROM abom_check_disagreement d
	     WHERE d.check_no = ? AND `+openDisagreement+`
	     ORDER BY d.first_run_row_id, d.subject, d.reading`, check)
}
