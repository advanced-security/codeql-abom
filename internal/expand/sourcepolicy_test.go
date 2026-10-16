package expand

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
