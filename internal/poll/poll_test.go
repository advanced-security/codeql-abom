package poll

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

const (
	shaA = "a000000000000000000000000000000000000001"
	shaB = "b000000000000000000000000000000000000002"
)

var (
	analyzedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	polledAt   = time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
)

// fakeResolver answers from a map and records every query it was sent.
type fakeResolver struct {
	answers map[string]string
	unread  map[string]string
	err     error
	cost    int
	queries [][]string
	// during runs while a query is out, as the crawl would.
	during func()
}

func (f *fakeResolver) ResolveRefs(_ context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error) {
	asked := make([]string, 0, len(qs))
	out := catalog.RefBatch{Requests: 1, Cost: f.cost, Priced: f.cost > 0}
	for _, q := range qs {
		key := q.Owner + "/" + q.Repo + "@" + q.Ref
		asked = append(asked, key)
		a := catalog.RefAnswer{Commit: f.answers[key], Unread: f.unread[key]}
		if a.Commit == "" && a.Unread == "" {
			a.Unread = "no such reference"
		}
		out.Answers = append(out.Answers, a)
	}
	f.queries = append(f.queries, asked)
	if f.during != nil {
		f.during()
	}
	if f.err != nil {
		return catalog.RefBatch{Requests: 1, Cost: f.cost, Priced: f.cost > 0}, f.err
	}
	return out, nil
}

type fixture struct {
	t        *testing.T
	store    *store.Store
	queue    *frontier.Queue
	resolver *fakeResolver
	poller   *Poller
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	q := &frontier.Queue{Store: s, Now: func() time.Time { return analyzedAt }}
	r := &fakeResolver{answers: map[string]string{}, unread: map[string]string{}}
	return &fixture{t: t, store: s, queue: q, resolver: r,
		poller: &Poller{Store: s, Queue: q, Resolve: r, Now: func() time.Time { return polledAt }}}
}

func (f *fixture) exec(query string, args ...any) {
	f.t.Helper()
	if _, err := f.store.DB().Exec(query, args...); err != nil {
		f.t.Fatal(err)
	}
}

