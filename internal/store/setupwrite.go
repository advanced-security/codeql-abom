package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// setupWriteDDL holds what ABOM was shown before writing default setup, who approved it, and every
// step each write took.
const setupWriteDDL = `
CREATE TABLE IF NOT EXISTS abom_setup_preview (
  row_id      INTEGER PRIMARY KEY,
  created_at  TEXT NOT NULL,
  -- The path that asked: the interface or the command line. The approver is a GitHub login.
  created_by  TEXT NOT NULL DEFAULT '',
  view_name   TEXT NOT NULL DEFAULT '',
  predicate   TEXT NOT NULL DEFAULT '',
  accounts    TEXT NOT NULL DEFAULT '[]',
  excluded    TEXT NOT NULL DEFAULT '[]',
  approved_at TEXT,
  approved_by TEXT
);

CREATE TABLE IF NOT EXISTS abom_setup_preview_row (
  preview_id INTEGER NOT NULL REFERENCES abom_setup_preview(row_id),
  repository TEXT NOT NULL COLLATE NOCASE,
  class      TEXT NOT NULL,
  evidence   TEXT NOT NULL DEFAULT '',
  languages  TEXT NOT NULL DEFAULT '[]',
  read_at    TEXT NOT NULL DEFAULT '',
  -- The body a write would send; empty where the class is never written.
  intended   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (preview_id, repository)
);

CREATE TABLE IF NOT EXISTS abom_setup_batch (
  row_id      INTEGER PRIMARY KEY,
  preview_id  INTEGER NOT NULL UNIQUE REFERENCES abom_setup_preview(row_id),
  started_at  TEXT NOT NULL,
  started_by  TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL,
  reason      TEXT NOT NULL DEFAULT '',
  finished_at TEXT
);

-- Every step a write took, appended and never changed.
CREATE TABLE IF NOT EXISTS abom_setup_write (
  row_id            INTEGER PRIMARY KEY,
  batch_id          INTEGER NOT NULL REFERENCES abom_setup_batch(row_id),
  repository        TEXT NOT NULL COLLATE NOCASE,
  at                TEXT NOT NULL,
  step              TEXT NOT NULL,
  detail            TEXT NOT NULL DEFAULT '',
  before_languages  TEXT,
  sent              TEXT,
  status            INTEGER,
  run_id            INTEGER,
  run_url           TEXT,
  after_languages   TEXT,
  attachment_before TEXT,
  attachment_after  TEXT
);

CREATE INDEX IF NOT EXISTS idx_setup_write_batch ON abom_setup_write(batch_id, repository, row_id);
`

// The steps a write records. Sent is the only one that leaves a write in flight.
const (
	SetupStepRefused  = "refused"
	SetupStepSkipped  = "skipped"
	SetupStepDeferred = "deferred"
	SetupStepSent     = "sent"
	SetupStepVerified = "verified"
	SetupStepFailed   = "failed"
)

// The states a batch is in.
const (
	SetupBatchRunning = "running"
	SetupBatchPaused  = "paused"
	SetupBatchStopped = "stopped"
	SetupBatchDone    = "done"
)

// SetupPreview is what somebody was shown before any write: every repository in scope, its class
// and the body a write would send.
type SetupPreview struct {
	ID         int64             `json:"id"`
	CreatedAt  time.Time         `json:"createdAt"`
	CreatedBy  string            `json:"createdBy"`
	ViewName   string            `json:"viewName"`
	Predicate  string            `json:"predicate"`
	Accounts   []string          `json:"accounts"`
	Excluded   []string          `json:"excluded"`
	ApprovedAt string            `json:"approvedAt,omitempty"`
	ApprovedBy string            `json:"approvedBy,omitempty"`
	BatchID    int64             `json:"batchId,omitempty"`
	Counts     map[string]int    `json:"counts"`
	Rows       []SetupPreviewRow `json:"rows,omitempty"`
}

