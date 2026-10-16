package expand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/sourcepolicy"
)

func TestLocalCheckoutReuseDoesNotRequireRemoteEligibility(t *testing.T) {
	cache := &CheckoutCache{Dir: t.TempDir()}
	stage, err := cache.Stage()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(stagedTree(stage), ".github", "workflows")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ci.yml"), []byte("on: push\njobs: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if _, err := cache.Retain(stage, "acme", "private", sha, sparsePatterns); err != nil {
		t.Fatal(err)
	}
	checked := 0
	e := &Expander{Cache: cache, Fetcher: &Checkout{SourcePolicy: func(context.Context, string) error {
		checked++
		return &sourcepolicy.Denied{Repository: "acme/private", Reason: "disabled"}
	}}}
	b := batch{owner: "acme", repo: "private", ref: sha}
	_, retained, err := e.materialize(context.Background(), b, filepath.Join(t.TempDir(), "src"), sha, true)
	if err != nil || !retained || checked != 0 {
		t.Fatalf("local reuse: retained=%v checked=%d err=%v", retained, checked, err)
	}
	b.ref = strings.Repeat("b", 40)
	_, _, err = e.materialize(context.Background(), b, filepath.Join(t.TempDir(), "src"), b.ref, true)
	if !IsUnavailable(err) || checked != 1 {
		t.Fatalf("cache miss: checked=%d err=%v", checked, err)
	}
}

type countingSourceReader struct{ reads int }

func (r *countingSourceReader) SourceAccess(_ context.Context, repository string) (catalog.SourceAccess, error) {
	r.reads++
	return catalog.SourceAccess{Repository: repository, Visibility: "public"}, nil
}
func (r *countingSourceReader) DefaultSetup(context.Context, string) (catalog.DefaultSetup, error) {
	return catalog.DefaultSetup{}, nil
}
func (r *countingSourceReader) CheckScanningAccess(context.Context, string) error { return nil }

// An operator who fixed eligibility and asks to run a repository now must get a fresh check, even
// when the name they typed carries surrounding whitespace; otherwise the memo refuses for minutes.
func TestRequeueingAPaddedRepositoryNameRefreshesItsSourceEligibility(t *testing.T) {
	h := newHarness(t)
	settledRow(t, h, "octo", "mine")
	read := &countingSourceReader{}
	h.expander.SourcePolicy = &sourcepolicy.Policy{Read: read, Owned: func(string) (bool, error) { return false, nil }}
	if err := h.expander.SourcePolicy.Check(context.Background(), "octo/mine"); err != nil {
		t.Fatal(err)
	}

	got, err := h.expander.RequeueRepository("  octo/mine  ")
	if err != nil || got.Requeued != 1 {
		t.Fatalf("RequeueRepository: requeued=%d err=%v", got.Requeued, err)
	}
	if err := h.expander.SourcePolicy.Check(context.Background(), "octo/mine"); err != nil {
		t.Fatal(err)
	}
	if read.reads != 2 {
		t.Errorf("eligibility read %d times, want 2: the requeue did not invalidate the memo", read.reads)
	}
}

// The checkout timeout is the budget for acquiring a source; an eligibility check that stalls on
// the network would otherwise hold a fetching worker with no bound at all.
func TestTheCheckoutTimeoutBoundsTheSourceEligibilityCheck(t *testing.T) {
	refused := errors.New("refused after observing the deadline")
	var bounded bool
	c := &Checkout{Timeout: time.Hour, SourcePolicy: func(ctx context.Context, _ string) error {
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		bounded = ok && remaining > 0 && remaining <= time.Hour
		return refused
	}}
	if _, err := c.Fetch(context.Background(), t.TempDir(), "acme", "private", ""); !errors.Is(err, refused) {
		t.Fatalf("Fetch: %v, want the policy refusal", err)
	}
	if !bounded {
		t.Error("the source policy did not run under the checkout deadline")
	}
}

func TestSourcePolicyRefusalsStayVisibleAtEveryDependencyDepth(t *testing.T) {
	for _, depth := range []int{0, 1, 3} {
		h := newHarness(t)
		h.enqueue(item("gradle", "actions", "setup-gradle", depth))
		h.fetchErr["gradle/actions"] = &sourcepolicy.Denied{Repository: "gradle/actions", Reason: "private and not owned"}
		stats := h.run(100)
		if stats.Unavailable != 1 || stats.Processed != 0 || len(h.built) != 0 {
			t.Fatalf("depth %d: stats=%+v built=%v", depth, stats, h.built)
		}
	}
}
