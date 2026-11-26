package serve

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// causeOther is an unavailable row whose reason Git or a probe did not word, so it is not named.
const causeOther = "other"

// exposition writes Prometheus' text format, one gauge family at a time.
type exposition struct{ b strings.Builder }

func (e *exposition) family(name, help string) {
	fmt.Fprintf(&e.b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
}

func (e *exposition) sample(name string, value int64, labels ...string) {
	pairs := make([]string, 0, len(labels)/2)
	for i := 0; i+1 < len(labels); i += 2 {
		pairs = append(pairs, labels[i]+`="`+labelEscaper.Replace(labels[i+1])+`"`)
	}
	fmt.Fprintf(&e.b, "%s{%s} %s\n", name, strings.Join(pairs, ","), strconv.FormatInt(value, 10))
}

var labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

// coverageMetrics serves what an alert needs about coverage: the queue by tier and state,
// unavailable rows by cause with the oldest one's settling time, and which declared accounts
// discovery could not read.
//
// A time nobody recorded is no series at all, never a zero, which an alert on age would read as
// fifty years.
func (s *Server) coverageMetrics(w http.ResponseWriter, r *http.Request) {
	var e exposition

	tallies, err := s.Store.FrontierTallies(s.queue().StuckAfterAttempts())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	e.family("abom_frontier_rows", "Queue rows by tier and state; stuck rows failed at least the attempt ceiling.")
	for _, t := range tallies {
		e.sample("abom_frontier_rows", int64(t.Rows), "tier", t.Tier, "state", t.State)
	}

	unavailable, err := s.Store.UnavailableRows()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	type key struct{ tier, cause string }
	rows, oldest := map[key]int{}, map[key]time.Time{}
	for _, u := range unavailable {
		k := key{u.Tier, expand.Cause(u.Reason)}
		if k.cause == "" {
			k.cause = causeOther
		}
		rows[k]++
		if at, err := time.Parse(time.RFC3339Nano, u.SettledAt); err == nil && (oldest[k].IsZero() || at.Before(oldest[k])) {
			oldest[k] = at
		}
	}
	tiers := []string{store.TierOwned, store.TierDirect, store.TierTransitive}
	causes := []string{expand.CauseAccess, expand.CauseAbsent, causeOther}
	e.family("abom_frontier_unavailable_rows",
		"Unavailable queue rows by tier and cause: access needs the App's access, absent names a reference that is gone.")
	for _, tier := range tiers {
		for _, cause := range causes {
			e.sample("abom_frontier_unavailable_rows", int64(rows[key{tier, cause}]), "tier", tier, "cause", cause)
		}
	}
	e.family("abom_frontier_unavailable_oldest_timestamp_seconds",
		"When the longest-unavailable row of a tier and cause was settled; absent when there is none, or none recorded when.")
	for _, tier := range tiers {
		for _, cause := range causes {
			if at := oldest[key{tier, cause}]; !at.IsZero() {
				e.sample("abom_frontier_unavailable_oldest_timestamp_seconds", at.Unix(), "tier", tier, "cause", cause)
			}
		}
	}

	// Every declared account, not only those a decision verifies: an undecided one is still one
	// nothing reads, which is what this reports.
	declared, err := s.Store.OwnedScope()
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	slices.SortFunc(declared, func(a, b store.DeclaredAccount) int { return strings.Compare(a.Owner, b.Owner) })
	type accountRead struct {
		owner  string
		unread bool
		read   time.Time
	}
	var readings []accountRead
	for _, a := range declared {
		if !a.Enabled {
			continue
		}
		marks, _, err := s.Store.EnumerationState(a.Owner)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		read := marks.ListedAt
		if marks.SweptAt.After(read) {
			read = marks.SweptAt
		}
		readings = append(readings, accountRead{a.Owner, read.IsZero() || len(marks.Failures) > 0, read})
	}
	e.family("abom_account_unread",
		"1 when a declared account was never read, or its last read, or that of a repository in it, failed.")
	for _, r := range readings {
		var unread int64
		if r.unread {
			unread = 1
		}
		e.sample("abom_account_unread", unread, "account", r.owner)
	}
	e.family("abom_account_last_read_timestamp_seconds", "When a declared account was last listed or swept; absent when never.")
	for _, r := range readings {
		if !r.read.IsZero() {
			e.sample("abom_account_last_read_timestamp_seconds", r.read.Unix(), "account", r.owner)
		}
	}

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(e.b.String()))
}
