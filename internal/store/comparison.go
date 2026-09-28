package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// comparisonDDL keeps a run's results set against the code scanning alerts GitHub held when the
// comparison was made.
//
// Self-contained on purpose: a held run analyzed again replaces what it held, and a comparison
// that only pointed at those rows would change after it was written down, which is the one
// thing a comparison kept to be checked afterwards must not do.
const comparisonDDL = `
CREATE TABLE IF NOT EXISTS abom_comparison (
  row_id       INTEGER PRIMARY KEY,
  run_id       INTEGER NOT NULL REFERENCES abom_analysis_run(row_id),
  requested_by TEXT NOT NULL,
  state        TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  finished_at  TEXT,
  -- The CodeQL the run's results came from, copied because starting the run again rewrites it.
  cli_version  TEXT NOT NULL DEFAULT ''
);

-- One row per run member, whatever became of it. A member nothing compared is named with why,
-- because a delta over an unnamed subset is quoted as if it covered the scope.
CREATE TABLE IF NOT EXISTS abom_comparison_member (
  comparison_id   INTEGER NOT NULL REFERENCES abom_comparison(row_id),
  repository      TEXT NOT NULL,
  analyzed_commit TEXT NOT NULL DEFAULT '',
  source_filter   TEXT NOT NULL DEFAULT '',
  status          TEXT NOT NULL,
  reason          TEXT NOT NULL DEFAULT '',
  detail          TEXT NOT NULL DEFAULT '',
  -- How many results the run had here. NULL where the run counted none, which is not zero.
  results         INTEGER,
  -- The commit of the newest Actions analysis GitHub held, which is what its alerts describe.
  -- NULL where none was read.
  alerted_commit  TEXT,
  alerted_at      TEXT,
  -- The CodeQL that analysis ran. The same commit under another CodeQL is another answer: measured,
  -- a year-old 2.23.1 analysis and a 2.27.1 run of one query disagreed by 7 results.
  alerted_cli     TEXT,
  -- The default branch as it was read when compared. NULL where it could not be read, which is
  -- why staleness is then unknown rather than current.
  head_commit     TEXT,
  staleness       TEXT NOT NULL DEFAULT '',
  -- Commits the default branch has that the analyzed commit lacks. NULL where nobody counted.
  behind_by       INTEGER,
  read_at         TEXT,
  PRIMARY KEY (comparison_id, repository, analyzed_commit, source_filter)
);

-- Each result compared, and the alert that already stands for it. NULL alert_number is a result
-- no alert stands for: what adopting the query would add.
CREATE TABLE IF NOT EXISTS abom_comparison_result (
  row_id          INTEGER PRIMARY KEY,
  comparison_id   INTEGER NOT NULL REFERENCES abom_comparison(row_id),
  repository      TEXT NOT NULL,
  analyzed_commit TEXT NOT NULL DEFAULT '',
  source_filter   TEXT NOT NULL DEFAULT '',
  rule_id         TEXT NOT NULL,
  file_path       TEXT NOT NULL DEFAULT '',
  start_line      INTEGER NOT NULL DEFAULT 0,
  start_column    INTEGER NOT NULL DEFAULT 0,
  message         TEXT NOT NULL DEFAULT '',
  alert_number    INTEGER,
  alert_state     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_comparison_result_member
  ON abom_comparison_result (comparison_id, repository, analyzed_commit);

-- How many alerts GitHub held for each rule the run named, by state, so the alerts a run did not
-- report are counted rather than lost.
CREATE TABLE IF NOT EXISTS abom_comparison_alerts (
  comparison_id   INTEGER NOT NULL REFERENCES abom_comparison(row_id),
  repository      TEXT NOT NULL,
  analyzed_commit TEXT NOT NULL DEFAULT '',
  source_filter   TEXT NOT NULL DEFAULT '',
  rule_id         TEXT NOT NULL,
  state           TEXT NOT NULL,
  alerts          INTEGER NOT NULL,
  PRIMARY KEY (comparison_id, repository, analyzed_commit, source_filter, rule_id, state)
);`

// What became of a comparison, and of each repository in it.
const (
	ComparisonRunning     = "running"
	ComparisonComplete    = "complete"
	ComparisonInterrupted = "interrupted"

	ComparisonPending     = "pending"
	ComparisonCompared    = "compared"
	ComparisonNotCompared = "not_compared"
)

