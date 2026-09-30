package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// What recorded an evaluation. A reader tells the crawl's lane from a run somebody started by this
// rather than by the query id, which both can share.
const (
	RecordedByCrawl  = "crawl"
	RecordedByRun    = "run"
	RecordedByCorpus = "corpus"
)

var recorders = []string{RecordedByCrawl, RecordedByRun, RecordedByCorpus}

func validRecorder(by string) bool { return slices.Contains(recorders, by) }

// QueryRunRule is one rule an evaluation ran, and the query behind it: its version and the pack
// that supplied it, which decides its source.
type QueryRunRule struct {
	RuleID       string
	QueryVersion string
	Pack         string
	PackVersion  string
	PackDigest   string
}

// ErrNoFinding is a finding the store does not hold.
var ErrNoFinding = errors.New("no such finding")

// FindingTrace is one finding and every evaluation on record as reporting it.
type FindingTrace struct {
	RowID       int64               `json:"rowId"`
	RuleID      string              `json:"ruleId"`
	Repository  string              `json:"repository"`
	FilePath    string              `json:"filePath"`
	StartLine   int                 `json:"startLine"`
	Message     string              `json:"message"`
	Severity    string              `json:"severity"`
	Provider    string              `json:"provider"`
	Evaluations []FindingEvaluation `json:"evaluations"`
}

// FindingEvaluation is one evaluation that reported a finding. The rule's own version, pack and
// source are empty where the evaluation did not record them, which is not known rather than none.
type FindingEvaluation struct {
	QueryRunID int64 `json:"queryRunId"`
	// Invoked is what ran: the query itself, or the suite or queries a lane ran as one.
	Invoked     string       `json:"invoked"`
	Packs       []string     `json:"packs"`
	RuleVersion string       `json:"ruleVersion"`
	Pack        string       `json:"pack"`
	PackVersion string       `json:"packVersion"`
	PackDigest  string       `json:"packDigest"`
	Source      string       `json:"source"`
	CLIVersion  string       `json:"cliVersion"`
	Mode        string       `json:"mode"`
	Commit      string       `json:"commit"`
	RanAt       string       `json:"ranAt"`
	RecordedBy  string       `json:"recordedBy"`
	Runs        []FindingRun `json:"runs"`
}

// FindingRun is a member of an on-demand run that the evaluation answered.
type FindingRun struct {
	RunID      int64  `json:"runId"`
	Repository string `json:"repository"`
	Status     string `json:"status"`
}

// mergeRunRules folds a rule named twice into one. Copies that disagree leave no way to say which
// reported a result, so the rule keeps every pack, which reads as mixed, and no version.
func mergeRunRules(rules []QueryRunRule) ([]QueryRunRule, error) {
	out := []QueryRunRule{}
	at := map[string]int{}
	packs := map[string][]string{}
	for _, r := range rules {
		if r.RuleID == "" {
			return nil, fmt.Errorf("store: a rule with no id")
		}
		i, seen := at[r.RuleID]
		if !seen {
			at[r.RuleID] = len(out)
			out = append(out, r)
			packs[r.RuleID] = []string{r.Pack}
			continue
		}
		if out[i] == r {
			continue
		}
		if !slices.Contains(packs[r.RuleID], r.Pack) {
			packs[r.RuleID] = append(packs[r.RuleID], r.Pack)
		}
		names := slices.Sorted(slices.Values(packs[r.RuleID]))
		joined := strings.Join(names, " ")
		if slices.Contains(names, "") {
			joined = ""
		}
		out[i] = QueryRunRule{RuleID: r.RuleID, Pack: joined}
	}
	return out, nil
}

