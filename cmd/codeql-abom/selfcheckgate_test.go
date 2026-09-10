package main

import (
	"strings"
	"testing"
)

// The gate's whole value is that it fails. A threshold nobody can reach means the check is
// decoration; a threshold that is raised whenever the build goes red means the same.

func TestSelfcheckFailsOnAnyFindingByDefault(t *testing.T) {
	err := selfcheckVerdict(selfcheckSummary{
		Results: 1,
		ByRule:  map[string]int{"go/unhandled-writable-file-close": 1},
	}, 0)
	if err == nil {
		t.Fatal("one finding was accepted at a threshold of zero")
	}
	if !strings.Contains(err.Error(), "go/unhandled-writable-file-close") {
		t.Errorf("error = %q, want it to name the rule that failed", err)
	}
}

func TestSelfcheckPassesWhenNothingFired(t *testing.T) {
	if err := selfcheckVerdict(selfcheckSummary{Results: 0}, 0); err != nil {
		t.Fatalf("a clean run was reported as a failure: %v", err)
	}
}

// A threshold above zero exists so a corpus mid-cleanup can still gate on getting no worse. It
// has to hold exactly at the boundary, or it silently allows one more than it claims.
func TestSelfcheckToleratesExactlyTheThreshold(t *testing.T) {
	if err := selfcheckVerdict(selfcheckSummary{Results: 3}, 3); err != nil {
		t.Fatalf("three findings at a threshold of three failed: %v", err)
	}
	if err := selfcheckVerdict(selfcheckSummary{Results: 4}, 3); err == nil {
		t.Fatal("four findings at a threshold of three were accepted")
	}
}

// Report-only has to remain reachable: the first run against a new language is a measurement, and
// a tool that can only fail cannot take one.
func TestSelfcheckReportsWithoutFailingWhenAsked(t *testing.T) {
	err := selfcheckVerdict(selfcheckSummary{
		Results: 99,
		ByRule:  map[string]int{"js/unused-local-variable": 99},
	}, reportOnly)
	if err != nil {
		t.Fatalf("report-only mode failed the run: %v", err)
	}
}

func TestSelfcheckVerdictNamesEveryRuleThatFired(t *testing.T) {
	err := selfcheckVerdict(selfcheckSummary{
		Results: 5,
		ByRule:  map[string]int{"a/one": 2, "b/two": 3},
	}, 0)
	if err == nil {
		t.Fatal("findings were accepted at a threshold of zero")
	}
	for _, want := range []string{"a/one", "b/two", "5"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestSelfcheckParsesTheThreshold(t *testing.T) {
	cfg, err := parseSelfcheck([]string{"--max-findings=7"}, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if cfg.maxFindings != 7 {
		t.Errorf("maxFindings = %d, want 7", cfg.maxFindings)
	}
}

// Zero is the default because it is the only threshold that cannot be argued down.
func TestSelfcheckDefaultsToGatingAtZero(t *testing.T) {
	cfg, err := parseSelfcheck(nil, &strings.Builder{})
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if cfg.maxFindings != 0 {
		t.Errorf("maxFindings = %d, want 0", cfg.maxFindings)
	}
}
