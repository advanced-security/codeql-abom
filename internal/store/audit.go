package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/audit"
)

// ErrNoAuditSession is a request, run or session that is not recorded. The error returned names
// which one, and unwraps to this.
var ErrNoAuditSession = errors.New("not recorded")

// ErrBadAuditFilter marks a filter, page size, order or cursor the store refuses: the reader's to
// correct, not a failure of the store.
var ErrBadAuditFilter = errors.New("refused audit filter")

type refusedFilter string

func (e refusedFilter) Error() string { return string(e) }

func (e refusedFilter) Unwrap() error { return ErrBadAuditFilter }

type notRecorded struct {
	kind string
	id   int64
}

func (e notRecorded) Error() string { return fmt.Sprintf("no %s %d is recorded", e.kind, e.id) }

func (e notRecorded) Unwrap() error { return ErrNoAuditSession }

type AuditCapture struct {
	Version          int               `json:"version"`
	RuntimeSessionID string            `json:"runtimeSessionId,omitempty"`
	CallID           string            `json:"callId,omitempty"`
	Correlation      string            `json:"correlation"`
	Policy           *audit.Policy     `json:"policy,omitempty"`
	Test             *QueryTest        `json:"test,omitempty"`
	Inputs           map[string]string `json:"inputs,omitempty"`
	ArtifactID       int64             `json:"artifactId,omitempty"`
}

type AuditFilter struct {
	Event       int64  `json:"event,omitempty"`
	Request     int64  `json:"request,omitempty"`
	Run         int64  `json:"run,omitempty"`
	Session     int64  `json:"session,omitempty"`
	Query       string `json:"query,omitempty"`
	Version     string `json:"version,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	Hook        string `json:"hook,omitempty"`
	Tool        string `json:"tool,omitempty"`
	Decision    string `json:"decision,omitempty"`
	Step        string `json:"step,omitempty"`
	Actor       string `json:"actor,omitempty"`
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Text        string `json:"text,omitempty"`
	Order       string `json:"order,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Cursor      string `json:"cursor,omitempty"`
	Through     int64  `json:"through,omitempty"`
	FixedCutoff bool   `json:"-"`
}

type AuditPage struct {
	Items      []AuthoringEvent `json:"items"`
	Total      int              `json:"total"`
	NextCursor string           `json:"nextCursor"`
	HasMore    bool             `json:"hasMore"`
	Through    int64            `json:"through"`
	Coverage   string           `json:"coverage"`
}

type AuditSession struct {
	AuthoringSession
	Through   int64            `json:"through"`
	RunID     int64            `json:"runId"`
	QueryID   string           `json:"queryId"`
	Purpose   string           `json:"purpose"`
	Policies  []AuthoringEvent `json:"policies"`
	Coverage  string           `json:"coverage"`
	Relations []AuditRelation  `json:"relations"`
	Observed  map[string]int   `json:"observed"`
}

type AuditRelation struct {
	Kind      string `json:"kind"`
	RequestID int64  `json:"requestId,omitempty"`
	RunID     int64  `json:"runId,omitempty"`
	QueryID   string `json:"queryId,omitempty"`
	Version   string `json:"version,omitempty"`
	DraftID   int64  `json:"draftId,omitempty"`
}

const auditCoverage = "Events, not tool calls. Hook registration is not observation; ordinary SDK hooks have no call IDs. Historical policies and missing inputs are not recorded. Text search covers sanitized envelope fields and safe structured details only."

func marshalCapture(c AuditCapture) (string, error) {
	if c.Policy != nil {
		if _, err := c.Policy.JSON(); err != nil {
			return "", err
		}
	}
	b, err := json.Marshal(c)
	if len(b) > audit.MaxDetail {
		return "", errors.New("complete audit capture exceeds 16 KiB")
	}
	return string(b), err
}

