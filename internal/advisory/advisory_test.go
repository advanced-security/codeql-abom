package advisory

import (
	"os"
	"strings"
	"testing"
)

// The fixture is a real response. Regenerate with:
//
//	curl -H 'Accept: application/vnd.github+json' \
//	  'https://api.github.com/advisories?ecosystem=actions&per_page=100&affects=github/codeql-action,tj-actions/changed-files,reviewdog/action-setup' \
//	  -o internal/advisory/testdata/advisories.json
const fixture = "testdata/advisories.json"

func parseFixture(t *testing.T) []Advisory {
	t.Helper()
	f, err := os.Open(fixture)
	if err != nil {
		t.Fatalf("opening fixture: %v", err)
	}
	defer f.Close()

	got, err := Parse(f)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return got
}

func find(t *testing.T, all []Advisory, ghsa string) Advisory {
	t.Helper()
	for _, a := range all {
		if a.GHSAID == ghsa {
			return a
		}
	}
	t.Fatalf("%s not in fixture", ghsa)
	return Advisory{}
}

func TestParseReadsAdvisoryFacts(t *testing.T) {
	a := find(t, parseFixture(t), "GHSA-mrrh-fwg8-r2c3")

	if a.Severity != "high" {
		t.Errorf("Severity = %q, want high", a.Severity)
	}
	if a.CVSSv3Score != 8.6 {
		t.Errorf("CVSSv3Score = %v, want 8.6", a.CVSSv3Score)
	}
	if a.CVSSv3 == "" {
		t.Error("CVSSv3 vector is empty")
	}
	if !a.HasEPSS || a.EPSSPercentile <= 0 {
		t.Errorf("EPSS = %v/%v (present=%v), want a populated score",
			a.EPSSPercentage, a.EPSSPercentile, a.HasEPSS)
	}
	if len(a.CWEs) == 0 {
		t.Error("no CWEs parsed")
	}
	if a.URL == "" || a.Summary == "" {
		t.Error("URL or Summary missing")
	}
}

// One advisory can cover several ranges of the same package; dropping any of them would
// understate what it affects.
func TestParseReadsEveryAffectedRange(t *testing.T) {
	a := find(t, parseFixture(t), "GHSA-vqf5-2xx6-9wfm")

	if len(a.Packages) != 2 {
		t.Fatalf("parsed %d affected ranges, want 2", len(a.Packages))
	}
	for _, p := range a.Packages {
		if p.Name != "github/codeql-action" {
			t.Errorf("package name = %q", p.Name)
		}
		if p.Ecosystem != "actions" {
			t.Errorf("ecosystem = %q", p.Ecosystem)
		}
		if p.VulnerableRange == "" {
			t.Error("vulnerable range is empty")
		}
	}
}

// An unfixed advisory has no patched version, which must stay distinguishable from "0".
func TestParseHandlesUnfixedAdvisory(t *testing.T) {
	a := find(t, parseFixture(t), "GHSA-qmg3-hpqr-gqvc")

	if len(a.Packages) != 1 {
		t.Fatalf("parsed %d packages, want 1", len(a.Packages))
	}
	if got := a.Packages[0].FirstPatched; got != "" {
		t.Errorf("FirstPatched = %q, want empty for an unfixed advisory", got)
	}
	if a.Packages[0].VulnerableRange == "" {
		t.Error("vulnerable range is empty")
	}
}

// A missing CVSS v4 block must not be read as a score of zero on the v3 fields.
func TestParseKeepsCVSSVersionsSeparate(t *testing.T) {
	a := find(t, parseFixture(t), "GHSA-mcph-m25j-8j63")

	if a.CVSSv3Score == 0 {
		t.Error("CVSSv3Score is zero, want the published v3 score")
	}
	if a.CVSSv4 != "" && a.CVSSv4Score == 0 {
		t.Error("a v4 vector was parsed without its score")
	}
}

func TestParseReadsEveryAdvisoryInTheResponse(t *testing.T) {
	if got := len(parseFixture(t)); got != 4 {
		t.Errorf("parsed %d advisories, want 4", got)
	}
}

func TestParseRejectsNonArrayResponses(t *testing.T) {
	if _, err := Parse(strings.NewReader(`{"message":"Not Found"}`)); err == nil {
		t.Error("Parse accepted an error response as advisories")
	}
}

func TestParseEmptyResponse(t *testing.T) {
	got, err := Parse(strings.NewReader(`[]`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("parsed %d advisories from an empty array", len(got))
	}
}

func TestWithdrawnReflectsRetraction(t *testing.T) {
	if (Advisory{}).Withdrawn() {
		t.Error("an advisory with no withdrawal date reports as withdrawn")
	}
	if !(Advisory{WithdrawnAt: "2026-01-01T00:00:00Z"}).Withdrawn() {
		t.Error("a retracted advisory does not report as withdrawn")
	}
}