// SetupPreviewRow is one repository as a preview showed it.
type SetupPreviewRow struct {
	Repository string   `json:"repository"`
	Class      string   `json:"class"`
	Evidence   string   `json:"evidence"`
	Languages  []string `json:"languages"`
	ReadAt     string   `json:"readAt"`
	Intended   string   `json:"intended,omitempty"`
}

// RecordSetupPreview keeps a preview exactly as shown.
func (s *Store) RecordSetupPreview(p SetupPreview) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO abom_setup_preview (created_at, created_by, view_name, predicate, accounts, excluded)
		VALUES (?, ?, ?, ?, ?, ?)`, p.CreatedAt.UTC().Format(time.RFC3339Nano), p.CreatedBy, p.ViewName,
		p.Predicate, jsonList(p.Accounts), jsonList(p.Excluded))
	if err != nil {
		return 0, fmt.Errorf("recording a preview: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, r := range p.Rows {
		if _, err := tx.Exec(`INSERT INTO abom_setup_preview_row
			(preview_id, repository, class, evidence, languages, read_at, intended) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, r.Repository, r.Class, r.Evidence, jsonList(r.Languages), r.ReadAt, r.Intended); err != nil {
			return 0, fmt.Errorf("recording %s in a preview: %w", r.Repository, err)
		}
	}
	return id, tx.Commit()
}

// ApproveSetupPreview records who approved a preview. A preview is approved once: approving it
// again would let a second person's name stand behind a decision somebody else made.
func (s *Store) ApproveSetupPreview(id int64, by string, at time.Time) error {
	if by == "" {
		return errors.New("store: an approval names who gave it")
	}
	res, err := s.db.Exec(`UPDATE abom_setup_preview SET approved_at = ?, approved_by = ?
		WHERE row_id = ? AND approved_at IS NULL`, at.UTC().Format(time.RFC3339Nano), by, id)
	if err != nil {
		return fmt.Errorf("approving preview %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("preview %d does not exist or is already approved", id)
	}
	return nil
}

// StartSetupBatch starts the one batch an approved preview may run.
func (s *Store) StartSetupBatch(previewID int64, by string, at time.Time) (int64, error) {
	var approved sql.NullString
	err := s.db.QueryRow(`SELECT approved_at FROM abom_setup_preview WHERE row_id = ?`, previewID).Scan(&approved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("preview %d does not exist", previewID)
	}
	if err != nil {
		return 0, err
	}
	if !approved.Valid {
		return 0, fmt.Errorf("preview %d is not approved, and nothing is written from a preview nobody approved", previewID)
	}
	res, err := s.db.Exec(`INSERT INTO abom_setup_batch (preview_id, started_at, started_by, state)
		VALUES (?, ?, ?, ?)`, previewID, at.UTC().Format(time.RFC3339Nano), by, SetupBatchRunning)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, fmt.Errorf("preview %d has already run its batch; preview again to write again", previewID)
		}
		return 0, fmt.Errorf("starting a batch from preview %d: %w", previewID, err)
	}
	return res.LastInsertId()
}