// Why a member was not compared. Each is a different thing somebody could do next.
const (
	// ReasonNotAnalyzed is a member the run has no results for.
	ReasonNotAnalyzed = "not_analyzed"
	// ReasonNoCodeScanning is a repository code scanning holds no analysis for.
	ReasonNoCodeScanning = "no_code_scanning"
	// ReasonNoActionsAnalysis is one whose code scanning holds no Actions analysis ABOM could
	// recognize, so nothing the run found there would be raised.
	ReasonNoActionsAnalysis = "no_actions_analysis"
	// ReasonUnread is one whose alerts or analyses GitHub did not give.
	ReasonUnread = "unread"
	// ReasonUnlinked is a findings member counted before members kept their evaluation, which has
	// a count and nothing to list it from.
	ReasonUnlinked = "unlinked"
)

// How the commit analyzed relates to the default branch when the comparison read it.
const (
	StalenessCurrent  = "current"
	StalenessBehind   = "behind"
	StalenessAhead    = "ahead"
	StalenessDiverged = "diverged"
	// StalenessMoved is a default branch at another commit whose relation could not be counted.
	StalenessMoved   = "moved"
	StalenessUnknown = "unknown"
)

// What one result is, set against the alerts.
const (
	MatchOpen      = "open"
	MatchDismissed = "dismissed"
	MatchReopens   = "reopens"
	MatchNew       = "new"
)

// The statements every comparison carries, because a number read without them says more than
// was measured.
const (
	comparisonCondition = "Alerts appear only where Actions analysis is running. A repository " +
		"with no Actions analysis raises no alert however often a query fires, so those are " +
		"named and not counted; that one ran is read from the analyses code scanning holds, " +
		"not from its settings."
	comparisonMerge = "A pack added through the github-codeql-config-file property is merged " +
		"with the configuration Default Setup generates, so what appears is the union of what " +
		"each repository already runs and the pack. New is counted over every CodeQL alert the " +
		"repository holds, whatever produced it, and a result one of them already stands for adds nothing."
	comparisonBoundary = "Exact for the commit analyzed, and an inference beyond it. Where the " +
		"commit or the CodeQL analyzed is not what GitHub last analyzed with, or the default " +
		"branch has moved past it, the count is an extrapolation, and each repository says which."
)

// ErrComparisonRefused is a run a comparison cannot be made of, with the reason.
var ErrComparisonRefused = errors.New("refused")

// ErrNoComparison is a comparison the store does not hold.
var ErrNoComparison = errors.New("no such comparison")