func safeAuditEvent(e AuthoringEvent, historic bool) AuthoringEvent {
	e.Tool, e.Subject, e.Reason = audit.Text(e.Tool), audit.Text(e.Subject), audit.Text(e.Reason)
	e.Actor = audit.Text(e.Actor)
	if historic {
		e.Detail = `{"omitted":"historic payload not captured under the current policy"}`
		e.Capture.Correlation = "not recorded"
	} else {
		e.Detail = audit.ProjectDetail(e.Detail)
		if e.Capture.Test != nil {
			test := *e.Capture.Test
			test.Detail = audit.Text(test.Detail)
			e.Capture.Test = &test
		}
	}
	return e
}

type auditCursor struct {
	Filter  string `json:"f"`
	Through int64  `json:"t"`
	After   int64  `json:"a"`
}

func (f AuditFilter) fingerprint() string {
	f.Cursor, f.Through, f.Limit, f.FixedCutoff = "", 0, 0, false
	b, _ := json.Marshal(f)
	return audit.Digest(b)
}

func (s *Store) auditCutoff(f AuditFilter) (AuditFilter, auditCursor, error) {
	if f.Limit == 0 {
		f.Limit = 100
	}
	if f.Limit < 1 || f.Limit > 500 {
		return f, auditCursor{}, refusedFilter("limit must be between 1 and 500")
	}
	if f.Order == "" {
		f.Order = "desc"
		if f.Session > 0 {
			f.Order = "asc"
		}
	}
	for _, owner := range []struct {
		id    int64
		table string
		name  string
	}{{f.Request, "abom_query_request", "request"}, {f.Run, "abom_analysis_run", "run"}} {
		if owner.id <= 0 {
			continue
		}
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM `+owner.table+` WHERE row_id=?`, owner.id).Scan(&exists); err != nil {
			return f, auditCursor{}, err
		}
		if exists == 0 {
			return f, auditCursor{}, notRecorded{owner.name, owner.id}
		}
	}
	if f.Order != "asc" && f.Order != "desc" {
		return f, auditCursor{}, refusedFilter("order must be asc or desc")
	}
	if f.Session > 0 {
		var exists int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_authoring_session WHERE row_id=?`, f.Session).Scan(&exists); err != nil {
			return f, auditCursor{}, err
		}
		if exists == 0 {
			return f, auditCursor{}, notRecorded{"session", f.Session}
		}
	}
	c := auditCursor{Filter: f.fingerprint(), Through: f.Through}
	if f.Cursor != "" {
		refused := refusedFilter("cursor does not match these filters, order and cutoff")
		raw, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Filter != f.fingerprint() || c.Through < 0 || c.After <= 0 {
			return f, c, refused
		}
		switch {
		case !f.FixedCutoff && f.Through == 0, f.Through == c.Through:
		// Oldest first, a later cutoff only extends the walk past where the page stopped: the next
		// events a reader following the log reads. Newest first it would put rows ahead of pages
		// already read, and an earlier cutoff would drop rows already shown.
		case f.Order == "asc" && f.Through > c.Through:
			c.Through = f.Through
		default:
			return f, c, refused
		}
	} else if c.Through == 0 && !f.FixedCutoff {
		if err := s.db.QueryRow(`SELECT COALESCE(MAX(row_id),0) FROM abom_authoring_event`).Scan(&c.Through); err != nil {
			return f, c, err
		}
	}
	return f, c, nil
}

const auditColumns = `e.row_id,e.session_id,e.at,e.hook,e.tool,e.subject,e.decision,e.step,e.reason,e.detail,e.took_ms,
	e.source,e.source_id,e.received_at,e.query_id,e.query_version,e.policy_id,e.capture,
	COALESCE(s.request_id,0),COALESCE(s.run_id,0),s.started_by`