// SetSetupBatchState moves a batch to state, saying why; done and stopped are where it ends.
func (s *Store) SetSetupBatchState(id int64, state, reason string, at time.Time) error {
	var finished any
	if state == SetupBatchDone || state == SetupBatchStopped {
		finished = at.UTC().Format(time.RFC3339Nano)
	}
	res, err := s.db.Exec(`UPDATE abom_setup_batch SET state = ?, reason = ?, finished_at = ?
		WHERE row_id = ? AND state NOT IN ('done', 'stopped')`, state, reason, finished, id)
	if err != nil {
		return fmt.Errorf("moving batch %d to %s: %w", id, state, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("batch %d does not exist or has already ended", id)
	}
	return nil
}

// SetupBatch is one run of an approved preview, with the last step each repository reached.
type SetupBatch struct {
	ID         int64          `json:"id"`
	PreviewID  int64          `json:"previewId"`
	StartedAt  string         `json:"startedAt"`
	StartedBy  string         `json:"startedBy"`
	State      string         `json:"state"`
	Reason     string         `json:"reason,omitempty"`
	FinishedAt string         `json:"finishedAt,omitempty"`
	Steps      map[string]int `json:"steps"`
	Remaining  int            `json:"remaining"`
}

// SetupBatches lists every batch, newest first.
func (s *Store) SetupBatches() ([]SetupBatch, error) {
	batches, err := queryAll(s, "listing batches", func(row *sql.Rows) (SetupBatch, error) {
		var b SetupBatch
		var finished sql.NullString
		err := row.Scan(&b.ID, &b.PreviewID, &b.StartedAt, &b.StartedBy, &b.State, &b.Reason, &finished)
		b.FinishedAt = finished.String
		return b, err
	}, `SELECT row_id, preview_id, started_at, started_by, state, reason, finished_at
	      FROM abom_setup_batch ORDER BY row_id DESC`)
	if err != nil {
		return nil, err
	}
	for i := range batches {
		if batches[i].Steps, err = s.lastSteps(batches[i].ID); err != nil {
			return nil, err
		}
		remaining, err := s.SetupBatchRemaining(batches[i].ID)
		if err != nil {
			return nil, err
		}
		batches[i].Remaining = len(remaining)
	}
	return batches, nil
}

// SetupBatchOf reads one batch.
func (s *Store) SetupBatchOf(id int64) (SetupBatch, bool, error) {
	all, err := s.SetupBatches()
	if err != nil {
		return SetupBatch{}, false, err
	}
	for _, b := range all {
		if b.ID == id {
			return b, true, nil
		}
	}
	return SetupBatch{}, false, nil
}

func (s *Store) lastSteps(batch int64) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT w.step, COUNT(*) FROM abom_setup_write w
		WHERE w.batch_id = ? AND w.row_id = (SELECT MAX(x.row_id) FROM abom_setup_write x
		                                      WHERE x.batch_id = w.batch_id AND x.repository = w.repository)
		GROUP BY w.step`, batch)
	if err != nil {
		return nil, fmt.Errorf("counting the steps of batch %d: %w", batch, err)
	}
	defer rows.Close()
	for rows.Next() {
		var step string
		var n int
		if err := rows.Scan(&step, &n); err != nil {
			return nil, err
		}
		out[step] = n
	}
	return out, rows.Err()
}

// SetupBatchRemaining lists the rows a batch may still write: those previewed as add or enable that
// it has not sent, refused, skipped or failed. A deferred row stays until it is written or given up.
func (s *Store) SetupBatchRemaining(batch int64) ([]SetupPreviewRow, error) {
	return queryAll(s, "listing what a batch has left", scanPreviewRow, `
		SELECT r.repository, r.class, r.evidence, r.languages, r.read_at, r.intended
		  FROM abom_setup_batch b
		  JOIN abom_setup_preview_row r ON r.preview_id = b.preview_id
		 WHERE b.row_id = ? AND r.class IN ('add', 'enable')
		   AND NOT EXISTS (SELECT 1 FROM abom_setup_write w
		                    WHERE w.batch_id = b.row_id AND w.repository = r.repository AND w.step <> 'deferred')
		 ORDER BY r.repository`, batch)
}

// SetupWrite is one step of one write.
type SetupWrite struct {
	ID               int64     `json:"id"`
	BatchID          int64     `json:"batchId"`
	Repository       string    `json:"repository"`
	At               time.Time `json:"at"`
	Step             string    `json:"step"`
	Detail           string    `json:"detail,omitempty"`
	Before           []string  `json:"before"`
	Sent             string    `json:"sent,omitempty"`
	Status           int       `json:"status,omitempty"`
	RunID            int64     `json:"runId,omitempty"`
	RunURL           string    `json:"runUrl,omitempty"`
	After            []string  `json:"after"`
	AttachmentBefore string    `json:"attachmentBefore,omitempty"`
	AttachmentAfter  string    `json:"attachmentAfter,omitempty"`
}

// AppendSetupWrite adds one step to the ledger.
func (s *Store) AppendSetupWrite(w SetupWrite) error {
	if w.BatchID == 0 || w.Repository == "" || w.Step == "" {
		return errors.New("store: a ledger step names its batch, repository and step")
	}
	var before, after any
	if w.Before != nil {
		before = jsonList(w.Before)
	}
	if w.After != nil {
		after = jsonList(w.After)
	}
	_, err := s.db.Exec(`INSERT INTO abom_setup_write (batch_id, repository, at, step, detail, before_languages,
		sent, status, run_id, run_url, after_languages, attachment_before, attachment_after)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		w.BatchID, w.Repository, w.At.UTC().Format(time.RFC3339Nano), w.Step, w.Detail, before, nullable(w.Sent),
		nullableInt64(int64(w.Status)), nullableInt64(w.RunID), nullable(w.RunURL), after,
		nullable(w.AttachmentBefore), nullable(w.AttachmentAfter))
	if err != nil {
		return fmt.Errorf("recording a step for %s: %w", w.Repository, err)
	}
	return nil
}

