package expand

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// probeFunc records what was asked and answers from a script.
type probeFunc struct {
	answers map[string]string
	errs    map[string]error
	asked   []string
}

// probeKey is how the script addresses one reference of one repository, because resolving a tag
// and resolving the default branch are different questions with different answers.
func probeKey(owner, repo, ref string) string {
	if ref == "" {
		ref = frontier.HeadRef
	}
	return owner + "/" + repo + "@" + ref
}

func (p *probeFunc) Resolve(_ context.Context, owner, repo, ref string) (string, error) {
	key := probeKey(owner, repo, ref)
	p.asked = append(p.asked, key)
	if err := p.errs[key]; err != nil {
		return "", err
	}
	return p.answers[key], nil
}

// analyzedAtRef puts a completed analysis of one reference in the queue, as an expansion would.
func analyzedAtRef(t *testing.T, q *frontier.Queue, owner, repo, ref, sha string) {
	t.Helper()
	key := owner + "/" + repo
	if _, err := q.Enqueue([]frontier.Item{
		{NodeKey: key, Owner: owner, Repo: repo, Ref: ref, Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(frontier.Key{NodeKey: key, Ref: ref},
		frontier.Resolution{Ref: ref, SHA: sha}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

// analyzed puts a completed analysis in the queue, as a finished expansion would.
func analyzed(t *testing.T, q *frontier.Queue, owner, repo, ref, sha string) {
	t.Helper()
	key := owner + "/" + repo
	if _, err := q.Enqueue([]frontier.Item{
		{NodeKey: key, Owner: owner, Repo: repo, Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(frontier.Of(key), frontier.Resolution{Ref: ref, SHA: sha}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func newRechecker(t *testing.T) (*Rechecker, *frontier.Queue, *probeFunc, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{BulkLoad: true})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	q := &frontier.Queue{Store: s, MaxDepth: 5}
	p := &probeFunc{answers: map[string]string{}, errs: map[string]error{}}
	return &Rechecker{Queue: q, Probe: p}, q, p, s
}

// stale advances the queue's clock so analyzes fall outside the freshness window.
func stale(q *frontier.Queue, by time.Duration) {
	q.Now = func() time.Time { return time.Now().UTC().Add(by) }
}

func frontierStatus(t *testing.T, s *store.Store, key string) string {
	t.Helper()
	var status string
	if err := s.DB().QueryRow(
		`SELECT status FROM abom_frontier WHERE node_key=?`, key).Scan(&status); err != nil {
		t.Fatalf("reading %s: %v", key, err)
	}
	return status
}

// A moved tag means the stored graph describes code that is no longer what consumers get.
func TestRecheckRequeuesDriftedRefs(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analyzed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")
	p.answers["actions/checkout@HEAD"] = "new-sha"
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Drifted != 1 || stats.Checked != 1 {
		t.Errorf("stats = %+v, want one checked and one drifted", stats)
	}
	if got := frontierStatus(t, s, "actions/checkout"); got != frontier.StatusPending {
		t.Errorf("status = %q, want pending for re-analysis", got)
	}
}

// Most probes find nothing changed, and that outcome must be cheap: no re-analysis is queued.
func TestRecheckLeavesUnchangedRefsAnalyzed(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analyzed(t, q, "actions", "checkout", frontier.HeadRef, "same-sha")
	p.answers["actions/checkout@HEAD"] = "same-sha"
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Unchanged != 1 || stats.Drifted != 0 {
		t.Errorf("stats = %+v, want one unchanged", stats)
	}
	if got := frontierStatus(t, s, "actions/checkout"); got != frontier.StatusDone {
		t.Errorf("status = %q, want it to stay done", got)
	}
}

// A probe that cannot be answered says nothing about the analysis, so discarding or requeueing
// it would either lose good data or spend a full re-analysis on a network blip.
func TestRecheckFailureKeepsTheExistingAnalysis(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analyzed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")
	p.errs["actions/checkout@HEAD"] = fmt.Errorf("connection reset")
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Failed != 1 {
		t.Errorf("stats = %+v, want one failure", stats)
	}
	if got := frontierStatus(t, s, "actions/checkout"); got != frontier.StatusDone {
		t.Errorf("status = %q, want the analysis retained", got)
	}
	// Not having been touched, it stays due, so the next pass tries again.
	due, err := q.DueForRecheck(24*time.Hour, 10)
	if err != nil {
		t.Fatalf("DueForRecheck: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("%d items due after a failed probe, want it still due", len(due))
	}
}

// A repository that has been deleted or made private will not come back by retrying.
func TestRecheckMarksVanishedRepositoriesUnavailable(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analyzed(t, q, "octo", "gone", frontier.HeadRef, "old-sha")
	p.errs["octo/gone@HEAD"] = fmt.Errorf("%w: repository not found", ErrUnavailable)
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Unavailable != 1 {
		t.Errorf("stats = %+v, want one unavailable", stats)
	}
	if got := frontierStatus(t, s, "octo/gone"); got != frontier.StatusUnavailable {
		t.Errorf("status = %q, want unavailable", got)
	}
}

// A SHA-pinned analysis cannot drift, so spending a probe on it is pure waste.
func TestRecheckSkipsImmutableRefs(t *testing.T) {
	r, q, p, _ := newRechecker(t)
	analyzed(t, q, "actions", "checkout", "abc123", "abc123")
	stale(q, 365*24*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Checked != 0 {
		t.Errorf("Checked = %d, want none", stats.Checked)
	}
	if len(p.asked) != 0 {
		t.Errorf("probed %v, want nothing", p.asked)
	}
}

// Rechecking competes with expansion for the same request budget, so a pass has to stay bounded.
func TestRecheckRespectsTheLimit(t *testing.T) {
	r, q, p, _ := newRechecker(t)
	for _, name := range []string{"one", "two", "three", "four"} {
		analyzed(t, q, "octo", name, frontier.HeadRef, "old-sha")
		p.answers["octo/"+name+"@HEAD"] = "old-sha"
	}
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Checked != 2 {
		t.Errorf("Checked = %d, want 2", stats.Checked)
	}
}

// A row requesting `v4` is asking about `v4`, and the drift question is whether *that* tag has
// moved. Probing the default branch instead compares two unrelated commits, so a tag that has
// not moved reads as drifted — and re-analyzing it produces the identical result, which makes it
// due again next pass. On the real corpus that is 800 of the 1,997 mutable rows on a permanent
// 24-hour treadmill, indistinguishable from real drift.
func TestARefIsProbedAgainstItselfRatherThanTheDefaultBranch(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analyzedAtRef(t, q, "actions", "checkout", "v4", "v4-sha")
	p.answers["actions/checkout@v4"] = "v4-sha"
	p.answers["actions/checkout@HEAD"] = "main-sha"
	stale(q, 48*time.Hour)

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stats.Unchanged != 1 || stats.Drifted != 0 {
		t.Errorf("stats = %+v, want the unmoved tag reported unchanged", stats)
	}
	if got := frontierStatus(t, s, "actions/checkout"); got != frontier.StatusDone {
		t.Errorf("status = %q, want it left analyzed", got)
	}
	if len(p.asked) != 1 || p.asked[0] != "actions/checkout@v4" {
		t.Errorf("probed %v, want only the reference the row is for", p.asked)
	}
}

// Freshly analyzed work must not be re-probed on the next pass.
func TestRecheckIgnoresFreshAnalyzes(t *testing.T) {
	r, q, p, _ := newRechecker(t)
	analyzed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")

	stats, err := r.Run(context.Background(), 24*time.Hour, 10)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Checked != 0 || len(p.asked) != 0 {
		t.Errorf("probed fresh work: %+v %v", stats, p.asked)
	}
}

// Resolve is exercised against real git so the parsing matches what the command actually emits.
func TestResolveReadsHeadFromARealRepository(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: test\n"})
	c := newCheckout(t, origin)

	got, err := c.Resolve(context.Background(), "", filepath.Base(origin), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = origin
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	if want := string(out[:40]); got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

// A tag resolves to the commit the tag names, not to whatever the default branch has moved on
// to. Only real git can say whether the ls-remote output for a tag parses the same way.
func TestResolveReadsATagRatherThanTheDefaultBranch(t *testing.T) {
	origin := originRepo(t, map[string]string{"action.yml": "name: test\n"})
	tagged := commitOf(t, origin, "HEAD")
	tag(t, origin, "v4")
	// Move the default branch on, so a probe that ignored the tag would answer differently.
	addCommit(t, origin, "action.yml", "name: moved\n")

	c := newCheckout(t, origin)
	got, err := c.Resolve(context.Background(), "", filepath.Base(origin), "v4")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if got != tagged {
		t.Errorf("Resolve(v4) = %q, want the tagged commit %q", got, tagged)
	}
	if head := commitOf(t, origin, "HEAD"); got == head {
		t.Errorf("Resolve(v4) returned the default branch commit %q", head)
	}
}

func TestResolveReportsMissingRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	c := &Checkout{BaseURL: t.TempDir()}

	if _, err := c.Resolve(context.Background(), "", "does-not-exist", ""); !IsUnavailable(err) {
		t.Errorf("err = %v, want an unavailable error", err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitOf(t *testing.T, dir, rev string) string {
	t.Helper()
	return gitIn(t, dir, "rev-parse", rev)
}

func tag(t *testing.T, dir, name string) {
	t.Helper()
	gitIn(t, dir, "tag", name)
}

func addCommit(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	gitIn(t, dir, "add", name)
	gitIn(t, dir, "commit", "-m", "move")
}