// ComparedResult is one result a run reported, and the alert that already stands for it.
type ComparedResult struct {
	RuleID      string `json:"ruleId"`
	FilePath    string `json:"filePath"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	Message     string `json:"message"`
	// AlertNumber is zero where no alert stands for the result; AlertState is then empty.
	AlertNumber int    `json:"alertNumber"`
	AlertState  string `json:"alertState"`
	// Match is what the alert makes of the result, derived from its state.
	Match string `json:"match"`
}

// RuleAlerts is how many alerts GitHub held for one rule in one state.
type RuleAlerts struct {
	RuleID string
	State  string
	Alerts int
}

// ComparisonRecord is what reading one member established.
type ComparisonRecord struct {
	DatabaseIdentity
	Status string
	Reason string
	Detail string
	// Results is every result compared, for a compared member; Counted then says the run had
	// ResultCount results for a member that was not compared.
	Results     []ComparedResult
	ResultCount int
	Counted     bool
	Alerts      []RuleAlerts

	AlertedCommit string
	AlertedAt     string
	AlertedCLI    string
	HeadCommit    string
	Staleness     string
	BehindBy      *int
}

// ComparisonCounts is what a set of results is, against the alerts.
type ComparisonCounts struct {
	Results int `json:"results"`
	// Open and Dismissed are results an alert already stands for; neither adds anything.
	Open      int `json:"open"`
	Dismissed int `json:"dismissed"`
	// Reopens are results whose alert was fixed, which a developer then sees again.
	Reopens int `json:"reopens"`
	New     int `json:"new"`
	// Adds is what a developer would see appear: new and reopened.
	Adds int `json:"adds"`
	// OpenAlerts is what GitHub held open for the rules compared, and Missed the ones of those no
	// result stood for, which stay because something the repository already runs raised them.
	OpenAlerts int `json:"openAlerts"`
	Missed     int `json:"missed"`
}

func (c *ComparisonCounts) add(o ComparisonCounts) {
	c.Results += o.Results
	c.Open += o.Open
	c.Dismissed += o.Dismissed
	c.Reopens += o.Reopens
	c.New += o.New
	c.Adds += o.Adds
	c.OpenAlerts += o.OpenAlerts
	c.Missed += o.Missed
}

// ComparisonRule is one rule's counts.
type ComparisonRule struct {
	RuleID string `json:"ruleId"`
	ComparisonCounts
}

// ComparisonMember is one repository in a comparison.
type ComparisonMember struct {
	Repository     string `json:"repository"`
	AnalyzedCommit string `json:"analyzedCommit"`
	SourceFilter   string `json:"sourceFilter"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	Detail         string `json:"detail"`
	// Counted says the run had Counts.Results results here; the rest of Counts is meaningful for a
	// compared member alone.
	Counted bool             `json:"counted"`
	Counts  ComparisonCounts `json:"counts"`
	Rules   []ComparisonRule `json:"rules"`

	AlertedCommit string `json:"alertedCommit"`
	AlertedAt     string `json:"alertedAt"`
	// AlertedCLI is the CodeQL GitHub's analysis ran, empty where it was not named.
	AlertedCLI string `json:"alertedCli"`
	HeadCommit string `json:"headCommit"`
	Staleness  string `json:"staleness"`
	BehindBy   *int   `json:"behindBy"`
	// Exact is a member analyzed at the commit, and under the CodeQL, that GitHub's alerts
	// describe: a measurement, where any other is an inference.
	Exact  bool   `json:"exact"`
	ReadAt string `json:"readAt"`
}

// Comparison is a run's results set against the alerts, member by member.
type Comparison struct {
	RowID       int64           `json:"rowId"`
	RunID       int64           `json:"runId"`
	RequestedBy string          `json:"requestedBy"`
	State       string          `json:"state"`
	CreatedAt   string          `json:"createdAt"`
	FinishedAt  string          `json:"finishedAt"`
	CLIVersion  string          `json:"cliVersion"`
	Queries     []AnalysisQuery `json:"queries"`

	Condition string `json:"condition"`
	Merge     string `json:"merge"`
	Boundary  string `json:"boundary"`

	Members []ComparisonMember `json:"members"`
	// Totals and Rules span the compared members, and nothing else.
	Totals      ComparisonCounts `json:"totals"`
	Rules       []ComparisonRule `json:"rules"`
	Compared    int              `json:"compared"`
	NotCompared int              `json:"notCompared"`
	Pending     int              `json:"pending"`
}

// CreateComparison records that somebody asked for a run's results to be set against the alerts,
// with every member of the run, and refuses a run whose results Default Setup would never see.
func (s *Store) CreateComparison(runID int64, by string) (int64, error) {
	if by == "" {
		return 0, fmt.Errorf("comparing run %d: no one is recorded as asking for it", runID)
	}
	run, err := s.AnalysisRun(runID)
	if err != nil {
		return 0, err
	}
	if err := validateComparisonRun(runID, run); err != nil {
		return 0, err
	}

	unlinked, err := s.unlinkedMembers(runID, run.Held)
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	id, err := insertComparison(tx, runID, by, run.CLIVersion)
	if err != nil {
		return 0, err
	}
	if err := insertComparisonMembers(tx, id, run.Members, unlinked); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing the comparison of run %d: %w", runID, err)
	}
	return id, nil
}

func validateComparisonRun(runID int64, run AnalysisRun) error {
	switch {
	case run.Mode != AnalysisModeRegular:
		return fmt.Errorf("run %d analyzed %s databases, and Default Setup builds one "+
			"repository's own checkout, so a comparison reads a regular run only: %w",
			runID, run.Mode, ErrComparisonRefused)
	case run.State == RunPending || run.State == RunRunning:
		return fmt.Errorf("run %d is still %s, and a comparison of results about to change "+
			"would describe nothing: %w", runID, run.State, ErrComparisonRefused)
	case countedComparisonMembers(run.Members) == 0:
		return fmt.Errorf("run %d has results for no repository, so there is nothing to "+
			"compare: %w", runID, ErrComparisonRefused)
	}
	return nil
}

