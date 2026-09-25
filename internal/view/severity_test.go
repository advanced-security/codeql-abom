package view

import (
	"slices"
	"strings"
	"testing"
)

// A rule's score is read through the thresholds code scanning publishes, and a rule that declares
// none is `unknown` rather than the lowest band: absent is not harmless.
func TestABandIsTheScoreReadThroughCodeScanningsThresholds(t *testing.T) {
	score := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		score *float64
		want  string
	}{
		{nil, BandUnknown},
		{score(0), BandUnknown},
		{score(0.1), BandLow},
		{score(3.9), BandLow},
		{score(4.0), BandMedium},
		{score(6.9), BandMedium},
		{score(7.0), BandHigh},
		{score(8.9), BandHigh},
		{score(9.0), BandCritical},
		{score(10), BandCritical},
	} {
		if got := BandOf(c.score); got != c.want {
			t.Errorf("BandOf(%v) = %q, want %q", c.score, got, c.want)
		}
	}
}

// The bands are offered most severe first, and rank in that order: severity outranks everything
// a finding is ordered by after it.
func TestBandsRankMostSevereFirst(t *testing.T) {
	want := []string{BandCritical, BandHigh, BandMedium, BandLow, BandUnknown}
	if got := Bands(); !slices.Equal(got, want) {
		t.Fatalf("Bands() = %v, want %v", got, want)
	}
	for i := 1; i < len(want); i++ {
		if BandRank(want[i-1]) <= BandRank(want[i]) {
			t.Errorf("%s does not outrank %s", want[i-1], want[i])
		}
	}
	if BandRank("severe") != BandRank(BandUnknown) {
		t.Error("a word that is not a band ranks above one nobody scored")
	}
}

// `securitySeverity` asks for findings by band, as a closed list: several bands are one question,
// and a word that is not a band is refused with the bands named.
func TestSecuritySeverityTakesBandsAndRefusesAnythingElse(t *testing.T) {
	spec, err := Parse("securitySeverity=high,critical")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !slices.Equal(spec.SecuritySeverities, []string{BandCritical, BandHigh}) {
		t.Errorf("SecuritySeverities = %v, want critical and high", spec.SecuritySeverities)
	}
	if got := spec.String(); got != "securitySeverity=critical%2Chigh" {
		t.Errorf("String() = %q, want the bands written once, in order", got)
	}

	_, err = Parse("securitySeverity=severe")
	if err == nil || !strings.Contains(err.Error(), "securitySeverity") ||
		!strings.Contains(err.Error(), BandCritical) {
		t.Errorf("Parse(securitySeverity=severe) = %v, want a refusal naming the bands", err)
	}
	// The level a result reported is a different vocabulary, asked by `severity`.
	if _, err := Parse("securitySeverity=error"); err == nil {
		t.Error("a SARIF level was accepted as a band")
	}
}

// A band is a fact about one finding, so it is applied wherever `severity` is: the set path, the
// listing, and the graph's per-finding facets. A policy is the set path.
func TestSecuritySeverityIsAppliedWhereSeverityIs(t *testing.T) {
	for _, side := range []struct {
		name string
		has  bool
	}{
		{"set", setTerms[paramSecuritySeverity]},
		{"inventory", inventoryTerms[paramSecuritySeverity]},
		{"client", clientTerms[paramSecuritySeverity]},
	} {
		if !side.has {
			t.Errorf("the %s side does not apply securitySeverity", side.name)
		}
	}
	spec, _ := Parse("securitySeverity=critical&rules=actions/*")
	if got := spec.ClientApplied(); !slices.Contains(got, paramSecuritySeverity) {
		t.Errorf("ClientApplied = %v, want the band handed to whoever draws the graph", got)
	}
}
