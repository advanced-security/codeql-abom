package store

import (
	"slices"
	"testing"
	"time"
)

// An account discovery could not read used to leave one line in a log and the supervisor's last
// error, so a declared organization the App was never installed on read as an organization with
// nothing in it. The failure is kept beside the account, and its repositories' beside them, until
// a read answers.
func TestAReadThatFailedIsKeptBesideTheAccountUntilOneAnswers(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "advanced-security", Kind: AccountOrganization, Enabled: true})
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for subject, reason := range map[string]string{
		"advanced-security":                   "the GitHub App is not installed on this account",
		"advanced-security/codeql-abom-test1": "Not Found",
		"advanced-security-labs":              "SAML enforcement",
		"advanced":                            "rate limited",
	} {
		if err := s.RecordEnumerationFailure(subject, reason, at); err != nil {
			t.Fatalf("RecordEnumerationFailure(%s): %v", subject, err)
		}
	}
	later := at.Add(time.Hour)
	if err := s.RecordEnumerationFailure("ADVANCED-SECURITY", "the GitHub App is still not installed", later); err != nil {
		t.Fatal(err)
	}

	// Never listed, so no marks; the failure is what says why.
	marks, known, err := s.EnumerationState("advanced-security")
	if err != nil || known {
		t.Fatalf("EnumerationState = known %v, %v; the account was never listed", known, err)
	}
	want := []EnumerationFailure{
		{Subject: "advanced-security", FailedAt: later, Reason: "the GitHub App is still not installed"},
		{Subject: "advanced-security/codeql-abom-test1", FailedAt: at, Reason: "Not Found"},
	}
	if !slices.Equal(marks.Failures, want) {
		t.Errorf("failures = %+v, want the account's latest, then its repository's, and no other account's", marks.Failures)
	}

	if err := s.ClearEnumerationFailure("Advanced-Security"); err != nil {
		t.Fatal(err)
	}
	marks, _, _ = s.EnumerationState("advanced-security")
	if !slices.Equal(marks.Failures, want[1:]) {
		t.Errorf("after the account answered, failures = %+v; a repository it did not read is still unread", marks.Failures)
	}
	if marks, _, _ := s.EnumerationState("octo"); marks.Failures == nil || len(marks.Failures) != 0 {
		t.Errorf("an account with no failure reads %#v, want an empty list rather than null", marks.Failures)
	}
}

// A repository's failure is kept only while the declaration covers it: one dropped from the
// declaration is not something its account failed to read, as an absence it no longer covers is not.
func TestAFailureTheDeclarationNoLongerCoversIsNotKept(t *testing.T) {
	s := newTestStore(t)
	declare(t, s, DeclaredAccount{Owner: "acme", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"kept"}})
	at := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for _, subject := range []string{"acme", "acme/kept", "acme/dropped"} {
		if err := s.RecordEnumerationFailure(subject, "Not Found", at); err != nil {
			t.Fatal(err)
		}
	}
	marks, _, err := s.EnumerationState("acme")
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, f := range marks.Failures {
		subjects = append(subjects, f.Subject)
	}
	if !slices.Equal(subjects, []string{"acme", "acme/kept"}) {
		t.Errorf("failures kept for %v; want the account's own and the repository it still declares", subjects)
	}
}

type resyncRow struct {
	key, ref, owner, repo, status string
	depth, attempts               int
	next, reason, awaiting        any
}