// SetupLedger lists every step, oldest first, for one repository, one batch, both or neither.
func (s *Store) SetupLedger(repository string, batch int64) ([]SetupWrite, error) {
	where, args := ledgerWhere(repository, batch)
	return queryAll(s, "reading the ledger", scanSetupWrite, setupWriteColumns+` WHERE `+where+` ORDER BY row_id`, args...)
}

// SetupLedgerPage is a ledger's newest steps and how many steps it holds.
type SetupLedgerPage struct {
	Items []SetupWrite `json:"items"`
	Total int          `json:"total"`
}

// SetupLedgerNewest lists the newest limit steps, newest first, and counts them all.
func (s *Store) SetupLedgerNewest(repository string, batch int64, limit int) (SetupLedgerPage, error) {
	where, args := ledgerWhere(repository, batch)
	page := SetupLedgerPage{}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_setup_write WHERE `+where, args...).Scan(&page.Total); err != nil {
		return page, fmt.Errorf("counting the ledger: %w", err)
	}
	items, err := queryAll(s, "reading the ledger", scanSetupWrite, setupWriteColumns+` WHERE `+where+
		` ORDER BY row_id DESC LIMIT ?`, append(args, limit)...)
	page.Items = items
	return page, err
}

func ledgerWhere(repository string, batch int64) (string, []any) {
	where, args := []string{"1 = 1"}, []any{}
	if repository != "" {
		where, args = append(where, "repository = ?"), append(args, repository)
	}
	if batch != 0 {
		where, args = append(where, "batch_id = ?"), append(args, batch)
	}
	return strings.Join(where, " AND "), args
}

// SetupWritesInFlight lists writes whose last step is sent: the change went out and has not been read back.
func (s *Store) SetupWritesInFlight() ([]SetupWrite, error) {
	return queryAll(s, "reading writes in flight", scanSetupWrite, setupWriteColumns+` w
		WHERE step = 'sent' AND row_id = (SELECT MAX(x.row_id) FROM abom_setup_write x
		                                   WHERE x.batch_id = w.batch_id AND x.repository = w.repository)
		ORDER BY row_id`)
}

const setupWriteColumns = `SELECT row_id, batch_id, repository, at, step, detail, COALESCE(before_languages, ''),
	COALESCE(sent, ''), COALESCE(status, 0), COALESCE(run_id, 0), COALESCE(run_url, ''), COALESCE(after_languages, ''),
	COALESCE(attachment_before, ''), COALESCE(attachment_after, '') FROM abom_setup_write`

func scanSetupWrite(row *sql.Rows) (SetupWrite, error) {
	var w SetupWrite
	var at, before, after string
	if err := row.Scan(&w.ID, &w.BatchID, &w.Repository, &at, &w.Step, &w.Detail, &before, &w.Sent, &w.Status,
		&w.RunID, &w.RunURL, &after, &w.AttachmentBefore, &w.AttachmentAfter); err != nil {
		return w, err
	}
	w.At = parseStoredTime(at)
	w.Before, w.After = listOf(before), listOf(after)
	return w, nil
}

// SetupPreviews lists every preview, newest first, without its rows.
func (s *Store) SetupPreviews() ([]SetupPreview, error) {
	previews, err := queryAll(s, "listing previews", scanPreview, previewColumns+` ORDER BY p.row_id DESC`)
	if err != nil {
		return nil, err
	}
	for i := range previews {
		if previews[i].Counts, err = s.previewCounts(previews[i].ID); err != nil {
			return nil, err
		}
	}
	return previews, nil
}

// SetupPreviewOf reads one preview with every row it showed.
func (s *Store) SetupPreviewOf(id int64) (SetupPreview, bool, error) {
	previews, err := queryAll(s, "reading a preview", scanPreview, previewColumns+` WHERE p.row_id = ?`, id)
	if err != nil || len(previews) == 0 {
		return SetupPreview{}, false, err
	}
	p := previews[0]
	if p.Rows, err = queryAll(s, "reading a preview's rows", scanPreviewRow, `SELECT repository, class, evidence,
		languages, read_at, intended FROM abom_setup_preview_row WHERE preview_id = ? ORDER BY repository`, id); err != nil {
		return p, false, err
	}
	p.Counts, err = s.previewCounts(id)
	return p, true, err
}

// SetupPreviewRows is one page of a preview's rows and how many rows it holds.
type SetupPreviewRows struct {
	Items []SetupPreviewRow `json:"items"`
	Total int               `json:"total"`
}

// previewRowOrder is the order an approver acts on a preview: what would be written, what is held
// or unread, then what is settled. Excluded, not-a-target and held are the preview's own classes.
var previewRowOrder = []string{
	SetupAdd, SetupEnable, "held", SetupLanguageNotWritable, "excluded", "not-a-target", SetupConflict, SetupBlocked,
	SetupActionsNotAllowed, SetupUnreadable, SetupNotRead, SetupNotCrawled, SetupNotDetected,
	SetupNotListed, SetupUnknown, SetupCovered, SetupAdvanced, SetupNoActionsCode,
}

// SetupPreviewRowsPage reads one page of a preview's rows, the ones a batch would write first.
func (s *Store) SetupPreviewRowsPage(id int64, limit, offset int) (SetupPreviewRows, error) {
	page := SetupPreviewRows{Items: []SetupPreviewRow{}}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_setup_preview_row WHERE preview_id = ?`, id).
		Scan(&page.Total); err != nil {
		return page, fmt.Errorf("counting a preview's rows: %w", err)
	}
	order := `CASE class`
	for i, class := range previewRowOrder {
		order += fmt.Sprintf(" WHEN '%s' THEN %d", class, i)
	}
	order += fmt.Sprintf(" ELSE %d END", len(previewRowOrder))
	items, err := queryAll(s, "reading a page of a preview", scanPreviewRow, `SELECT repository, class, evidence,
		languages, read_at, intended FROM abom_setup_preview_row WHERE preview_id = ?
		ORDER BY `+order+`, repository LIMIT ? OFFSET ?`, id, limit, offset)
	if items != nil {
		page.Items = items
	}
	return page, err
}

