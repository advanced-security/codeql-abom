package authoring

import (
	"strings"
	"testing"
	"time"
)

// A reviewer reads what each test last reported beside what it is expected to report, and a report
// older than the query or its fixtures describes a version that is no longer there.
func TestWhatATestReportedIsReadWithWhetherItIsStillCurrent(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	write(t, root, "test/A/A.actual", "| a job | uses a tag |\n")
	q, _, err := Find(root, "acme/a")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Now().Add(-time.Hour)
	touch(t, root, "src/A.ql", then)
	touch(t, root, "test/A/.github/workflows/ci.yml", then)

	got, err := Actuals(root, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Test != "test/A/A.qlref" || got[0].Actual == nil ||
		*got[0].Actual != "| a job | uses a tag |\n" || got[0].Expected == nil ||
		*got[0].Expected != "| a job |\n" || got[0].Stale != "" {
		t.Fatalf("actuals %+v", got)
	}

	touch(t, root, "test/A/.github/workflows/ci.yml", time.Now().Add(time.Hour))
	if got, _ = Actuals(root, q); !strings.Contains(got[0].Stale, "fixtures") {
		t.Errorf("a report older than its fixtures reads %q, want it stale", got[0].Stale)
	}
	touch(t, root, "test/A/.github/workflows/ci.yml", then)
	touch(t, root, "src/A.ql", time.Now().Add(time.Hour))
	if got, _ = Actuals(root, q); !strings.Contains(got[0].Stale, "query") {
		t.Errorf("a report older than its query reads %q, want it stale", got[0].Stale)
	}
}

// The digest names the text a reviewer was shown, and nothing else about the tests.
func TestTheReviewDigestMovesOnlyWithWhatWasReported(t *testing.T) {
	root := workspace(t, map[string]string{"A": "acme/a"})
	q, _, err := Find(root, "acme/a")
	if err != nil {
		t.Fatal(err)
	}
	nothing, err := Actuals(root, q)
	if err != nil || len(nothing) != 1 || nothing[0].Actual != nil {
		t.Fatalf("a test that reported nothing: %+v %v", nothing, err)
	}
	write(t, root, "test/A/A.actual", "| one |\n")
	one, _ := Actuals(root, q)
	write(t, root, "test/A/A.expected", "| something else |\n")
	stillOne, _ := Actuals(root, q)
	write(t, root, "test/A/A.actual", "| two |\n")
	two, _ := Actuals(root, q)

	switch {
	case DigestActuals(nothing) == DigestActuals(one):
		t.Error("reporting something reads as reporting nothing")
	case DigestActuals(one) != DigestActuals(stillOne):
		t.Error("the digest moved with the .expected, which is not what was reported")
	case DigestActuals(one) == DigestActuals(two):
		t.Error("a different report has the same digest")
	}
}
