package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// QueryRun identifies one evaluation of one query against one repository's database.
//
// Every stored fact points at a run, so any row can be traced to the query and pack version
// that produced it. That traceability is what makes amending a query and re-running an
// auditable operation rather than an opaque data change.
type QueryRun struct {
	QueryID      string
	QueryVersion string
	Pack         string
	PackVersion  string
	// PackDigest is the content hash of the pack that supplied the query. A pack version alone
	// cannot serve as identity: ours track the CodeQL CLI and do not move when a query is
	// edited, and upstream versions are immutable only by a convention nothing here verifies.
	PackDigest string
	// CLIVersion is the CodeQL that ran, not the one pinned. Extraction and evaluation both
	// change between releases, so the same query on the same commit may legitimately differ.
	CLIVersion string
	Repository string
	DBCommit   string
	// SourceFilter names the checkout filter the database was built from. A commit alone does
	// not identify a tree: ABOM fetches only the paths an actions database needs, so widening
	// that filter puts files in front of the extractor that this run never saw. Without it here,
	// a wider crawl would find every commit already on record and decline to look again.
	SourceFilter string
	// AnalysisMode says how the database was built, `regular` or `extended`. Absent is refused
	// rather than defaulted: the same rule id means a defect in code the repository holds in one
	// shape and exposure across a `uses:` boundary in the other, and a row that cannot say which
	// silently joins whichever total reads it.
	AnalysisMode string
	// Ref and AnalyzedRef are how this evaluation was reached, not what it evaluated. They are
	// deliberately absent from key(): the run is identified by content, so two refs at one
	// commit are one evaluation, recorded as two rows in abom_query_run_ref.
	Ref         string
	AnalyzedRef string
	// RecordedBy is what produced the evaluation, one of RecordedBy*. Like the refs it is not
	// identity, and it is kept from whichever recording came first.
	RecordedBy string
	// Rules are the rules the evaluation ran and where each one's query came from. One invocation
	// runs a whole suite, so the evaluation alone cannot say which version or pack a finding's
	// rule was.
	Rules []QueryRunRule
	// Held marks an evaluation whose results a run held. Part of the key, so no lookup for the
	// evaluation that stores findings can find it and skip.
	Held bool
}