// analyzed queues and completes key at ref, at sha, before the poll's floor; key may carry a subpath.
func (f *fixture) analyzed(key, ref, sha string) {
	f.t.Helper()
	owner, rest, _ := strings.Cut(key, "/")
	repo, _, _ := strings.Cut(rest, "/")
	if _, err := f.queue.Enqueue([]frontier.Item{{NodeKey: key, Ref: ref, Owner: owner, Repo: repo, Depth: 1}}); err != nil {
		f.t.Fatalf("Enqueue: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: key, Ref: ref}, frontier.Resolution{Ref: ref, SHA: sha}); err != nil {
		f.t.Fatalf("Complete: %v", err)
	}
}

// reach records that n seed repositories run key at sha.
func (f *fixture) reach(key, sha string, n int) {
	f.t.Helper()
	f.exec(`INSERT INTO abom_node (node_key, node_kind) VALUES (?, 'action')`, key)
	f.exec(`INSERT INTO abom_blast_radius (node_row_id, dependent_repos, dependent_workflows, max_depth)
		SELECT row_id, ?, 0, 1 FROM abom_node WHERE node_key = ?`, n, key)
	for i := range n {
		name := "me/" + strings.ReplaceAll(key, "/", "-") + "-" + string(rune('a'+i))
		f.exec(`INSERT OR IGNORE INTO repository (repository_full_name, origin) VALUES (?, 'seed')`, name)
		f.exec(`INSERT INTO abom_node_version_reach (node_row_id, commit_sha, repository_row_id, hops)
			SELECT n.row_id, ?, r.row_id, 1 FROM abom_node n, repository r
			 WHERE n.node_key = ? AND r.repository_full_name = ?`, sha, key, name)
	}
}

func (f *fixture) row(key, ref string) store.QueueRow {
	f.t.Helper()
	r, found, err := f.store.QueueRow(key, ref)
	if err != nil || !found {
		f.t.Fatalf("QueueRow(%s@%s) = %v, %v", key, ref, found, err)
	}
	return r
}

// everything is a row's every column, so a test can say nothing else moved.
func (f *fixture) everything(key, ref string) map[string]any {
	f.t.Helper()
	rows, err := f.store.DB().Query(`SELECT * FROM abom_frontier WHERE node_key = ? AND ref = ?`, key, ref)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !rows.Next() {
		f.t.Fatalf("no row %s@%s: %v", key, ref, err)
	}
	values := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range values {
		ptrs[i] = &values[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		f.t.Fatal(err)
	}
	out := map[string]any{}
	for i, c := range cols {
		out[c] = values[i]
	}
	return out
}

func (f *fixture) pass() Pass {
	f.t.Helper()
	pass, err := f.poller.Pass(context.Background(), 0)
	if err != nil {
		f.t.Fatalf("Pass: %v", err)
	}
	return pass
}

// A commit cannot move, so a poll of one is budget spent learning nothing.
func TestCommitPinsAreNeverPolled(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/pinned", shaA, shaA)
	f.analyzed("vendor/tagged", "v1", shaA)

	f.pass()

	if got := slices.Concat(f.resolver.queries...); !slices.Equal(got, []string{"vendor/tagged@v1"}) {
		t.Errorf("only the tag is asked about; asked %v", got)
	}
}

// The budget goes first to what the most starting points run, then to what moves most often.
func TestThePollFollowsConstrainedReachThenMutability(t *testing.T) {
	f := newFixture(t)
	f.analyzed("o/lone-tag", "v1", shaA)
	f.analyzed("o/lone-branch", "main", shaA)
	f.analyzed("o/popular", "v2", shaA)
	f.analyzed("o/unmeasured", "v1", shaA)
	f.reach("o/lone-tag", shaA, 1)
	f.reach("o/lone-branch", shaA, 1)
	f.reach("o/popular", shaA, 4)
	f.poller.Batch = 1

	f.pass()

	want := [][]string{{"o/popular@v2"}, {"o/lone-branch@main"}, {"o/lone-tag@v1"}, {"o/unmeasured@v1"}}
	if !slices.EqualFunc(f.resolver.queries, want, slices.Equal) {
		t.Errorf("asked in order %v, want %v", f.resolver.queries, want)
	}
}

// Aliases resolve many references for one query's price, and one reference two subpaths share is
// asked once and settles both.
func TestOneQueryAsksAboutABatchAndSettlesEveryRowItAnswers(t *testing.T) {
	f := newFixture(t)
	f.analyzed("github/codeql-action/init", "v3", shaA)
	f.analyzed("github/codeql-action/analyze", "v3", shaA)
	f.analyzed("vendor/tool", "", shaA)
	f.resolver.answers["github/codeql-action@v3"] = shaA
	f.resolver.answers["vendor/tool@"] = shaA

	pass := f.pass()

	if len(f.resolver.queries) != 1 || len(f.resolver.queries[0]) != 2 {
		t.Fatalf("two references in one query; asked %v", f.resolver.queries)
	}
	if pass.Asked != 2 || pass.Queries != 1 || pass.Unchanged != 3 {
		t.Errorf("two references asked in one query settle three rows; got %+v", pass)
	}
	for _, key := range []string{"github/codeql-action/init", "github/codeql-action/analyze"} {
		if f.row(key, "v3").CheckedAt == "" {
			t.Errorf("%s shares the answer and records the probe", key)
		}
	}
}

// A moved reference goes back to the queue the way the sweep sends it, and the claim order decides
// what happens; an unmoved one records the probe and nothing else.
func TestAMovedReferenceIsRequeuedAndAnUnmovedOneOnlyRecordsTheProbe(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/moved", "v1", shaA)
	f.analyzed("vendor/still", "v1", shaA)
	f.resolver.answers["vendor/moved@v1"] = shaB
	f.resolver.answers["vendor/still@v1"] = shaA
	before := f.everything("vendor/still", "v1")
	f.queue.Now = func() time.Time { return polledAt }

	pass := f.pass()

	if pass.Moved != 1 || pass.Unchanged != 1 {
		t.Errorf("one moved and one did not; got %+v", pass)
	}
	moved := f.row("vendor/moved", "v1")
	if moved.Status != frontier.StatusPending || moved.Reason != "ref moved to "+shaB || moved.ResolvedSHA != shaA {
		t.Errorf("a moved reference is requeued, still naming the commit its graph came from; got %+v", moved)
	}
	after := f.everything("vendor/still", "v1")
	if after["checked_at"] == nil {
		t.Fatal("an unmoved reference records that it was probed")
	}
	for col, was := range before {
		if col != "checked_at" && after[col] != was {
			t.Errorf("an unmoved reference changed %s from %v to %v", col, was, after[col])
		}
	}
}

// An answer nobody could read is not an answer that nothing moved: it writes nothing, and one
// failure does not make a repository unavailable.
func TestAnUnreadAnswerLeavesTheReferenceUnknownWithNothingWritten(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/deleted", "v1", shaA)
	f.analyzed("vendor/missing-tag", "v9", shaA)
	f.resolver.unread["vendor/deleted@v1"] = "Could not resolve to a Repository"
	refs := map[string]string{"vendor/deleted": "v1", "vendor/missing-tag": "v9"}
	before := map[string]map[string]any{}
	for key, ref := range refs {
		before[key] = f.everything(key, ref)
	}

	pass := f.pass()
	f.resolver.err = errors.New("502 Bad Gateway")
	f.poller.unreadUntil = nil
	refused, err := f.poller.Pass(context.Background(), 0)
	if err != nil {
		t.Fatalf("a refused query is a gap in what is known, not a failed pass: %v", err)
	}

	if pass.Unread != 2 || pass.Moved+pass.Unchanged != 0 {
		t.Errorf("both answers were unread; got %+v", pass)
	}
	if refused.Unread != 2 || refused.Moved+refused.Unchanged != 0 {
		t.Errorf("a refused query leaves both unread; got %+v", refused)
	}
	for key, ref := range refs {
		after := f.everything(key, ref)
		for col, was := range before[key] {
			if after[col] != was {
				t.Errorf("%s: an unread answer changed %s from %v to %v", key, col, was, after[col])
			}
		}
	}
}

// Asking again within the floor about a reference nobody could read buys nothing but cost.
func TestAnUnreadReferenceIsNotAskedAgainWithinTheFloor(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/deleted", "v1", shaA)

	f.pass()
	f.pass()

	if len(f.resolver.queries) != 1 {
		t.Errorf("asked %d times within the floor; want once", len(f.resolver.queries))
	}
}

// The crawl keeps claiming while a query is out; an answer about the commit a row no longer
// records says nothing about the row.
func TestARowClaimedWhileItsQueryWasOutIsLeftToTheCrawl(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/tool", "v1", shaA)
	f.resolver.answers["vendor/tool@v1"] = shaB
	f.resolver.during = func() {
		f.exec(`UPDATE abom_frontier SET status = 'running' WHERE node_key = 'vendor/tool'`)
	}

	f.pass()

	if got := f.row("vendor/tool", "v1"); got.Status != frontier.StatusRunning || got.CheckedAt != "" {
		t.Errorf("a row the crawl claimed meanwhile is the crawl's; got %+v", got)
	}
}

// Polling spends its own budget, in what GitHub charged, and nobody else's.
func TestThePollerSpendsWhatGitHubChargedFromItsPool(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/one", "v1", shaA)
	f.analyzed("vendor/two", "v1", shaA)
	f.resolver.cost = 1
	pools := work.NewTokenPools()
	f.poller.Pool = pools.Pool(work.PoolPolling, work.UnitGraphQL)
	f.poller.Batch = 1

	pass := f.pass()

	spent := map[string]work.Spend{}
	for _, s := range pools.Drain() {
		got := spent[s.Pool+"/"+s.Unit]
		got.Requests += s.Requests
		got.Charged += s.Charged
		spent[s.Pool+"/"+s.Unit] = got
	}
	if got := spent[work.PoolPolling+"/"+work.UnitGraphQL]; got.Requests != 2 || got.Charged != 2 {
		t.Errorf("two queries charged a point each to the polling pool; got %+v", got)
	}
	if len(spent) != 1 {
		t.Errorf("polling spends from its own pool only; got %+v", spent)
	}
	if pass.Queries != 2 || pass.Charged != 2 {
		t.Errorf("the pass records what each query cost; got %+v", pass)
	}
}

// An allowance reported spent holds polling until its reset, rather than spending what is not there.
func TestAPoolWithNoRoomHoldsThePoller(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/one", "v1", shaA)
	pools := work.NewTokenPools()
	f.poller.Pool = pools.Pool(work.PoolPolling, work.UnitGraphQL)
	f.poller.Pool.Observe(work.Reading{Known: true, Remaining: 0, Limit: 5000, ResetAt: time.Now().Add(time.Hour)})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := f.poller.Pass(ctx, 0)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the pass waits for the reset until it is told to stop; got %v", err)
	}
	if len(f.resolver.queries) != 0 {
		t.Errorf("nothing is asked while nothing is left; asked %v", f.resolver.queries)
	}
	if got := f.row("vendor/one", "v1"); got.CheckedAt != "" {
		t.Errorf("a reference nobody asked about was not probed; got %+v", got)
	}
}

// A reference nothing owned reaches is never claimed, so learning that it moved queues work the
// crawl will not do, and the poll that learned it is budget spent on somebody else's supply chain.
func TestOnlyReferencesInsideTheOwnedClosureArePolled(t *testing.T) {
	f := newFixture(t)
	f.analyzed("vendor/kept", "v1", shaA)
	f.analyzed("vendor/left", "v1", shaA)
	f.queue.Closure = func() ([]string, error) { return []string{"Vendor/Kept"}, nil }

	f.pass()

	if got := slices.Concat(f.resolver.queries...); !slices.Equal(got, []string{"vendor/kept@v1"}) {
		t.Errorf("asked %v, want only vendor/kept@v1", got)
	}
}