func insertResyncRow(t *testing.T, s *Store, r resyncRow) {
	t.Helper()
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, attempts, score, enqueued_at,
			next_attempt_at, reason, awaiting_code_scanning, analysis_count, resolved_sha, analyzed_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, '2026-01-01T00:00:00Z', ?, ?, ?, 1, 'sha', '2026-01-02T00:00:00Z',
			CASE WHEN ? IN ('done', 'nothing', 'unavailable') THEN '2026-01-02T00:00:00Z' END)`,
		r.key, r.ref, r.owner, r.repo, r.depth, r.status, r.attempts, r.next, r.reason, r.awaiting, r.status); err != nil {
		t.Fatalf("inserting %s@%s: %v", r.key, r.ref, err)
	}
}

type rowState struct {
	Status, Reason, Next, Completed, Analyzed, SHA string
	Attempts                                       int
}

func rowStates(t *testing.T, s *Store) map[NodeRef]rowState {
	t.Helper()
	rows, err := s.DB().Query(`SELECT node_key, ref, status, attempts, COALESCE(reason, ''),
		COALESCE(next_attempt_at, ''), COALESCE(completed_at, ''), COALESCE(analyzed_at, ''),
		COALESCE(resolved_sha, '') FROM abom_frontier`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[NodeRef]rowState{}
	for rows.Next() {
		var k NodeRef
		var st rowState
		if err := rows.Scan(&k.NodeKey, &k.Ref, &st.Status, &st.Attempts, &st.Reason, &st.Next,
			&st.Completed, &st.Analyzed, &st.SHA); err != nil {
			t.Fatal(err)
		}
		out[k] = st
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Resyncing is asked of the rows somebody chose, after fixing what failed them. The only other
// answers were a whole tier, every unavailable row, or one repository typed by name, so fixing
// access to two repositories re-cloned hundreds. Each chosen row is put back as its state allows
// and every other row, including another reference of the same dependency, is left exactly as it was.
func TestResyncingPutsBackOnlyTheChosenRowsAsTheirStateAllows(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "octo", Kind: AccountOrganization, Enabled: true}},
		DeclaredByInterface); err != nil {
		t.Fatal(err)
	}
	future := "2099-01-01T00:00:00Z"
	rows := []resyncRow{
		{key: "octo/analyzed", owner: "octo", repo: "analyzed", status: "done"},
		{key: "octo/analyzed", ref: "v2", owner: "octo", repo: "analyzed", status: "done", depth: 1},
		{key: "octo/empty", owner: "octo", repo: "empty", status: "nothing", reason: "has no workflows"},
		{key: "other/private", owner: "other", repo: "private", status: "unavailable", depth: 1, attempts: 3,
			reason: "fatal: could not read Username"},
		{key: "${{ inputs.x }}/setup", owner: "${{ inputs.x }}", repo: "setup", status: "unavailable", depth: 2,
			reason: "names no repository"},
		{key: "other/flaky", ref: "v1", owner: "other", repo: "flaky", status: "pending", depth: 1, attempts: 2,
			next: future, reason: "rate limited"},
		{key: "other/queued", owner: "other", repo: "queued", status: "pending", depth: 1},
		{key: "octo/held", owner: "octo", repo: "held", status: "pending", awaiting: "abc123"},
		{key: "other/busy", owner: "other", repo: "busy", status: "running", depth: 1},
	}
	for _, r := range rows {
		insertResyncRow(t, s, r)
	}
	before := rowStates(t, s)

	chosen := []NodeRef{
		{NodeKey: "octo/analyzed"}, {NodeKey: "octo/empty"}, {NodeKey: "other/private"},
		{NodeKey: "${{ inputs.x }}/setup"}, {NodeKey: "other/flaky", Ref: "v1"}, {NodeKey: "other/queued"},
		{NodeKey: "octo/held"}, {NodeKey: "other/busy"}, {NodeKey: "octo/gone"},
	}
	got, err := s.ResyncRows(chosen)
	if err != nil {
		t.Fatalf("ResyncRows: %v", err)
	}
	if got.Requeued != 4 || got.Queued != 1 || got.Waiting != 1 || got.Running != 1 ||
		got.NamesNoRepository != 1 || got.NotFound != 1 {
		t.Errorf("resync = %+v, want 4 put back, 1 already queued, 1 held, 1 running, 1 naming no repository, 1 missing", got)
	}
	if want := []string{"octo/analyzed", "octo/empty", "octo/held"}; !slices.Equal(got.Owned, want) {
		t.Errorf("owned = %v, want %v: those are read again before they are analyzed", got.Owned, want)
	}
	if want := []string{"octo/analyzed", "octo/empty", "octo/held", "other/busy", "other/flaky", "other/private",
		"other/queued"}; !slices.Equal(got.Repositories, want) {
		t.Errorf("repositories = %v, want every chosen repository that names one: %v", got.Repositories, want)
	}

	after := rowStates(t, s)
	fresh := map[NodeRef]bool{
		{NodeKey: "octo/analyzed"}: true, {NodeKey: "octo/empty"}: true, {NodeKey: "other/private"}: true,
		{NodeKey: "other/flaky", Ref: "v1"}: true,
	}
	for k, was := range before {
		now := after[k]
		if !fresh[k] {
			if now != was {
				t.Errorf("%s@%s changed from %+v to %+v, and it was not put back", k.NodeKey, k.Ref, was, now)
			}
			continue
		}
		if now.Status != "pending" || now.Attempts != 0 || now.Next != "" {
			t.Errorf("%s@%s = %+v, want pending with its retry budget and wait reset", k.NodeKey, k.Ref, now)
		}
		// What is known stays until a new analysis replaces it, so the Explorer is not blanked meanwhile.
		if now.Analyzed != was.Analyzed || now.SHA != was.SHA {
			t.Errorf("%s@%s lost what was known about it: %+v -> %+v", k.NodeKey, k.Ref, was, now)
		}
	}
	for _, k := range []NodeRef{{NodeKey: "other/private"}, {NodeKey: "other/flaky", Ref: "v1"}} {
		if after[k].Reason != "" || after[k].Completed != "" {
			t.Errorf("%s still describes a failure it is being given another chance at: %+v", k.NodeKey, after[k])
		}
	}
}

// Settled before the classification existed, a row can be unavailable for a reason no longer
// terminal. Each is reconsidered with what the store knows of it, and only those the decision names
// go back, with a fresh budget and no reason left describing a failure that is no longer current.
func TestUnavailableRowsAreReconsideredOneByOne(t *testing.T) {
	s := newTestStore(t)
	for _, r := range []resyncRow{
		{key: "octo/empty", owner: "octo", repo: "empty", status: "unavailable", attempts: 2, reason: "retry me"},
		{key: "other/lib", owner: "other", repo: "lib", status: "unavailable", depth: 1, reason: "retry me"},
		{key: "other/gone", owner: "other", repo: "gone", status: "unavailable", depth: 1, reason: "keep me"},
		{key: "other/done", owner: "other", repo: "done", status: "done", depth: 1, reason: "retry me"},
	} {
		insertResyncRow(t, s, r)
	}
	var seen []UnavailableRow
	n, err := s.ReconsiderUnavailable(func(r UnavailableRow) bool {
		seen = append(seen, r)
		return r.Reason == "retry me"
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || len(seen) != 3 {
		t.Errorf("put back %d of %d seen, want 2 of the 3 unavailable rows", n, len(seen))
	}
	for _, r := range seen {
		if want := map[string]int{"octo/empty": 0, "other/lib": 1, "other/gone": 1}[r.NodeKey]; r.Depth != want {
			t.Errorf("%s was handed at depth %d, want %d", r.NodeKey, r.Depth, want)
		}
	}
	after := rowStates(t, s)
	for key, want := range map[string]string{"octo/empty": "pending", "other/lib": "pending", "other/gone": "unavailable", "other/done": "done"} {
		got := after[NodeRef{NodeKey: key}]
		if got.Status != want {
			t.Errorf("%s = %q, want %q", key, got.Status, want)
		}
		if want == "pending" && (got.Attempts != 0 || got.Reason != "" || got.Completed != "") {
			t.Errorf("%s went back carrying its old failure: %+v", key, got)
		}
	}
}

// An unavailable row is a reason that must still be true, and nothing else looks at one again: a
// dependency is named by no event. A probe can change only an answer about reaching a reference, so
// only those are due — once a day after the row was settled or last probed, or at once when an
// installation names the owner — and never a commit, which no ref listing names.
func TestAnUnavailableRowIsDueForAProbeDailyOrWhenItsOwnerGainsAccess(t *testing.T) {
	s := newTestStore(t)
	settled := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	q := s.Frontier(FrontierOptions{Now: func() time.Time { return settled.Add(25 * time.Hour) }})
	commit := "0123456789abcdef0123456789abcdef01234567"
	for _, r := range []resyncRow{
		{key: "octo/lib", ref: "v1", owner: "octo", repo: "lib", status: "unavailable", reason: "reach"},
		{key: "octo/recent", ref: "v1", owner: "octo", repo: "recent", status: "unavailable", reason: "reach"},
		{key: "Vendor/tool", ref: "v2", owner: "Vendor", repo: "tool", status: "unavailable", reason: "reach"},
		{key: "octo/probed", ref: "v1", owner: "octo", repo: "probed", status: "unavailable", reason: "reach"},
		{key: "octo/pinned", ref: commit, owner: "octo", repo: "pinned", status: "unavailable", reason: "reach"},
		{key: "octo/empty", ref: "v1", owner: "octo", repo: "empty", status: "unavailable", reason: "content"},
		{key: "octo/done", ref: "v1", owner: "octo", repo: "done", status: "done", reason: "reach"},
		{key: "docker://alpine", ref: "3", status: "unavailable", reason: "reach"},
	} {
		insertResyncRow(t, s, r)
	}
	for key, column := range map[string]string{"octo/recent": "completed_at", "Vendor/tool": "completed_at", "octo/probed": "checked_at"} {
		if _, err := s.DB().Exec(`UPDATE abom_frontier SET `+column+` = ? WHERE node_key = ?`,
			settled.Add(24*time.Hour).Format(time.RFC3339Nano), key); err != nil {
			t.Fatal(err)
		}
	}
	reach := func(reason string) bool { return reason == "reach" }

	due := func(owners []string, limit int) []string {
		t.Helper()
		got, err := q.DueForReprobe(24*time.Hour, owners, limit, reach)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for _, r := range got {
			if r.Owner == "" || r.Repo == "" || r.Requested == "" || r.Reason != "reach" {
				t.Errorf("%s was handed without what a probe needs: %+v", r.NodeKey, r)
			}
			keys = append(keys, r.NodeKey+"@"+r.Requested)
		}
		slices.Sort(keys)
		return keys
	}
	if got, want := due(nil, 10), []string{"octo/lib@v1"}; !slices.Equal(got, want) {
		t.Errorf("due on the daily clock = %v, want %v", got, want)
	}
	if got, want := due([]string{"vendor"}, 10), []string{"Vendor/tool@v2", "octo/lib@v1"}; !slices.Equal(got, want) {
		t.Errorf("due once vendor gained access = %v, want %v", got, want)
	}
	if got := due([]string{"vendor"}, 1); len(got) != 1 {
		t.Errorf("a limit of 1 handed %v", got)
	}
}

// A failure is two dates to a reader: when it last happened and when it is tried again. A retry's
// failure is recorded when it happens, not worked back from its next attempt, so a row put back
// unclaimed still says when it failed; an unavailable row was settled when it failed, and the probe
// that looks again since dates its last look. A row nothing has failed has no last attempt to show.
func TestTheCoverageGridDatesEachRowsLastAttemptAndProbe(t *testing.T) {
	s := newTestStore(t)
	failed := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	q := s.Frontier(FrontierOptions{Now: func() time.Time { return failed }})
	for _, r := range []resyncRow{
		{key: "octo/retry", ref: "v1", owner: "octo", repo: "retry", status: "pending"},
		{key: "octo/fresh", ref: "v1", owner: "octo", repo: "fresh", status: "pending"},
		{key: "octo/released", ref: "v1", owner: "octo", repo: "released", status: "pending"},
		{key: "octo/held", ref: "v1", owner: "octo", repo: "held", status: "pending", attempts: 2,
			next: "2026-03-04T06:00:00Z", awaiting: "abc"},
		{key: "octo/gone", ref: "v1", owner: "octo", repo: "gone", status: "unavailable", reason: "refused"},
	} {
		insertResyncRow(t, s, r)
	}
	for range 3 {
		if err := q.Fail(Key{NodeKey: "octo/retry", Ref: "v1"}, "exit status 128"); err != nil {
			t.Fatal(err)
		}
	}
	released := Key{NodeKey: "octo/released", Ref: "v1"}
	if err := q.Fail(released, "exit status 128"); err != nil {
		t.Fatal(err)
	}
	if err := q.Release(released); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET checked_at = '2026-03-05T00:00:00Z' WHERE node_key = 'octo/gone'`); err != nil {
		t.Fatal(err)
	}

	items, err := s.FrontierSnapshot("", 10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for _, it := range items {
		got[it.NodeKey] = [2]string{it.LastAttemptAt, it.ProbedAt}
	}
	for key, want := range map[string][2]string{
		"octo/retry":    {failed.Format(time.RFC3339Nano), ""},
		"octo/fresh":    {"", ""},
		"octo/released": {failed.Format(time.RFC3339Nano), ""},
		"octo/held":     {"", ""},
		"octo/gone":     {"2026-01-02T00:00:00Z", "2026-03-05T00:00:00Z"},
	} {
		if got[key] != want {
			t.Errorf("%s last attempt and probe = %q, want %q", key, got[key], want)
		}
	}
}

