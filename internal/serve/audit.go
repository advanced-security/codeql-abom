package serve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/audit"
	"github.com/advanced-security/codeql-abom/internal/store"
)

var auditTerms = []string{"event", "request", "run", "session", "query", "version", "purpose", "hook", "tool", "decision", "step", "actor", "from", "to", "text", "order", "limit", "cursor", "through"}

func auditFilter(r *http.Request) (store.AuditFilter, error) {
	q := r.URL.Query()
	for key, values := range q {
		if len(values) != 1 {
			return store.AuditFilter{}, fmt.Errorf("%s must appear once", key)
		}
	}
	f := store.AuditFilter{Query: q.Get("query"), Version: q.Get("version"), Purpose: q.Get("purpose"), Hook: q.Get("hook"), Tool: q.Get("tool"),
		Decision: q.Get("decision"), Step: q.Get("step"), Actor: q.Get("actor"), From: q.Get("from"), To: q.Get("to"), Text: q.Get("text"), Order: q.Get("order"), Cursor: q.Get("cursor")}
	for key, p := range map[string]*int64{"event": &f.Event, "request": &f.Request, "run": &f.Run, "session": &f.Session, "through": &f.Through} {
		if raw := q.Get(key); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 || n == 0 && key != "through" {
				return f, fmt.Errorf("%s must be a positive integer", key)
			}
			*p = n
			if key == "through" {
				f.FixedCutoff = true
			}
		}
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			return f, errors.New("limit must be between 1 and 500")
		}
		f.Limit = n
	}
	if f.Purpose != "" && f.Purpose != "authoring" && f.Purpose != "sealed drafting" {
		return f, errors.New("purpose is authoring or sealed drafting")
	}
	if f.Order != "" && f.Order != "asc" && f.Order != "desc" {
		return f, errors.New("order is asc or desc")
	}
	for _, key := range []string{"from", "to"} {
		if raw := q.Get(key); raw != "" {
			if _, err := time.Parse(time.RFC3339Nano, raw); err != nil {
				return f, fmt.Errorf("%s must be an RFC3339 timestamp", key)
			}
		}
	}
	if f.From != "" && f.To != "" {
		from, _ := time.Parse(time.RFC3339Nano, f.From)
		to, _ := time.Parse(time.RFC3339Nano, f.To)
		if from.After(to) {
			return f, errors.New("from must precede to")
		}
	}
	if len(f.Text) > 4096 || len(f.Cursor) > 4096 {
		return f, errors.New("audit search or cursor is too long")
	}
	return f, nil
}

// scopeToSession narrows a filter to the session its route names. A request, run or other session
// in the query could only narrow that session further, to nothing, so it is refused.
func scopeToSession(f *store.AuditFilter, id int64) error {
	switch {
	case f.Session != 0 && f.Session != id:
		return errors.New("session conflicts with route")
	case f.Request != 0 || f.Run != 0:
		return errors.New("the route names the session; request and run do not apply")
	}
	f.Session = id
	return nil
}

func auditError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, store.ErrNoAuditSession):
		code = http.StatusNotFound
	case errors.Is(err, store.ErrBadAuditFilter):
		code = http.StatusBadRequest
	}
	writeError(w, code, err.Error())
}

func (s *Server) auditEvents(w http.ResponseWriter, r *http.Request) {
	f, err := auditFilter(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if raw := r.PathValue("id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, 400, "a session is a positive integer")
			return
		}
		if err := scopeToSession(&f, id); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	page, err := s.Store.AuditEvents(f)
	if err != nil {
		auditError(w, err)
		return
	}
	writeJSON(w, page, nil)
}

func (s *Server) auditSession(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "a session is a positive integer")
		return
	}
	meta, err := s.Store.AuditSession(id)
	if err != nil {
		auditError(w, err)
		return
	}
	writeJSON(w, meta, nil)
}

func (s *Server) auditSessions(w http.ResponseWriter, r *http.Request) {
	f, err := auditFilter(r)
	if err != nil || f.Request == 0 && f.Run == 0 {
		writeError(w, 400, "session summaries require a request or run")
		return
	}
	items, err := s.Store.AuditSessions(f.Request, f.Run)
	if err != nil {
		auditError(w, err)
		return
	}
	writeJSON(w, map[string]any{"items": items, "total": len(items)}, nil)
}

func (s *Server) auditExport(w http.ResponseWriter, r *http.Request) {
	f, err := auditFilter(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if f.Cursor != "" {
		writeError(w, 400, "export takes filters and a cutoff, not a page cursor")
		return
	}
	page, err := s.Store.AuditEvents(f)
	if err != nil {
		auditError(w, err)
		return
	}
	f.Through, f.FixedCutoff = page.Through, true
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="codeql-audit.jsonl"`)
	enc := json.NewEncoder(w)
	if err := enc.Encode(map[string]any{"type": "metadata", "schema": audit.Version, "filter": f, "through": page.Through, "total": page.Total, "coverage": page.Coverage}); err != nil {
		return
	}
	count := 0
	// Cutoff zero denotes an empty snapshot, not a later reading of newly appended events.
	if page.Through > 0 {
		err = s.Store.WalkAudit(f, func(e store.AuthoringEvent) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			if err := enc.Encode(map[string]any{"type": "event", "event": e}); err != nil {
				return err
			}
			count++
			return nil
		})
	}
	if err != nil || r.Context().Err() != nil || count != page.Total {
		return
	}
	_ = enc.Encode(map[string]any{"type": "completion", "complete": true, "events": count, "through": page.Through})
}

const reproductionLimits = "This is recorded evidence, not a replay or an authorship claim. Digests cannot recover files. Source, patches and scratch are not archived. Obtain matching query, pack dependencies, fixtures, expected results and toolchain independently; unavailable or changed inputs prevent a claim of exact reproduction. Model messages and successful tool returns do not prove a CodeQL test passed. Events, policies and observed coverage use the stated cutoff; session status and non-event relations describe the current record."

func (s *Server) auditReproduction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "a session is a positive integer")
		return
	}
	f, err := auditFilter(r)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := scopeToSession(&f, id); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	page, err := s.Store.AuditEvents(f)
	if err != nil {
		auditError(w, err)
		return
	}
	meta, err := s.Store.AuditSession(id, page.Through)
	if err != nil {
		auditError(w, err)
		return
	}
	if !r.URL.Query().Has("download") {
		writeJSON(w, map[string]any{"session": meta, "evidence": page, "limitations": reproductionLimits}, nil)
		return
	}
	f.Cursor, f.Through, f.FixedCutoff = "", page.Through, true
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="session-%d-reproduction.md"`, id))
	s.writeAuditGuide(w, r, meta, f, page)
}

// md makes recorded text inert in the guide. CommonMark lets any ASCII punctuation character be
// backslash-escaped, so escaping every one leaves no link, image, emphasis, code span, HTML,
// entity, math or bare-address autolink to form from text an agent controls, and no `|` to split
// a table cell, while a viewer still shows exactly what was recorded. A line break would end a
// table row, so it is a space.
func md(s string) string {
	var b strings.Builder
	b.Grow(len(s) + len(s)/4)
	for _, r := range s {
		switch {
		case r == '\r' || r == '\n':
			b.WriteByte(' ')
		case strings.ContainsRune(markdownPunctuation, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

const markdownPunctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"