// recordRunRules keeps which rules an evaluation ran. A second recording of the same evaluation
// fills in only what the first did not know: one run key is one set of pack contents.
func (s *Store) recordRunRules(runID int64, rules []QueryRunRule) error {
	if len(rules) == 0 {
		return nil
	}
	merged, err := mergeRunRules(rules)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.Prepare(`
		INSERT INTO abom_query_run_rule
			(query_run_id, rule_id, query_version, pack, pack_version, pack_digest, source)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(query_run_id, rule_id) DO UPDATE SET
			query_version = COALESCE(abom_query_run_rule.query_version, excluded.query_version),
			pack_version  = COALESCE(abom_query_run_rule.pack_version, excluded.pack_version),
			pack_digest   = COALESCE(abom_query_run_rule.pack_digest, excluded.pack_digest)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, r := range merged {
		if _, err := stmt.Exec(runID, r.RuleID, nullable(r.QueryVersion), nullable(r.Pack),
			nullable(r.PackVersion), nullable(r.PackDigest), nullable(sourceOfPack(r.Pack))); err != nil {
			return fmt.Errorf("recording rule %s: %w", r.RuleID, err)
		}
	}
	return tx.Commit()
}

// backfillRunRules names the rules of evaluations recorded before rules were kept, only where the
// answer is certain. One pack supplied everything a single-pack evaluation ran, so that pack is
// every reported rule's; the version is certain only when the evaluation ran that one query.
func (s *Store) backfillRunRules() error {
	rows, err := s.db.Query(`
		SELECT DISTINCT sg.query_run_id, f.rule_id,
		       CASE WHEN q.query_id = f.rule_id THEN COALESCE(q.query_version, '') ELSE '' END,
		       q.pack, COALESCE(q.pack_version, ''), COALESCE(q.pack_digest, '')
		FROM abom_finding_sighting sg
		JOIN abom_finding_any_mode f ON f.row_id = sg.finding_row_id
		JOIN abom_query_run q ON q.row_id = sg.query_run_id
		WHERE COALESCE(q.pack, '') <> '' AND instr(q.pack, ' ') = 0
		  AND NOT EXISTS (SELECT 1 FROM abom_query_run_rule r
		                   WHERE r.query_run_id = sg.query_run_id AND r.rule_id = f.rule_id)`)
	if err != nil {
		return fmt.Errorf("finding evaluations whose rules were not kept: %w", err)
	}
	byRun := map[int64][]QueryRunRule{}
	order := []int64{}
	for rows.Next() {
		var run int64
		var r QueryRunRule
		if err := rows.Scan(&run, &r.RuleID, &r.QueryVersion, &r.Pack, &r.PackVersion, &r.PackDigest); err != nil {
			rows.Close()
			return err
		}
		if _, seen := byRun[run]; !seen {
			order = append(order, run)
		}
		byRun[run] = append(byRun[run], r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, run := range order {
		if err := s.recordRunRules(run, byRun[run]); err != nil {
			return fmt.Errorf("naming the rules of evaluation %d: %w", run, err)
		}
	}
	return nil
}

// sourceClause keeps the finding aliased `finding` when an evaluation that reported it recorded
// its rule's query as coming from one of these sources. Unknown is not a match.
func sourceClause(finding string, sources []string) (string, []any) {
	list, args := inList(sources)
	return `EXISTS (SELECT 1 FROM abom_finding_sighting sg
	                 JOIN abom_query_run_rule qr
	                   ON qr.query_run_id = sg.query_run_id AND qr.rule_id = ` + finding + `.rule_id
	                WHERE sg.finding_row_id = ` + finding + `.row_id AND qr.source IN ` + list + `)`, args
}

// recordedSource holds when an evaluation that reported the finding aliased `finding` recorded
// where its rule's query came from.
func recordedSource(finding string) string {
	return `EXISTS (SELECT 1 FROM abom_finding_sighting sg
	                 JOIN abom_query_run_rule qr
	                   ON qr.query_run_id = sg.query_run_id AND qr.rule_id = ` + finding + `.rule_id
	                WHERE sg.finding_row_id = ` + finding + `.row_id AND qr.source IS NOT NULL)`
}

// findingSources is the sources every evaluation that reported the finding `f` recorded for its
// rule, NULL where none did.
const findingSources = `(SELECT GROUP_CONCAT(DISTINCT qr.source) FROM abom_finding_sighting sg
	JOIN abom_query_run_rule qr ON qr.query_run_id = sg.query_run_id AND qr.rule_id = f.rule_id
	WHERE sg.finding_row_id = f.row_id)`

// distinctSources reads a concatenation of findingSources columns once each, in a stable order.
func distinctSources(v sql.NullString) []string {
	out := []string{}
	for _, s := range splitSorted(v) {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}

// FindingTrace reads one finding and every evaluation that reported it, oldest first.
func (s *Store) FindingTrace(id int64) (FindingTrace, error) {
	return s.findingTrace(id, "", 0)
}

// findingTrace returns every evaluation oldest first, or with limit > 0 only the newest limit, newest first.
func (s *Store) findingTrace(id int64, mode string, limit int) (FindingTrace, error) {
	out := FindingTrace{Evaluations: []FindingEvaluation{}}
	err := s.db.QueryRow(`
		SELECT f.row_id, f.rule_id, r.repository_full_name, COALESCE(f.file_path, ''),
		       COALESCE(f.start_line, 0), COALESCE(f.message, ''), COALESCE(f.severity, ''),
		       COALESCE(f.provider, '')
		FROM abom_finding_any_mode f JOIN repository r ON r.row_id = f.repository_row_id
		WHERE f.row_id = ?`, id).Scan(&out.RowID, &out.RuleID, &out.Repository, &out.FilePath,
		&out.StartLine, &out.Message, &out.Severity, &out.Provider)
	if errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("finding %d: %w", id, ErrNoFinding)
	}
	if err != nil {
		return out, fmt.Errorf("reading finding %d: %w", id, err)
	}

	query := `
		SELECT q.row_id, q.query_id, COALESCE(q.pack, ''), COALESCE(qr.query_version, ''),
		       COALESCE(qr.pack, ''), COALESCE(qr.pack_version, ''), COALESCE(qr.pack_digest, ''),
		       COALESCE(qr.source, ''), COALESCE(q.cli_version, ''), q.analysis_mode,
		       COALESCE(q.db_commit, ''), q.ran_at, COALESCE(q.recorded_by, '')
		FROM abom_finding_sighting sg
		JOIN abom_query_run q ON q.row_id = sg.query_run_id
		LEFT JOIN abom_query_run_rule qr ON qr.query_run_id = q.row_id AND qr.rule_id = ?
		WHERE sg.finding_row_id = ?`
	args := []any{out.RuleID, id}
	if mode != "" {
		query += " AND q.analysis_mode = ?"
		args = append(args, mode)
	}
	if limit > 0 {
		query += " ORDER BY q.ran_at DESC, q.row_id DESC LIMIT ?"
		args = append(args, limit)
	} else {
		query += " ORDER BY q.ran_at, q.row_id"
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return out, fmt.Errorf("reading what reported finding %d: %w", id, err)
	}
	for rows.Next() {
		e := FindingEvaluation{Runs: []FindingRun{}}
		var packs string
		if err := rows.Scan(&e.QueryRunID, &e.Invoked, &packs, &e.RuleVersion, &e.Pack, &e.PackVersion,
			&e.PackDigest, &e.Source, &e.CLIVersion, &e.Mode, &e.Commit, &e.RanAt, &e.RecordedBy); err != nil {
			rows.Close()
			return out, err
		}
		e.Packs = strings.Fields(packs)
		out.Evaluations = append(out.Evaluations, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}

	for i := range out.Evaluations {
		runs, err := s.db.Query(`
			SELECT run_id, repository, status FROM abom_analysis_member
			WHERE query_run_id = ? ORDER BY run_id, repository`, out.Evaluations[i].QueryRunID)
		if err != nil {
			return out, fmt.Errorf("reading the runs evaluation %d answered: %w", out.Evaluations[i].QueryRunID, err)
		}
		for runs.Next() {
			var r FindingRun
			if err := runs.Scan(&r.RunID, &r.Repository, &r.Status); err != nil {
				runs.Close()
				return out, err
			}
			out.Evaluations[i].Runs = append(out.Evaluations[i].Runs, r)
		}
		runs.Close()
		if err := runs.Err(); err != nil {
			return out, err
		}
	}
	return out, nil
}