func scanAudit(rows *sql.Rows) (AuthoringEvent, error) {
	var e AuthoringEvent
	var capture string
	err := rows.Scan(&e.RowID, &e.SessionID, &e.At, &e.Hook, &e.Tool, &e.Subject, &e.Decision, &e.Step, &e.Reason, &e.Detail, &e.TookMs,
		&e.Source, &e.SourceID, &e.ReceivedAt, &e.QueryID, &e.QueryVersion, &e.PolicyID, &capture, &e.RequestID, &e.RunID, &e.Actor)
	if err != nil {
		return e, err
	}
	if capture != "" && json.Unmarshal([]byte(capture), &e.Capture) != nil {
		return e, errors.New("invalid stored audit capture")
	}
	e.Purpose = "authoring"
	if e.RunID > 0 {
		e.Purpose = "sealed drafting"
	}
	return safeAuditEvent(e, e.Capture.Version == 0), nil
}

func auditWhere(f AuditFilter, through int64) (string, []any) {
	where := `e.row_id <= ?`
	args := []any{through}
	add := func(clause string, arg any) { where += " AND " + clause; args = append(args, arg) }
	if f.Session > 0 {
		add("e.session_id = ?", f.Session)
	}
	if f.Event > 0 {
		add("e.row_id = ?", f.Event)
	}
	if f.Request > 0 {
		add("s.request_id = ?", f.Request)
	}
	if f.Run > 0 {
		add(`(s.run_id = ? OR s.request_id IN (SELECT row_id FROM abom_query_request WHERE source_run_id = ?)
			OR e.session_id IN (SELECT v.session_id FROM abom_authoring_event v
			  JOIN abom_analysis_query q ON q.query_id=v.query_id AND q.query_version=v.query_version
			  WHERE v.query_version<>'' AND q.run_id=?))`, f.Run)
		args = append(args, f.Run, f.Run)
	}
	if f.Query != "" {
		add(`(e.query_id=? OR s.query_id=? OR s.request_id IN (SELECT row_id FROM abom_query_request WHERE query_id=?))`, f.Query)
		args = append(args, f.Query, f.Query)
	}
	if f.Version != "" {
		add("e.query_version=?", f.Version)
	}
	if f.Purpose == "authoring" {
		where += " AND s.request_id IS NOT NULL"
	}
	if f.Purpose == "sealed drafting" {
		where += " AND s.run_id IS NOT NULL"
	}
	if f.Hook != "" {
		add("e.hook=?", f.Hook)
	}
	if f.Decision != "" {
		add("e.decision=?", f.Decision)
	}
	if f.Step != "" {
		add("e.step=?", f.Step)
	}
	return where, args
}

// AuditEvents scans sanitized matches in constant working space; row IDs order equal timestamps.
func (s *Store) AuditEvents(filter AuditFilter) (AuditPage, error) {
	page := AuditPage{Items: []AuthoringEvent{}, Coverage: auditCoverage}
	f, c, err := s.auditCutoff(filter)
	if err != nil {
		return page, err
	}
	page.Through = c.Through
	where, args := auditWhere(f, c.Through)
	if f.Text == "" && f.Tool == "" && f.Actor == "" && f.From == "" && f.To == "" {
		return s.auditKeysetPage(f, c, where, args)
	}
	rows, err := s.db.Query(`SELECT `+auditColumns+` FROM abom_authoring_event e JOIN abom_authoring_session s ON s.row_id=e.session_id WHERE `+where+` ORDER BY e.row_id `+f.Order, args...)
	if err != nil {
		return page, fmt.Errorf("reading audit events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return page, err
		}
		if !auditMatches(f, e) {
			continue
		}
		page.Total++
		if c.After != 0 && (f.Order == "asc" && e.RowID <= c.After || f.Order == "desc" && e.RowID >= c.After) {
			continue
		}
		if len(page.Items) < f.Limit {
			page.Items = append(page.Items, e)
		} else {
			page.HasMore = true
		}
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if page.HasMore {
		c.After = page.Items[len(page.Items)-1].RowID
		b, _ := json.Marshal(c)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(b)
	}
	return page, nil
}

func auditMatches(f AuditFilter, e AuthoringEvent) bool {
	if f.Tool != "" && f.Tool != e.Tool || f.Actor != "" && f.Actor != e.Actor {
		return false
	}
	if !auditTimeMatches(f, e.At) {
		return false
	}
	text := strings.Join([]string{e.Tool, e.Subject, e.Reason, e.Detail, e.Hook, e.Step, e.Decision, e.Actor}, " ")
	return f.Text == "" || strings.Contains(strings.ToLower(text), strings.ToLower(f.Text))
}

func auditTimeMatches(f AuditFilter, raw string) bool {
	if f.From == "" && f.To == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return false
	}
	if f.From != "" {
		from, err := time.Parse(time.RFC3339Nano, f.From)
		if err != nil || at.Before(from) {
			return false
		}
	}
	if f.To != "" {
		to, err := time.Parse(time.RFC3339Nano, f.To)
		if err != nil || at.After(to) {
			return false
		}
	}
	return true
}

