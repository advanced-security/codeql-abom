package expand

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
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

func (p *probeFunc) Resolve(_ context.Context, owner, repo string) (string, error) {
	full := owner + "/" + repo
	p.asked = append(p.asked, full)
	if err := p.errs[full]; err != nil {
		return "", err
	}
	return p.answers[full], nil
}

// analysed puts a completed analysis in the queue, as a finished expansion would.
func analysed(t *testing.T, q *frontier.Queue, owner, repo, ref, sha string) {
	t.Helper()
	key := owner + "/" + repo
	if _, err := q.Enqueue([]frontier.Item{
		{NodeKey: key, Owner: owner, Repo: repo, Depth: 1},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Complete(key, frontier.Resolution{Ref: ref, SHA: sha}); err != nil {
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

// stale advances the queue's clock so analyses fall outside the freshness window.
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
	analysed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")
	p.answers["actions/checkout"] = "new-sha"
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
func TestRecheckLeavesUnchangedRefsAnalysed(t *testing.T) {
	r, q, p, s := newRechecker(t)
	analysed(t, q, "actions", "checkout", frontier.HeadRef, "same-sha")
	p.answers["actions/checkout"] = "same-sha"
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
	analysed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")
	p.errs["actions/checkout"] = fmt.Errorf("connection reset")
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
	analysed(t, q, "octo", "gone", frontier.HeadRef, "old-sha")
	p.errs["octo/gone"] = fmt.Errorf("%w: repository not found", ErrUnavailable)
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
	analysed(t, q, "actions", "checkout", "abc123", "abc123")
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
		analysed(t, q, "octo", name, frontier.HeadRef, "old-sha")
		p.answers["octo/"+name] = "old-sha"
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

// Freshly analysed work must not be re-probed on the next pass.
func TestRecheckIgnoresFreshAnalyses(t *testing.T) {
	r, q, p, _ := newRechecker(t)
	analysed(t, q, "actions", "checkout", frontier.HeadRef, "old-sha")

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

	got, err := c.Resolve(context.Background(), "", filepath.Base(origin))
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

func TestResolveReportsMissingRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	c := &Checkout{BaseURL: t.TempDir()}

	if _, err := c.Resolve(context.Background(), "", "does-not-exist"); !IsUnavailable(err) {
		t.Errorf("err = %v, want an unavailable error", err)
	}
}
