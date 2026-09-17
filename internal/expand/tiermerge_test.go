package expand

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A tier's plan is assembled by merging the suites bound to it, and a merge that copies only the
// obvious slices loses everything else. That happened: the CodeQL version was read once per round
// and then dropped on the way into the tier, so every run recorded a NULL cli_version while the
// pack and digest beside it looked right. Unit tests on plan() all passed, because plan() was
// never the broken half.
func TestMergingSuitesIntoATierKeepsTheRunIdentity(t *testing.T) {
	h, _ := boundHarness(t)

	plans, err := h.expander.plansByTier(context.Background(), Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	for _, tier := range []string{store.TierOwned, store.TierDirect, store.TierTransitive} {
		if v := plans[tier].cliVersion; v != "2.26.4" {
			t.Errorf("%s tier carries CodeQL version %q, want 2.26.4", tier, v)
		}
		if len(plans[tier].contents) == 0 {
			t.Errorf("%s tier carries no resolved contents", tier)
		}
	}
}

// The identity has to survive the merge all the way to the row that gets written, since that is
// the only place it is ever read back from.
func TestARunRecordedForATierNamesTheCodeQLThatEvaluatedIt(t *testing.T) {
	h, _ := boundHarness(t)

	plans, err := h.expander.plansByTier(context.Background(), Options{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("plansByTier: %v", err)
	}

	run := plans[store.TierDirect].identify("abom/Edges.ql", store.QueryRun{Repository: "octo/app"})
	if run.CLIVersion != "2.26.4" {
		t.Errorf("recorded CLI version %q, want 2.26.4", run.CLIVersion)
	}
}
