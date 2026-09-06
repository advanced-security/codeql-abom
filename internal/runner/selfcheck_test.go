package runner

import (
	"slices"
	"strings"
	"testing"
)

// The self-check pack gates this repository against its own workflows. Offering it as a binding
// advertises a control that answers no question the corpus asks, and `make selfcheck` names the
// suite directly, so nothing that needs it comes through here.
func TestTheSelfCheckPackIsNotOfferedAsABinding(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/selfcheck/src", "advanced-security/abom-actions-selfcheck-src",
		"codeql-suites", "abom-selfcheck.qls")
	pack(t, root, "actions/findings/src", "advanced-security/abom-actions-findings-src",
		"codeql-suites", "abom-findings.qls")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}

	for _, suite := range got {
		if strings.Contains(suite, "selfcheck") {
			t.Errorf("the self-check suite is offered: %v", got)
		}
	}
	if !slices.Contains(got,
		"advanced-security/abom-actions-findings-src:codeql-suites/abom-findings.qls") {
		t.Errorf("got %v, want the findings suite still offered", got)
	}
}

// The exclusion is one pack that exists only for CI, not a list of suite names. A suite dropped
// into a pack that is for analysis still appears with no release, which is what the control is
// for and what an exception list would have cost.
func TestANewSuiteInAnAnalysisPackIsStillOffered(t *testing.T) {
	root := t.TempDir()
	pack(t, root, "actions/graph-extraction/src",
		"advanced-security/abom-actions-graph-extraction-src",
		"codeql-suites", "abom-dependency-graph.qls", "invented-today.qls")

	got, err := AvailableSuites(root)
	if err != nil {
		t.Fatalf("AvailableSuites: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %v, want both suites of an analysis pack", got)
	}
}