func countedComparisonMembers(members []AnalysisMember) int {
	counted := 0
	for _, m := range members {
		if m.Counted {
			counted++
		}
	}
	return counted
}

func insertComparison(tx *sql.Tx, runID int64, by, cliVersion string) (int64, error) {
	res, err := tx.Exec(`
		INSERT INTO abom_comparison (run_id, requested_by, state, created_at, cli_version)
		VALUES (?, ?, ?, ?, ?)`,
		runID, by, ComparisonRunning, time.Now().UTC().Format(time.RFC3339), cliVersion)
	if err != nil {
		return 0, fmt.Errorf("recording the comparison of run %d: %w", runID, err)
	}
	// A plain INSERT, so LastInsertId is the row it just wrote.
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading back the comparison of run %d: %w", runID, err)
	}
	return id, nil
}

func insertComparisonMembers(
	tx *sql.Tx, id int64, members []AnalysisMember, unlinked map[DatabaseIdentity]bool,
) error {
	for _, m := range members {
		status, reason, detail, results := comparisonMemberRecord(m, unlinked[m.DatabaseIdentity])
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO abom_comparison_member
				(comparison_id, repository, analyzed_commit, source_filter, status, reason, detail, results)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, m.Repository, m.Commit, m.SourceFilter, status, reason, detail, results); err != nil {
			return fmt.Errorf("recording %s in the comparison: %w", m.Repository, err)
		}
	}
	return nil
}

func comparisonMemberRecord(m AnalysisMember, unlinked bool) (string, string, string, any) {
	status, reason, detail := ComparisonPending, "", ""
	var results any
	switch {
	case !m.Counted:
		status, reason = ComparisonNotCompared, ReasonNotAnalyzed
		detail = fmt.Sprintf("the run has no results here: %s", memberStatusText(m))
	case unlinked:
		status, reason, results = ComparisonNotCompared, ReasonUnlinked, m.Results
		detail = "the run counted its results here before it kept which evaluation they came " +
			"from, so there is nothing to list them from; run it again to compare them"
	default:
		results = m.Results
	}
	return status, reason, detail, results
}

// unlinkedMembers is a findings run's counted members that name no evaluation. A held run lists
// what it kept whether or not one was named, so it has none.
func (s *Store) unlinkedMembers(runID int64, held bool) (map[DatabaseIdentity]bool, error) {
	out := map[DatabaseIdentity]bool{}
	if held {
		return out, nil
	}
	rows, err := s.db.Query(`
		SELECT repository, db_commit, source_filter, analysis_mode FROM abom_analysis_member
		WHERE run_id = ? AND query_run_id IS NULL AND result_count IS NOT NULL`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading the unlinked members of run %d: %w", runID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var d DatabaseIdentity
		if err := rows.Scan(&d.Repository, &d.Commit, &d.SourceFilter, &d.Mode); err != nil {
			return nil, fmt.Errorf("scanning an unlinked member: %w", err)
		}
		out[d] = true
	}
	return out, rows.Err()
}

func memberStatusText(m AnalysisMember) string {
	if m.Detail != "" {
		return m.Status + ", " + m.Detail
	}
	return m.Status
}

