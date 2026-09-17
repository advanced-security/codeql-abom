package main

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func pinningTotals() store.PinningRow {
	return store.PinningRow{
		Pinnable:          10664,
		Commit:            2298,
		Floating:          8366,
		Flagged:           3019,
		FloatingUnflagged: 5353,
		Yours:             store.OwnedSplit{Yours: 127, Elsewhere: 10537},
		FlaggedSplit:      store.OwnedSplit{Yours: 28, Elsewhere: 2991},
	}
}

func render(t *testing.T, total store.PinningRow, verified bool) string {
	t.Helper()
	var b strings.Builder
	writePinning(&b, total, verified)
	return b.String()
}

// The two measures answer different questions and must never be presented as one number: how a
// reference is written is an observation, whereas whether it is safe is the rule's call.
func TestPinningReportKeepsTheTwoMeasuresApart(t *testing.T) {
	out := render(t, pinningTotals(), true)

	for _, want := range []string{"2298", "10664", "3019", "5353"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not carry %s:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "actions/unpinned-tag") {
		t.Error("the flagged figure does not name the rule that produced it")
	}
	// Reporting a single percentage would imply the rule agrees with the commit share.
	if strings.Count(out, "%") > 1 {
		t.Errorf("more than one percentage, which reads as one measure:\n%s", out)
	}
}

// Both halves, always. Reporting only the actionable half understates what the corpus holds.
func TestPinningReportCarriesBothHalvesOfTheSplit(t *testing.T) {
	out := render(t, pinningTotals(), true)

	if !strings.Contains(out, "28") || !strings.Contains(out, "2991") {
		t.Errorf("the split is not reported in full:\n%s", out)
	}
	if !strings.Contains(out, "belong to") {
		t.Errorf("the split does not say what makes a reference yours:\n%s", out)
	}
}

// With no identity established, "0 are yours" would read as a measurement of the corpus rather
// than of the setup, and the two call for entirely different actions.
func TestPinningReportSaysWhenOwnershipIsUnknownRatherThanReportingZero(t *testing.T) {
	total := pinningTotals()
	total.Yours = store.OwnedSplit{Yours: 0, Elsewhere: total.Pinnable}
	total.FlaggedSplit = store.OwnedSplit{Yours: 0, Elsewhere: total.Flagged}

	out := render(t, total, false)

	if !strings.Contains(out, "unknown") {
		t.Errorf("an unverified identity is not reported as unknown:\n%s", out)
	}
	if !strings.Contains(out, "codeql-abom accounts") || !strings.Contains(out, "--refresh") {
		t.Errorf("nothing tells the reader how to establish it:\n%s", out)
	}
	if strings.Contains(out, "belong to") {
		t.Errorf("an unverified corpus still claims a share is yours:\n%s", out)
	}
}

// An empty corpus is not a clean one, and the difference matters more here than anywhere.
func TestNoPinnableReferencesIsNotTheSameAsEverythingBeingPinned(t *testing.T) {
	out := render(t, store.PinningRow{}, true)

	if !strings.Contains(out, "not the same as") {
		t.Errorf("an empty result is reported as though it were a clean one:\n%s", out)
	}
	if strings.Contains(out, "100") {
		t.Errorf("an empty corpus produced a percentage:\n%s", out)
	}
}