func (q QueryRun) key() string {
	h := sha256.New()
	parts := []string{
		q.QueryID, q.QueryVersion, q.Pack, q.PackVersion, q.PackDigest,
		q.CLIVersion, q.Repository, q.DBCommit, q.SourceFilter,
	}
	// Appended rather than always present, so a regular run hashes exactly the tuple it hashed
	// before the column existed. A length-prefixed empty field is still a field.
	if part := analysisModeKeyPart(q.AnalysisMode); part != "" {
		parts = append(parts, part)
	}
	if q.Held {
		parts = append(parts, "held")
	}
	for _, part := range parts {
		// Length-prefixed, so no two different tuples can concatenate to the same bytes.
		fmt.Fprintf(h, "%d:%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// identified refuses a run that cannot be traced or aggregated.
//
// The mode is checked here rather than at the insert alone, so the skip paths refuse it too: a
// lookup that defaulted the mode would report an extended evaluation as already done because the
// regular one was.
func (q QueryRun) identified() error {
	if q.QueryID == "" || q.Repository == "" {
		return fmt.Errorf("store: QueryRun needs QueryID and Repository")
	}
	if !ValidAnalysisMode(q.AnalysisMode) {
		return fmt.Errorf(
			"store: QueryRun for %s must state how its database was built, one of %v; got %q",
			q.QueryID, AnalysisModes(), q.AnalysisMode)
	}
	return nil
}

// RecordQueryRun returns the row id for a run, inserting it when absent.
func (s *Store) RecordQueryRun(q QueryRun) (int64, error) {
	if err := q.identified(); err != nil {
		return 0, err
	}
	if q.RecordedBy != "" && !validRecorder(q.RecordedBy) {
		return 0, fmt.Errorf("store: QueryRun for %s names recorder %q, which is none of %v",
			q.QueryID, q.RecordedBy, recorders)
	}
	key := q.key()

	if _, err := s.db.Exec(`
		INSERT INTO abom_query_run
			(run_key, query_id, query_version, pack, pack_version, pack_digest,
			 cli_version, repository, db_commit, source_filter, analysis_mode, recorded_by, held,
			 ran_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_key) DO UPDATE SET
			ran_at      = excluded.ran_at,
			recorded_by = COALESCE(abom_query_run.recorded_by, excluded.recorded_by)`,
		key, q.QueryID, nullable(q.QueryVersion), nullable(q.Pack), nullable(q.PackVersion),
		nullable(q.PackDigest), nullable(q.CLIVersion),
		q.Repository, nullable(q.DBCommit), nullable(q.SourceFilter),
		q.AnalysisMode, nullable(q.RecordedBy), q.Held, now()); err != nil {
		return 0, fmt.Errorf("recording query run for %s: %w", q.QueryID, err)
	}

	var id int64
	if err := s.db.QueryRow(
		`SELECT row_id FROM abom_query_run WHERE run_key = ?`, key).Scan(&id); err != nil {
		return 0, fmt.Errorf("resolving query run for %s: %w", q.QueryID, err)
	}
	if err := s.recordRunRules(id, q.Rules); err != nil {
		return 0, fmt.Errorf("recording the rules %s ran: %w", q.QueryID, err)
	}

	// Only when a ref is actually known. An unrecorded ref must stay distinguishable from the
	// default branch, which the frontier spells as the empty ref.
	if q.AnalyzedRef != "" {
		if err := s.recordRunRef(id, q); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// AttachQueryRunRef records a reference against a run that is already on the ledger, reporting
// whether one was found.
//
// This is the skip path: a second reference to a commit already analyzed produces the same run
// key, so nothing is written and the reference would be lost. It matches on the full identity,
// **which includes the commit**, so a run found here analyzed this exact revision. Content
// equivalence is deliberately not accepted: that run happened at a different commit, and saying
// this reference was analyzed there would claim something that did not happen.
func (s *Store) AttachQueryRunRef(q QueryRun) (bool, error) {
	if q.AnalyzedRef == "" {
		return false, nil
	}
	if err := q.identified(); err != nil {
		return false, err
	}
	var id int64
	err := s.db.QueryRow(
		`SELECT row_id FROM abom_query_run WHERE run_key = ?`, q.key()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolving query run for %s: %w", q.QueryID, err)
	}
	if err := s.recordRunRef(id, q); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) recordRunRef(id int64, q QueryRun) error {
	if _, err := s.db.Exec(`
		INSERT INTO abom_query_run_ref (query_run_id, ref, analyzed_ref)
		VALUES (?, ?, ?)
		ON CONFLICT(query_run_id, ref, analyzed_ref) DO NOTHING`,
		id, q.Ref, q.AnalyzedRef); err != nil {
		return fmt.Errorf("recording ref %q for query run %s: %w", q.AnalyzedRef, q.QueryID, err)
	}
	return nil
}

// QueryRunRefs lists the references an evaluation was reached through, as `ref@analyzed`.
//
// Empty rather than an error when nothing is on record: no ref was recorded is a coverage state,
// not a failure.
func (s *Store) QueryRunRefs(runID int64) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT ref, analyzed_ref FROM abom_query_run_ref
		WHERE query_run_id = ? ORDER BY ref, analyzed_ref`, runID)
	if err != nil {
		return nil, fmt.Errorf("reading refs for query run %d: %w", runID, err)
	}
	defer rows.Close()

	refs := []string{}
	for rows.Next() {
		var ref, analyzed string
		if err := rows.Scan(&ref, &analyzed); err != nil {
			return nil, fmt.Errorf("scanning ref for query run %d: %w", runID, err)
		}
		refs = append(refs, ref+"@"+analyzed)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading refs for query run %d: %w", runID, err)
	}
	return refs, nil
}

// Finding is one result from any provider — a CodeQL suite, or anything else that can attach
// evidence to the graph without changing its shape.
type Finding struct {
	// Provider names the producer. Empty means nobody recorded one, which is a state and not a
	// synonym for CodeQL: every finding predating this column is unattributed, and relabeling
	// them would invent the provenance the field exists to hold.
	Provider     string
	QueryRunID   int64
	RuleID       string
	RepositoryID int64
	FilePath     string
	StartLine    int
	StartColumn  int
	EndLine      int
	EndColumn    int
	Message      string
	Severity     string
	// SecuritySeverity is the rule's `@security-severity`, nil where the rule declares none.
	SecuritySeverity *float64
	Fingerprint      string
	// ResultHash identifies the finding by content, with the repository deliberately outside it.
	ResultHash string
}

// AddFindings writes findings in one transaction, folding a finding already on record into the
// row that holds it.
func (s *Store) AddFindings(findings []Finding) (int, error) {
	if len(findings) == 0 {
		return 0, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := addFindings(tx, findings)
	if err != nil {
		return len(rows), err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// addFindings writes findings and their sightings inside tx, returning each one's row in order.
func addFindings(tx *sql.Tx, findings []Finding) ([]int64, error) {
	// Provenance is refreshed and identity is not: a finding seen again is the same finding, but
	// the run that reproduced it is newer evidence than the one that first saw it. Ignoring the
	// conflict outright left 2,759 corpus findings unable to name any run at all, 2,377 of them
	// in repositories re-analyzed since. COALESCE so a lane that carries neither cannot erase
	// what an earlier one recorded.
	// RETURNING rather than LastInsertId: after ON CONFLICT DO UPDATE the CLI reports one row
	// affected and leaves LastInsertId on an unrelated row, which corrupted node identity once.
	stmt, err := tx.Prepare(`
		INSERT INTO abom_finding
			(provider, query_run_id, rule_id, repository_row_id, file_path, start_line,
			 start_column, end_line, end_column, message, severity, security_severity,
			 fingerprint, result_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(rule_id, repository_row_id, file_path, start_line, start_column, fingerprint)
		DO UPDATE SET
			provider     = COALESCE(excluded.provider, abom_finding.provider),
			query_run_id = CASE
				WHEN (SELECT analysis_mode FROM abom_query_run
				      WHERE row_id = excluded.query_run_id) = 'extended'
				 AND COALESCE((SELECT analysis_mode FROM abom_query_run
				               WHERE row_id = abom_finding.query_run_id), 'regular') = 'regular'
				THEN abom_finding.query_run_id
				ELSE COALESCE(excluded.query_run_id, abom_finding.query_run_id) END,
			result_hash  = COALESCE(excluded.result_hash, abom_finding.result_hash)
		RETURNING row_id`)
	if err != nil {
		return nil, fmt.Errorf("preparing finding insert: %w", err)
	}
	defer stmt.Close()

	// Which run reported which finding. One finding row spans commits and can name only one run,
	// so without this relation "is it still reported" is unanswerable.
	sighting, err := tx.Prepare(`
		INSERT INTO abom_finding_sighting (finding_row_id, query_run_id)
		VALUES (?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return nil, fmt.Errorf("preparing sighting insert: %w", err)
	}
	defer sighting.Close()

	written := make([]int64, 0, len(findings))
	for _, f := range findings {
		var rowID int64
		if err := stmt.QueryRow(nullable(f.Provider), nullableID(f.QueryRunID), f.RuleID,
			f.RepositoryID, nullable(f.FilePath), f.StartLine, f.StartColumn, f.EndLine, f.EndColumn,
			nullable(f.Message), nullable(f.Severity), f.SecuritySeverity,
			f.Fingerprint, nullable(f.ResultHash)).Scan(&rowID); err != nil {
			return written, fmt.Errorf("inserting finding for %s: %w", f.RuleID, err)
		}
		// A finding nobody attributed to a run gets no sighting. Inventing one would put a claim
		// in the relation that the sweep then trusts.
		if f.QueryRunID != 0 {
			if _, err := sighting.Exec(rowID, f.QueryRunID); err != nil {
				return written, fmt.Errorf("recording a sighting for %s: %w", f.RuleID, err)
			}
		}
		written = append(written, rowID)
	}
	return written, nil
}

// LinkFindingsToNodes attributes findings to graph nodes by source location.
//
// This join is what lets graph traversal answer security questions: a finding recorded against
// a file position becomes a property of the dependency edge at that position.
func (s *Store) LinkFindingsToNodes() (int, error) {
	// A finding on a uses: line belongs to that step.
	res, err := s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, e.src_node_row_id
		FROM abom_finding f
		JOIN abom_edge_live e
		  ON e.repository_row_id = f.repository_row_id
		 AND e.file_path = f.file_path
		 AND e.start_line = f.start_line
		WHERE true
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to nodes: %w", err)
	}
	n, _ := res.RowsAffected()

	// Most rules report on a workflow or a job rather than on a uses: line, so anything left
	// over attaches to the file it was reported in. Without this the majority of a suite's
	// output would be invisible to every view built on the graph.
	res, err = s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, n.row_id
		FROM abom_finding f
		JOIN abom_node_live n
		  ON n.repository_row_id = f.repository_row_id
		 AND n.node_kind IN ('workflow', 'composite_action')
		 AND n.path = f.file_path
		WHERE NOT EXISTS (
		        SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to workflows: %w", err)
	}
	m, _ := res.RowsAffected()

	// A reusable workflow or a composite action is represented by the container holding its
	// jobs, so nothing at its path is a workflow node and the join above finds nothing. Walk out
	// of the jobs and steps recorded there to whatever holds them. Nesting is at most
	// step -> job -> container, so two levels reach it.
	//
	// Findings in a file the graph never modeled are left alone: a graph built from
	// dependencies has no node for a file that names none, and inventing one would report
	// something that was never extracted.
	res, err = s.db.Exec(`
		INSERT INTO abom_finding_node (finding_row_id, node_row_id)
		SELECT f.row_id, (
		        SELECT CASE
		                 WHEN n.node_kind NOT IN ('job','step') THEN n.row_id
		                 WHEN p.node_kind NOT IN ('job','step') THEN p.row_id
		                 WHEN gp.row_id IS NOT NULL             THEN gp.row_id
		                 ELSE COALESCE(p.row_id, n.row_id)
		               END
		        FROM abom_node_live n
		        LEFT JOIN abom_node_live p  ON p.row_id  = n.container_node_row_id
		        LEFT JOIN abom_node_live gp ON gp.row_id = p.container_node_row_id
		        WHERE n.repository_row_id = f.repository_row_id AND n.path = f.file_path
		        ORDER BY n.row_id LIMIT 1)
		FROM abom_finding f
		WHERE NOT EXISTS (
		        SELECT 1 FROM abom_finding_node fn WHERE fn.finding_row_id = f.row_id)
		  AND EXISTS (
		        SELECT 1 FROM abom_node_live n
		        WHERE n.repository_row_id = f.repository_row_id AND n.path = f.file_path)
		ON CONFLICT DO NOTHING`)
	if err != nil {
		return 0, fmt.Errorf("linking findings to the container holding their file: %w", err)
	}
	c, _ := res.RowsAffected()
	return int(n + m + c), nil
}