// An alert reads one series per tier and state, so each is present when nothing is in it, and a row
// past the ceiling is counted apart from one still retrying. An unavailable row carries its tier and
// when it was settled, which is what an alert on its age reads.
func TestTheQueueIsTalliedByTierAndStateWithStuckApart(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "octo", Kind: AccountOrganization, Enabled: true}},
		DeclaredByInterface); err != nil {
		t.Fatal(err)
	}
	for _, r := range []resyncRow{
		{key: "octo/app", owner: "octo", repo: "app", status: "done"},
		{key: "a/stuck", ref: "v1", owner: "a", repo: "stuck", status: "pending", depth: 1, attempts: 8},
		{key: "a/trying", ref: "v1", owner: "a", repo: "trying", status: "pending", depth: 1, attempts: 7},
		{key: "b/held", ref: "v1", owner: "b", repo: "held", status: "pending", depth: 2, attempts: 9, awaiting: "abc"},
		{key: "b/gone", ref: "v1", owner: "b", repo: "gone", status: "unavailable", depth: 2, reason: "gone"},
	} {
		insertResyncRow(t, s, r)
	}

	tallies, err := s.FrontierTallies(8)
	if err != nil {
		t.Fatal(err)
	}
	got := map[[2]string]int{}
	for _, tally := range tallies {
		got[[2]string{tally.Tier, tally.State}] = tally.Rows
	}
	if len(tallies) != 24 {
		t.Errorf("%d tallies, want every one of 3 tiers by 8 states", len(tallies))
	}
	for k, want := range map[[2]string]int{
		{TierOwned, StatusDone}: 1, {TierOwned, FrontierQueued}: 0,
		{TierDirect, FrontierStuck}: 1, {TierDirect, FrontierRetrying}: 1,
		{TierTransitive, FrontierWaiting}: 1, {TierTransitive, StatusUnavailable}: 1,
	} {
		if n, ok := got[k]; !ok || n != want {
			t.Errorf("%v = %d (present %v), want %d", k, n, ok, want)
		}
	}

	rows, err := s.UnavailableRows()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Tier != TierTransitive || rows[0].SettledAt != "2026-01-02T00:00:00Z" {
		t.Errorf("unavailable rows = %+v, want b/gone, transitive, settled 2026-01-02", rows)
	}
}
