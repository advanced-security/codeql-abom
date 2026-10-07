package store

import (
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	pollSHA   = "1111111111111111111111111111111111111111"
	pollOther = "2222222222222222222222222222222222222222"
)

type pollRow struct {
	key, ref, status, resolved, analyzed, checked string
}

// pollFixture holds the rows polling chooses from; analyzed long ago unless a row says otherwise.
func pollFixture(t *testing.T, rows ...pollRow) *Store {
	t.Helper()
	s := newTestStore(t)
	for _, r := range rows {
		owner, rest, _ := strings.Cut(r.key, "/")
		repo, _, _ := strings.Cut(rest, "/")
		if _, err := s.DB().Exec(`
			INSERT INTO abom_frontier (node_key, ref, owner, repo, depth, status, enqueued_at,
			                           resolved_sha, analyzed_ref, analyzed_at, checked_at)
			VALUES (?, ?, ?, ?, 1, ?, '2026-01-01T00:00:00Z', NULLIF(?, ''), NULLIF(?, ''),
			        '2026-01-01T00:00:00Z', NULLIF(?, ''))`,
			r.key, r.ref, owner, repo, r.status, r.resolved, r.analyzed, r.checked); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// reached records that each named repository runs node at commit, and that the node's reach was
// measured at all.
func reached(t *testing.T, s *Store, node, commit string, repos map[string]string) {
	t.Helper()
	if _, err := s.DB().Exec(`INSERT OR IGNORE INTO abom_node (node_key, node_kind) VALUES (?, 'action')`, node); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT OR IGNORE INTO abom_blast_radius (node_row_id, dependent_repos, dependent_workflows, max_depth)
		SELECT row_id, 0, 0, 0 FROM abom_node WHERE node_key = ?`, node); err != nil {
		t.Fatal(err)
	}
	for repo, origin := range repos {
		if _, err := s.DB().Exec(`INSERT OR IGNORE INTO repository (repository_full_name, origin) VALUES (?, ?)`,
			repo, origin); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB().Exec(`
			INSERT INTO abom_node_version_reach (node_row_id, commit_sha, repository_row_id, hops)
			SELECT n.row_id, ?, r.row_id, 1 FROM abom_node n, repository r
			 WHERE n.node_key = ? AND r.repository_full_name = ?`, commit, node, repo); err != nil {
			t.Fatal(err)
		}
	}
}

func pollKeys(c []PollCandidate) []string {
	out := make([]string, 0, len(c))
	for _, p := range c {
		out = append(out, p.NodeKey+"@"+p.Ref)
	}
	return out
}

var pollSince = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// A commit pin names a commit, which cannot move, so asking about one buys nothing.
func TestPollingNeverAsksAboutACommitPin(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "acme/tagged", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1"},
		pollRow{key: "acme/pinned", ref: pollSHA, status: "done", resolved: pollSHA, analyzed: pollSHA},
		// An annotated tag's own object id, pinned: it resolves to a different commit and still cannot move.
		pollRow{key: "acme/tag-object", ref: pollOther, status: "done", resolved: pollSHA, analyzed: pollOther},
		pollRow{key: "acme/same", ref: "v2", status: "done", resolved: pollSHA, analyzed: pollSHA},
		pollRow{key: "acme/waiting", ref: "v1", status: "pending"},
		pollRow{key: "acme/gone", ref: "v1", status: "unavailable", resolved: pollSHA, analyzed: "v1"},
		pollRow{key: "acme/unresolved", ref: "v1", status: "done", analyzed: "v1"},
	)

	due, err := s.PollDue(pollSince, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := pollKeys(due); !slices.Equal(got, []string{"acme/tagged@v1"}) {
		t.Errorf("only an analyzed reference that can move is asked about; got %v", got)
	}
}

// A repository the operator owns is read on its own events, with the sweep behind them, so the
// polling budget is spent only on what can tell ABOM nothing.
func TestPollingAsksOnlyAboutExternalRepositories(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "acme/service", ref: "", status: "done", resolved: pollSHA, analyzed: "HEAD"},
		pollRow{key: "vendor/action", ref: "", status: "done", resolved: pollSHA, analyzed: "HEAD"},
	)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	due, err := s.PollDue(pollSince, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := pollKeys(due); !slices.Equal(got, []string{"vendor/action@"}) {
		t.Errorf("an owned repository is not polled; got %v", got)
	}
}

// What moves first is what the most starting points run, at the version the reference resolves to
// now; then a branch, which moves on every push, before a tag, which moves on a release.
func TestThePollOrderFollowsConstrainedReachThenMutability(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "o/unmeasured", ref: "main", status: "done", resolved: pollSHA, analyzed: "main"},
		pollRow{key: "o/zero", ref: "main", status: "done", resolved: pollSHA, analyzed: "main"},
		pollRow{key: "o/one-tag", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1"},
		pollRow{key: "o/one-branch", ref: "develop", status: "done", resolved: pollSHA, analyzed: "develop"},
		pollRow{key: "o/one-default", ref: "", status: "done", resolved: pollSHA, analyzed: "HEAD"},
		pollRow{key: "o/three", ref: "v3.1.0", status: "done", resolved: pollSHA, analyzed: "v3.1.0"},
		pollRow{key: "o/upstream-heavy", ref: "main", status: "done", resolved: pollSHA, analyzed: "main"},
		pollRow{key: "o/old-version", ref: "release", status: "done", resolved: pollSHA, analyzed: "release"},
	)
	reached(t, s, "o/three", pollSHA, map[string]string{"me/a": "seed", "me/b": "seed", "me/c": "seed"})
	reached(t, s, "o/one-tag", pollSHA, map[string]string{"me/a": "seed"})
	reached(t, s, "o/one-branch", pollSHA, map[string]string{"me/b": "seed"})
	reached(t, s, "o/one-default", pollSHA, map[string]string{"me/c": "seed"})
	// Measured, and no starting point runs it: reach zero, which is not the same as unmeasured.
	reached(t, s, "o/zero", pollSHA, nil)
	// Upstream repositories are not dependents, however many of them run it.
	reached(t, s, "o/upstream-heavy", pollSHA, map[string]string{"up/a": "upstream", "up/b": "upstream"})
	// Reach at a commit the reference no longer resolves to is not the reach of the version that would move.
	reached(t, s, "o/old-version", pollOther, map[string]string{"me/a": "seed", "me/b": "seed"})

	due, err := s.PollDue(pollSince, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"o/three@v3.1.0",
		"o/one-branch@develop", "o/one-default@",
		"o/one-tag@v1",
		"o/unmeasured@main",
		"o/old-version@release", "o/upstream-heavy@main", "o/zero@main",
	}
	if got := pollKeys(due); !slices.Equal(got, want) {
		t.Errorf("the poll order:\n got %v\nwant %v", got, want)
	}
	for _, c := range due {
		switch c.NodeKey {
		case "o/unmeasured":
			if c.Reach != nil {
				t.Errorf("a node whose reach was never measured has no reach, not %d", *c.Reach)
			}
		case "o/zero", "o/upstream-heavy", "o/old-version":
			if c.Reach == nil || *c.Reach != 0 {
				t.Errorf("%s is measured and no starting point runs its version; got %v", c.NodeKey, c.Reach)
			}
		case "o/three":
			if c.Reach == nil || *c.Reach != 3 {
				t.Errorf("three starting points run o/three at its version; got %v", c.Reach)
			}
		}
	}
}

// Asked about or analyzed since the floor, a reference is as fresh as polling would make it.
func TestPollingLeavesAReferenceProbedOrAnalyzedSinceTheFloorAlone(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "o/stale", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1", checked: "2026-02-01T00:00:00Z"},
		pollRow{key: "o/probed", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1", checked: "2026-07-01T00:00:00Z"},
		pollRow{key: "o/analyzed", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1"},
	)
	if _, err := s.DB().Exec(`UPDATE abom_frontier SET analyzed_at = '2026-07-01T00:00:00Z' WHERE node_key = 'o/analyzed'`); err != nil {
		t.Fatal(err)
	}

	due, err := s.PollDue(pollSince, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := pollKeys(due); !slices.Equal(got, []string{"o/stale@v1"}) {
		t.Fatalf("only a reference nothing has read since the floor is due; got %v", got)
	}
	if due[0].ResolvedSHA != pollSHA || due[0].Owner != "o" || due[0].Repo != "stale" {
		t.Errorf("a candidate carries what to ask and what to compare with; got %+v", due[0])
	}
}

// The estimate counts what polling cycles through: each repository and reference once, at the
// most reach any of its rows has.
func TestThePollWorkCountsEachReferenceOnceByItsReach(t *testing.T) {
	s := pollFixture(t,
		pollRow{key: "g/codeql/init", ref: "v3", status: "done", resolved: pollSHA, analyzed: "v3"},
		pollRow{key: "g/codeql/analyze", ref: "v3", status: "done", resolved: pollSHA, analyzed: "v3"},
		pollRow{key: "o/one", ref: "v1", status: "done", resolved: pollSHA, analyzed: "v1", checked: "2026-07-01T00:00:00Z"},
		pollRow{key: "o/none", ref: "main", status: "done", resolved: pollSHA, analyzed: "main"},
		pollRow{key: "o/pinned", ref: pollSHA, status: "done", resolved: pollSHA, analyzed: pollSHA},
	)
	reached(t, s, "g/codeql/init", pollSHA, map[string]string{"me/a": "seed"})
	reached(t, s, "g/codeql/analyze", pollSHA, map[string]string{"me/a": "seed", "me/b": "seed"})
	reached(t, s, "o/one", pollSHA, map[string]string{"me/c": "seed"})

	w, err := s.ReadPollWork(QueueBound{})
	if err != nil {
		t.Fatal(err)
	}
	if w.References != 3 {
		t.Errorf("three references can move, one of them named by two rows; got %d", w.References)
	}
	want := []PollReach{{Reach: 2, References: 1}, {Reach: 1, References: 1}}
	if !slices.Equal(w.ByReach, want) {
		t.Errorf("by reach, most first: want %+v, got %+v", want, w.ByReach)
	}
}