func (s *Store) auditKeysetPage(f AuditFilter, c auditCursor, where string, args []any) (AuditPage, error) {
	page := AuditPage{Items: []AuthoringEvent{}, Through: c.Through, Coverage: auditCoverage}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_authoring_event e JOIN abom_authoring_session s
		ON s.row_id=e.session_id WHERE `+where, args...).Scan(&page.Total); err != nil {
		return page, err
	}
	if c.After > 0 {
		op := ">"
		if f.Order == "desc" {
			op = "<"
		}
		where += " AND e.row_id " + op + " ?"
		args = append(args, c.After)
	}
	args = append(args, f.Limit+1)
	rows, err := s.db.Query(`SELECT `+auditColumns+` FROM abom_authoring_event e JOIN abom_authoring_session s
		ON s.row_id=e.session_id WHERE `+where+` ORDER BY e.row_id `+f.Order+` LIMIT ?`, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return page, err
		}
		if len(page.Items) == f.Limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, e)
	}
	if page.HasMore {
		c.After = page.Items[len(page.Items)-1].RowID
		body, _ := json.Marshal(c)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(body)
	}
	return page, rows.Err()
}

// WalkAudit streams a fixed-cutoff filtered export without accumulating a session transcript.
func (s *Store) WalkAudit(f AuditFilter, visit func(AuthoringEvent) error) error {
	f, c, err := s.auditCutoff(f)
	if err != nil {
		return err
	}
	where, args := auditWhere(f, c.Through)
	rows, err := s.db.Query(`SELECT `+auditColumns+` FROM abom_authoring_event e JOIN abom_authoring_session s ON s.row_id=e.session_id WHERE `+where+` ORDER BY e.row_id `+f.Order, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return err
		}
		if auditMatches(f, e) {
			if err := visit(e); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *Store) AuditSessions(request, run int64) ([]AuditSession, error) {
	if request <= 0 && run <= 0 {
		return nil, errors.New("session summaries require a request or run")
	}
	if _, _, err := s.auditCutoff(AuditFilter{Request: request, Run: run}); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT row_id FROM abom_authoring_session WHERE
		(? = 0 OR request_id=?) AND (? = 0 OR (run_id=? OR
		request_id IN (SELECT row_id FROM abom_query_request WHERE source_run_id=?) OR
		row_id IN (SELECT e.session_id FROM abom_authoring_event e JOIN abom_analysis_query q
		ON q.query_id=e.query_id AND q.query_version=e.query_version WHERE e.query_version<>'' AND q.run_id=?)))
		ORDER BY row_id DESC`, request, request, run, run, run, run)
	if err != nil {
		return nil, err
	}
	ids, err := collectRows(rows, func(rows *sql.Rows) (int64, error) { var id int64; err := rows.Scan(&id); return id, err })
	if err != nil {
		return nil, err
	}
	out := []AuditSession{}
	for _, id := range ids {
		a, err := s.AuditSession(id)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) AuditSession(id int64, through ...int64) (AuditSession, error) {
	a := AuditSession{Policies: []AuthoringEvent{}, Relations: []AuditRelation{}, Observed: map[string]int{}, Coverage: auditCoverage}
	f := AuditFilter{Session: id, Limit: 500}
	if len(through) > 0 {
		f.Through, f.FixedCutoff = through[0], true
	}
	_, cutoff, err := s.auditCutoff(f)
	if err != nil {
		return a, err
	}
	a.Through, f.Through, f.FixedCutoff = cutoff.Through, cutoff.Through, true
	var calls, input, output, nano sql.NullInt64
	err = s.db.QueryRow(`SELECT row_id,COALESCE(request_id,0),COALESCE(run_id,0),query_id,state,started_by,model,started_at,COALESCE(ended_at,''),detail,
		model_calls,input_tokens,output_tokens,nano_aiu
		FROM abom_authoring_session WHERE row_id=?`, id).Scan(&a.RowID, &a.RequestID, &a.RunID, &a.QueryID, &a.State, &a.StartedBy, &a.Model, &a.StartedAt, &a.EndedAt, &a.Detail,
		&calls, &input, &output, &nano)
	if errors.Is(err, sql.ErrNoRows) {
		return a, notRecorded{"session", id}
	}
	if err != nil {
		return a, err
	}
	a.ModelCalls, a.InputTokens = reported(calls), reported(input)
	a.OutputTokens, a.NanoAIU = reported(output), reported(nano)
	a.Detail, a.StartedBy, a.Model = audit.Text(a.Detail), audit.Text(a.StartedBy), audit.Text(a.Model)
	a.Purpose = "authoring"
	if a.RequestID > 0 {
		a.Relations = append(a.Relations, AuditRelation{Kind: "worked request", RequestID: a.RequestID})
	}
	if a.RequestID > 0 {
		var run, draft int64
		if err := s.db.QueryRow(`SELECT COALESCE(source_run_id,0),COALESCE(source_draft_id,0) FROM abom_query_request WHERE row_id=?`, a.RequestID).Scan(&run, &draft); err != nil {
			return a, err
		}
		if run > 0 {
			a.Relations = append(a.Relations, AuditRelation{Kind: "request filed from run", RunID: run, RequestID: a.RequestID, DraftID: draft})
		}
	}
	if a.RunID > 0 {
		a.Purpose = "sealed drafting"
		a.Relations = append(a.Relations, AuditRelation{Kind: "drafted from run", RunID: a.RunID})
	}
	if a.RunID > 0 {
		rows, err := s.db.Query(`SELECT r.row_id,r.source_draft_id FROM abom_query_request r
			JOIN abom_request_draft d ON d.row_id=r.source_draft_id WHERE d.session_id=? ORDER BY r.row_id`, id)
		if err != nil {
			return a, err
		}
		relations, err := collectRows(rows, func(rows *sql.Rows) (AuditRelation, error) {
			r := AuditRelation{Kind: "request filed using draft"}
			err := rows.Scan(&r.RequestID, &r.DraftID)
			return r, err
		})
		if err != nil {
			return a, err
		}
		a.Relations = append(a.Relations, relations...)
	}
	for {
		p, err := s.AuditEvents(f)
		if err != nil {
			return a, err
		}
		a.Events = p.Total
		for _, e := range p.Items {
			a.Observed[e.Source+":"+e.Hook]++
			if e.Capture.Policy != nil {
				a.Policies = append(a.Policies, e)
			}
			if e.Decision == "refused" {
				a.Refused++
			}
		}
		if !p.HasMore {
			break
		}
		f.Cursor = p.NextCursor
	}
	rows, err := s.db.Query(`SELECT DISTINCT e.query_id,e.query_version,q.run_id FROM abom_authoring_event e
		LEFT JOIN abom_analysis_query q ON q.query_id=e.query_id AND q.query_version=e.query_version
		WHERE e.session_id=? AND e.query_version<>'' AND e.row_id<=? ORDER BY e.query_id,e.query_version,q.run_id`, id, a.Through)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		r := AuditRelation{Kind: "observed version"}
		var run sql.NullInt64
		if err := rows.Scan(&r.QueryID, &r.Version, &run); err != nil {
			return a, err
		}
		if run.Valid {
			r.RunID, r.Kind = run.Int64, "version evaluated by run"
		}
		a.Relations = append(a.Relations, r)
	}
	return a, rows.Err()
}