const previewColumns = `SELECT p.row_id, p.created_at, p.created_by, p.view_name, p.predicate, p.accounts,
	p.excluded, COALESCE(p.approved_at, ''), COALESCE(p.approved_by, ''),
	COALESCE((SELECT b.row_id FROM abom_setup_batch b WHERE b.preview_id = p.row_id), 0)
	FROM abom_setup_preview p`

func scanPreview(row *sql.Rows) (SetupPreview, error) {
	var p SetupPreview
	var created, accounts, excluded string
	if err := row.Scan(&p.ID, &created, &p.CreatedBy, &p.ViewName, &p.Predicate, &accounts, &excluded,
		&p.ApprovedAt, &p.ApprovedBy, &p.BatchID); err != nil {
		return p, err
	}
	p.CreatedAt = parseStoredTime(created)
	p.Accounts, p.Excluded, p.Rows = listOf(accounts), listOf(excluded), []SetupPreviewRow{}
	return p, nil
}

func scanPreviewRow(row *sql.Rows) (SetupPreviewRow, error) {
	var r SetupPreviewRow
	var languages string
	err := row.Scan(&r.Repository, &r.Class, &r.Evidence, &languages, &r.ReadAt, &r.Intended)
	r.Languages = listOf(languages)
	return r, err
}

