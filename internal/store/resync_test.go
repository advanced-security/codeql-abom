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