// PendingComparisonMembers lists the members a comparison has still to read.
func (s *Store) PendingComparisonMembers(id int64) ([]DatabaseIdentity, error) {
	rows, err := s.db.Query(`
		SELECT repository, analyzed_commit, source_filter FROM abom_comparison_member
		WHERE comparison_id = ? AND status = ?
		ORDER BY repository, analyzed_commit, source_filter`, id, ComparisonPending)
	if err != nil {
		return nil, fmt.Errorf("listing what comparison %d has to read: %w", id, err)
	}
	defer rows.Close()
	out := []DatabaseIdentity{}
	for rows.Next() {
		d := DatabaseIdentity{Mode: AnalysisModeRegular}
		if err := rows.Scan(&d.Repository, &d.Commit, &d.SourceFilter); err != nil {
			return nil, fmt.Errorf("scanning a comparison member: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MemberResults lists every result one member of a run reported, from what a held run kept or
// from the sightings of a findings member's own evaluation.
func (s *Store) MemberResults(runID int64, id DatabaseIdentity) ([]ComparedResult, error) {
	var held bool
	if err := s.db.QueryRow(`SELECT held FROM abom_analysis_run WHERE row_id = ?`, runID).
		Scan(&held); err != nil {
		return nil, fmt.Errorf("reading run %d: %w", runID, err)
	}
	query := `
		SELECT f.rule_id, COALESCE(f.file_path, ''), COALESCE(f.start_line, 0),
		       COALESCE(f.start_column, 0), COALESCE(f.message, '')
		FROM abom_analysis_member m
		JOIN abom_finding_sighting sg ON sg.query_run_id = m.query_run_id
		JOIN abom_finding_any_mode f ON f.row_id = sg.finding_row_id
		WHERE m.run_id = ? AND m.repository = ? AND m.db_commit = ?
		  AND m.source_filter = ? AND m.analysis_mode = ?
		ORDER BY f.file_path, f.start_line, f.start_column, f.rule_id, f.row_id`
	if held {
		query = `
		SELECT rule_id, file_path, start_line, start_column, message FROM abom_held_result
		WHERE run_id = ? AND repository = ? AND db_commit = ? AND source_filter = ?
		  AND analysis_mode = ?
		ORDER BY file_path, start_line, start_column, rule_id, row_id`
	}
	rows, err := s.db.Query(query, runID, id.Repository, id.Commit, id.SourceFilter, id.Mode)
	if err != nil {
		return nil, fmt.Errorf("reading what %s reported in run %d: %w", id.Repository, runID, err)
	}
	defer rows.Close()
	out := []ComparedResult{}
	for rows.Next() {
		var r ComparedResult
		if err := rows.Scan(&r.RuleID, &r.FilePath, &r.StartLine, &r.StartColumn, &r.Message); err != nil {
			return nil, fmt.Errorf("scanning a result of %s: %w", id.Repository, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordComparisonMember keeps what reading one member established, in one transaction so its
// counts and its list always describe the same read. A member is read once.
func (s *Store) RecordComparisonMember(id int64, r ComparisonRecord) error {
	if r.Status != ComparisonCompared && r.Status != ComparisonNotCompared {
		return fmt.Errorf("a compared member is %s or %s, not %q",
			ComparisonCompared, ComparisonNotCompared, r.Status)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	var results any
	switch {
	case r.Status == ComparisonCompared:
		results = len(r.Results)
	case r.Counted:
		results = r.ResultCount
	}
	res, err := tx.Exec(`
		UPDATE abom_comparison_member
		SET status = ?, reason = ?, detail = ?, results = COALESCE(?, results),
		    alerted_commit = ?, alerted_at = ?, alerted_cli = ?, head_commit = ?, staleness = ?,
		    behind_by = ?, read_at = ?
		WHERE comparison_id = ? AND repository = ? AND analyzed_commit = ? AND source_filter = ?
		  AND status = ?`,
		r.Status, r.Reason, r.Detail, results, nullable(r.AlertedCommit), nullable(r.AlertedAt),
		nullable(r.AlertedCLI), nullable(r.HeadCommit), r.Staleness, nullableInt(r.BehindBy),
		time.Now().UTC().Format(time.RFC3339),
		id, r.Repository, r.Commit, r.SourceFilter, ComparisonPending)
	if err != nil {
		return fmt.Errorf("recording %s in comparison %d: %w", r.Repository, id, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		return fmt.Errorf("comparison %d has no member %s at %q still to read", id, r.Repository, r.Commit)
	}
	if err := insertCompared(tx, id, r); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing %s in comparison %d: %w", r.Repository, id, err)
	}
	return nil
}

func insertCompared(tx *sql.Tx, id int64, r ComparisonRecord) error {
	result, err := tx.Prepare(`
		INSERT INTO abom_comparison_result
			(comparison_id, repository, analyzed_commit, source_filter, rule_id, file_path,
			 start_line, start_column, message, alert_number, alert_state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("preparing the results of %s: %w", r.Repository, err)
	}
	defer result.Close()
	for _, c := range r.Results {
		var number any
		if c.AlertNumber != 0 {
			number = c.AlertNumber
		}
		if _, err := result.Exec(id, r.Repository, r.Commit, r.SourceFilter, c.RuleID, c.FilePath,
			c.StartLine, c.StartColumn, c.Message, number, c.AlertState); err != nil {
			return fmt.Errorf("recording a result of %s: %w", r.Repository, err)
		}
	}
	for _, a := range r.Alerts {
		if _, err := tx.Exec(`
			INSERT INTO abom_comparison_alerts
				(comparison_id, repository, analyzed_commit, source_filter, rule_id, state, alerts)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (comparison_id, repository, analyzed_commit, source_filter, rule_id, state)
			DO UPDATE SET alerts = abom_comparison_alerts.alerts + excluded.alerts`,
			id, r.Repository, r.Commit, r.SourceFilter, a.RuleID, a.State, a.Alerts); err != nil {
			return fmt.Errorf("recording the alerts of %s: %w", r.Repository, err)
		}
	}
	return nil
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// FinishComparison records that every member has been read.
func (s *Store) FinishComparison(id int64) error {
	return s.setComparisonState(id, ComparisonComplete)
}

func (s *Store) setComparisonState(id int64, state string) error {
	if _, err := s.db.Exec(`
		UPDATE abom_comparison SET state = ?, finished_at = ? WHERE row_id = ?`,
		state, time.Now().UTC().Format(time.RFC3339), id); err != nil {
		return fmt.Errorf("moving comparison %d to %s: %w", id, state, err)
	}
	return nil
}

// InterruptAbandonedComparisons marks comparisons still running as interrupted; valid only before
// serving starts, since nothing in this process can be reading them yet.
func (s *Store) InterruptAbandonedComparisons() (int, error) {
	res, err := s.db.Exec(`
		UPDATE abom_comparison SET state = ?, finished_at = ? WHERE state = ?`,
		ComparisonInterrupted, time.Now().UTC().Format(time.RFC3339), ComparisonRunning)
	if err != nil {
		return 0, fmt.Errorf("interrupting abandoned comparisons: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// InterruptComparison records that a comparison stopped before reading every member.
func (s *Store) InterruptComparison(id int64) error {
	return s.setComparisonState(id, ComparisonInterrupted)
}

// ComparisonsOf lists a run's comparisons, newest first, without their members.
func (s *Store) ComparisonsOf(runID int64) ([]Comparison, error) {
	rows, err := s.db.Query(`
		SELECT row_id FROM abom_comparison WHERE run_id = ? ORDER BY row_id DESC`, runID)
	if err != nil {
		return nil, fmt.Errorf("listing the comparisons of run %d: %w", runID, err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning a comparison: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []Comparison{}
	for _, id := range ids {
		c, err := s.Comparison(id)
		if err != nil {
			return nil, err
		}
		c.Members = []ComparisonMember{}
		out = append(out, c)
	}
	return out, nil
}

// Comparison reads one comparison with every member, each member's counts per rule, and totals
// over the members that were compared.
func (s *Store) Comparison(id int64) (Comparison, error) {
	c := Comparison{
		Condition: comparisonCondition, Merge: comparisonMerge, Boundary: comparisonBoundary,
		Members: []ComparisonMember{}, Rules: []ComparisonRule{}, Queries: []AnalysisQuery{},
	}
	var finished sql.NullString
	err := s.db.QueryRow(`
		SELECT row_id, run_id, requested_by, state, created_at, finished_at, cli_version
		FROM abom_comparison WHERE row_id = ?`, id).
		Scan(&c.RowID, &c.RunID, &c.RequestedBy, &c.State, &c.CreatedAt, &finished, &c.CLIVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return c, fmt.Errorf("comparison %d: %w", id, ErrNoComparison)
	}
	if err != nil {
		return c, fmt.Errorf("reading comparison %d: %w", id, err)
	}
	c.FinishedAt = finished.String
	if c.Queries, err = s.runQueriesOf(c.RunID); err != nil {
		return c, err
	}
	if c.Members, err = s.comparisonMembersOf(id, c.CLIVersion); err != nil {
		return c, err
	}
	if err := s.attachComparisonCounts(id, c.Members); err != nil {
		return c, err
	}

	rules := map[string]*ComparisonRule{}
	for _, m := range c.Members {
		switch m.Status {
		case ComparisonCompared:
			c.Compared++
			c.Totals.add(m.Counts)
			for _, r := range m.Rules {
				if rules[r.RuleID] == nil {
					rules[r.RuleID] = &ComparisonRule{RuleID: r.RuleID}
				}
				rules[r.RuleID].add(r.ComparisonCounts)
			}
		case ComparisonNotCompared:
			c.NotCompared++
		default:
			c.Pending++
		}
	}
	for _, r := range rules {
		c.Rules = append(c.Rules, *r)
	}
	sort.Slice(c.Rules, func(i, j int) bool { return c.Rules[i].RuleID < c.Rules[j].RuleID })
	return c, nil
}

func (s *Store) comparisonMembersOf(id int64, cli string) ([]ComparisonMember, error) {
	rows, err := s.db.Query(`
		SELECT repository, analyzed_commit, source_filter, status, reason, detail, results,
		       COALESCE(alerted_commit, ''), COALESCE(alerted_at, ''), COALESCE(alerted_cli, ''),
		       COALESCE(head_commit, ''), staleness, behind_by, COALESCE(read_at, '')
		FROM abom_comparison_member WHERE comparison_id = ?
		ORDER BY repository, analyzed_commit, source_filter`, id)
	if err != nil {
		return nil, fmt.Errorf("reading the members of comparison %d: %w", id, err)
	}
	defer rows.Close()
	out := []ComparisonMember{}
	for rows.Next() {
		m := ComparisonMember{Rules: []ComparisonRule{}}
		var results, behind sql.NullInt64
		if err := rows.Scan(&m.Repository, &m.AnalyzedCommit, &m.SourceFilter, &m.Status, &m.Reason,
			&m.Detail, &results, &m.AlertedCommit, &m.AlertedAt, &m.AlertedCLI, &m.HeadCommit,
			&m.Staleness, &behind, &m.ReadAt); err != nil {
			return nil, fmt.Errorf("scanning a comparison member: %w", err)
		}
		m.Counted, m.Counts.Results = results.Valid, int(results.Int64)
		if behind.Valid {
			n := int(behind.Int64)
			m.BehindBy = &n
		}
		m.Exact = m.Status == ComparisonCompared && m.AnalyzedCommit != "" &&
			m.AnalyzedCommit == m.AlertedCommit && cli != "" && m.AlertedCLI == cli
		out = append(out, m)
	}
	return out, rows.Err()
}

// attachComparisonCounts splits each compared member's results by rule and by what the alerts
// make of them, and adds the alerts no result stood for.
func (s *Store) attachComparisonCounts(id int64, members []ComparisonMember) error {
	index := map[comparisonCountKey]int{}
	for i, m := range members {
		index[comparisonCountKey{m.Repository, m.AnalyzedCommit, m.SourceFilter}] = i
	}
	perRule := comparisonCountsByRule{}

	if err := s.readComparisonResultCounts(id, perRule); err != nil {
		return err
	}
	if err := s.readComparisonOpenAlertCounts(id, perRule); err != nil {
		return err
	}
	attachComparisonRuleCounts(members, index, perRule)
	return nil
}

type comparisonCountKey struct{ repository, commit, filter string }

type comparisonCountsByRule map[comparisonCountKey]map[string]*ComparisonCounts

func (p comparisonCountsByRule) counts(k comparisonCountKey, rule string) *ComparisonCounts {
	if p[k] == nil {
		p[k] = map[string]*ComparisonCounts{}
	}
	if p[k][rule] == nil {
		p[k][rule] = &ComparisonCounts{}
	}
	return p[k][rule]
}

func (s *Store) readComparisonResultCounts(id int64, perRule comparisonCountsByRule) error {
	rows, err := s.db.Query(`
		SELECT repository, analyzed_commit, source_filter, rule_id, alert_state, COUNT(*)
		FROM abom_comparison_result WHERE comparison_id = ?
		GROUP BY repository, analyzed_commit, source_filter, rule_id, alert_state`, id)
	if err != nil {
		return fmt.Errorf("counting what comparison %d found: %w", id, err)
	}
	for rows.Next() {
		var k comparisonCountKey
		var rule, state string
		var n int
		if err := rows.Scan(&k.repository, &k.commit, &k.filter, &rule, &state, &n); err != nil {
			rows.Close()
			return fmt.Errorf("scanning a comparison count: %w", err)
		}
		addComparisonResultCount(perRule.counts(k, rule), state, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return nil
}

func addComparisonResultCount(c *ComparisonCounts, state string, n int) {
	c.Results += n
	switch MatchOf(state) {
	case MatchOpen:
		c.Open += n
	case MatchDismissed:
		c.Dismissed += n
	case MatchReopens:
		c.Reopens += n
		c.Adds += n
	default:
		c.New += n
		c.Adds += n
	}
}

func (s *Store) readComparisonOpenAlertCounts(id int64, perRule comparisonCountsByRule) error {
	alerts, err := s.db.Query(`
		SELECT repository, analyzed_commit, source_filter, rule_id, alerts
		FROM abom_comparison_alerts WHERE comparison_id = ? AND state = 'open'`, id)
	if err != nil {
		return fmt.Errorf("reading the alerts of comparison %d: %w", id, err)
	}
	for alerts.Next() {
		var k comparisonCountKey
		var rule string
		var n int
		if err := alerts.Scan(&k.repository, &k.commit, &k.filter, &rule, &n); err != nil {
			alerts.Close()
			return fmt.Errorf("scanning an alert count: %w", err)
		}
		perRule.counts(k, rule).OpenAlerts += n
	}
	alerts.Close()
	if err := alerts.Err(); err != nil {
		return err
	}
	return nil
}

func attachComparisonRuleCounts(
	members []ComparisonMember, index map[comparisonCountKey]int, perRule comparisonCountsByRule,
) {
	for k, byRule := range perRule {
		i, ok := index[k]
		if !ok || members[i].Status != ComparisonCompared {
			continue
		}
		m := &members[i]
		m.Counts = ComparisonCounts{Results: m.Counts.Results}
		results := 0
		for rule, c := range byRule {
			c.Missed = max(c.OpenAlerts-c.Open, 0)
			m.Rules = append(m.Rules, ComparisonRule{RuleID: rule, ComparisonCounts: *c})
			results += c.Results
			m.Counts.add(ComparisonCounts{
				Open: c.Open, Dismissed: c.Dismissed, Reopens: c.Reopens, New: c.New, Adds: c.Adds,
				OpenAlerts: c.OpenAlerts, Missed: c.Missed,
			})
		}
		m.Counts.Results = results
		sort.Slice(m.Rules, func(a, b int) bool { return m.Rules[a].RuleID < m.Rules[b].RuleID })
	}
}

// MatchOf reads what an alert's state makes of the result it stands for.
func MatchOf(alertState string) string {
	switch alertState {
	case "open":
		return MatchOpen
	case "dismissed":
		return MatchDismissed
	case "fixed":
		return MatchReopens
	}
	return MatchNew
}

// ComparisonResults lists what one repository's results were against the alerts.
func (s *Store) ComparisonResults(id int64, repository string) ([]ComparedResult, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, file_path, start_line, start_column, message, COALESCE(alert_number, 0),
		       alert_state
		FROM abom_comparison_result WHERE comparison_id = ? AND repository = ?
		ORDER BY file_path, start_line, start_column, rule_id, row_id`, id, repository)
	if err != nil {
		return nil, fmt.Errorf("listing what %s was in comparison %d: %w", repository, id, err)
	}
	defer rows.Close()
	out := []ComparedResult{}
	for rows.Next() {
		var r ComparedResult
		if err := rows.Scan(&r.RuleID, &r.FilePath, &r.StartLine, &r.StartColumn, &r.Message,
			&r.AlertNumber, &r.AlertState); err != nil {
			return nil, fmt.Errorf("scanning a compared result: %w", err)
		}
		r.Match = MatchOf(r.AlertState)
		out = append(out, r)
	}
	return out, rows.Err()
}