func (s *Store) previewCounts(id int64) (map[string]int, error) {
	out := map[string]int{}
	rows, err := s.db.Query(`SELECT class, COUNT(*) FROM abom_setup_preview_row WHERE preview_id = ? GROUP BY class`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var n int
		if err := rows.Scan(&class, &n); err != nil {
			return nil, err
		}
		out[class] = n
	}
	return out, rows.Err()
}

// RecordRepositoryConfiguration records what one repository says about its own configuration: nil
// is GitHub saying none is attached, which is an answer and settles enforcement for it.
func (s *Store) RecordRepositoryConfiguration(repository string, cfg *SecurityConfiguration, status string, at time.Time) error {
	account, _, _ := strings.Cut(repository, "/")
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	id := int64(0)
	if cfg != nil {
		id = cfg.ID
		if _, err := tx.Exec(`INSERT INTO abom_security_configuration (account, configuration_id, name,
			target_type, enforcement, default_setup, allow_advanced, runner_type, runner_label)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(account, configuration_id) DO UPDATE SET name = excluded.name,
			  target_type = excluded.target_type, enforcement = excluded.enforcement,
			  default_setup = excluded.default_setup, allow_advanced = excluded.allow_advanced,
			  runner_type = excluded.runner_type, runner_label = excluded.runner_label`,
			account, cfg.ID, cfg.Name, cfg.TargetType, cfg.Enforcement, cfg.DefaultSetup,
			nullableBool(cfg.AllowAdvanced), cfg.RunnerType, cfg.RunnerLabel); err != nil {
			return fmt.Errorf("recording the configuration of %s: %w", repository, err)
		}
	}
	if status == "" {
		status = "none"
	}
	if _, err := tx.Exec(`INSERT INTO abom_configuration_attachment (repository, account, configuration_id, status, read_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(repository) DO UPDATE SET account = excluded.account,
		  configuration_id = excluded.configuration_id, status = excluded.status, read_at = excluded.read_at`,
		repository, account, id, status, at.UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("recording the attachment of %s: %w", repository, err)
	}
	return tx.Commit()
}

func jsonList(v []string) string {
	if v == nil {
		v = []string{}
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// listOf reads a JSON list, answering an empty list for text that holds none.
func listOf(raw string) []string {
	out := []string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func nullableInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// SetupBatchState reads one batch's state.
func (s *Store) SetupBatchState(id int64) (string, error) {
	var state string
	err := s.db.QueryRow(`SELECT state FROM abom_setup_batch WHERE row_id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("batch %d does not exist", id)
	}
	return state, err
}

// SetupDeferral is a row whose last step was deferred: when, and how often it has been.
type SetupDeferral struct {
	At    time.Time
	Count int
}

// SetupDeferred lists the rows of a batch whose last step was deferred.
func (s *Store) SetupDeferred(batch int64) (map[string]SetupDeferral, error) {
	out := map[string]SetupDeferral{}
	rows, err := s.db.Query(`SELECT w.repository, w.at,
		       (SELECT COUNT(*) FROM abom_setup_write d
		         WHERE d.batch_id = w.batch_id AND d.repository = w.repository AND d.step = 'deferred')
		  FROM abom_setup_write w
		 WHERE w.batch_id = ? AND w.step = 'deferred'
		   AND w.row_id = (SELECT MAX(x.row_id) FROM abom_setup_write x
		                    WHERE x.batch_id = w.batch_id AND x.repository = w.repository)`, batch)
	if err != nil {
		return nil, fmt.Errorf("reading the deferrals of batch %d: %w", batch, err)
	}
	defer rows.Close()
	for rows.Next() {
		var repository, at string
		var n int
		if err := rows.Scan(&repository, &at, &n); err != nil {
			return nil, err
		}
		out[strings.ToLower(repository)] = SetupDeferral{At: parseStoredTime(at), Count: n}
	}
	return out, rows.Err()
}
