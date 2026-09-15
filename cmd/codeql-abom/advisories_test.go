package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The two Advisory types are field-identical today, which is exactly what makes a hand-written
// conversion dangerous: a field added to one side and forgotten here loses data silently, and
// the result still compiles, still stores and still renders. Every advisory would simply look
// unscored, or unattributed, forever.
func TestConvertingAnAdvisoryCarriesEveryField(t *testing.T) {
	from := advisory.Advisory{
		GHSAID:         "GHSA-xxxx-yyyy-zzzz",
		CVEID:          "CVE-2024-0001",
		Severity:       "high",
		Summary:        "Command injection in an action",
		URL:            "https://github.com/advisories/GHSA-xxxx-yyyy-zzzz",
		CVSSv3Score:    8.8,
		CVSSv3:         "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:H/I:H/A:H",
		CVSSv4Score:    9.3,
		CVSSv4:         "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:P/VC:H/VI:H/VA:H",
		EPSSPercentage: 0.00042,
		EPSSPercentile: 0.11,
		HasEPSS:        true,
		CWEs:           []string{"CWE-77", "CWE-94"},
		PublishedAt:    "2024-01-02T03:04:05Z",
		UpdatedAt:      "2024-02-03T04:05:06Z",
		WithdrawnAt:    "",
		Packages: []advisory.Package{{
			Ecosystem:       "actions",
			Name:            "some-org/some-action",
			VulnerableRange: ">= 4.0.0, < 4.1.3",
			FirstPatched:    "4.1.3",
		}},
	}

	got := storeAdvisories([]advisory.Advisory{from})

	want := []store.Advisory{{
		GHSAID:         from.GHSAID,
		CVEID:          from.CVEID,
		Severity:       from.Severity,
		Summary:        from.Summary,
		URL:            from.URL,
		CVSSv3Score:    from.CVSSv3Score,
		CVSSv3:         from.CVSSv3,
		CVSSv4Score:    from.CVSSv4Score,
		CVSSv4:         from.CVSSv4,
		EPSSPercentage: from.EPSSPercentage,
		EPSSPercentile: from.EPSSPercentile,
		HasEPSS:        from.HasEPSS,
		CWEs:           from.CWEs,
		PublishedAt:    from.PublishedAt,
		UpdatedAt:      from.UpdatedAt,
		WithdrawnAt:    from.WithdrawnAt,
		Packages: []store.AdvisoryPackage{{
			Ecosystem:       "actions",
			Name:            "some-org/some-action",
			VulnerableRange: ">= 4.0.0, < 4.1.3",
			FirstPatched:    "4.1.3",
		}},
	}}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("converted advisory\n got %+v\nwant %+v", got, want)
	}
}

// A conversion that defaulted the score would turn "no model output yet" into "a probability of
// zero", which reads as safe and is not a measurement at all.
func TestAnUnscoredAdvisoryStaysUnscored(t *testing.T) {
	got := storeAdvisories([]advisory.Advisory{{GHSAID: "GHSA-none", Severity: "low"}})

	if len(got) != 1 {
		t.Fatalf("converted %d advisories, want 1", len(got))
	}
	if got[0].HasEPSS {
		t.Error("an advisory with no EPSS was converted as though it had one")
	}
	if got[0].EPSSPercentage != 0 {
		t.Errorf("EPSSPercentage = %v, want the absent value left alone", got[0].EPSSPercentage)
	}
}

// A retraction is a fact about the advisory, not a reason to forget it: a report that quietly
// dropped one could never explain why a finding it used to show had disappeared.
func TestAWithdrawnAdvisoryIsStillRecorded(t *testing.T) {
	got := storeAdvisories([]advisory.Advisory{{
		GHSAID:      "GHSA-gone",
		WithdrawnAt: "2024-03-04T05:06:07Z",
	}})

	if len(got) != 1 {
		t.Fatalf("converted %d advisories, want the withdrawal recorded rather than dropped", len(got))
	}
	if got[0].WithdrawnAt == "" {
		t.Error("the withdrawal date was lost, so nothing can say the advisory was retracted")
	}
}

// The advisory endpoint is rate limited, and an empty graph names no packages. Asking anyway
// spends budget to be told nothing, on exactly the cycles that run before any data exists.
func TestNoPackagesMeansNoRequest(t *testing.T) {
	s := newTestStore(t)
	var asked bool
	refresh := advisorFor(s, func(context.Context, []string) ([]advisory.Advisory, error) {
		asked = true
		return nil, nil
	}, nil)

	n, err := refresh(context.Background())

	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n != 0 {
		t.Errorf("recorded %d advisories from an empty graph, want none", n)
	}
	if asked {
		t.Error("queried the advisory API for a graph that names no packages")
	}
}

// The store is the thing that has to end up holding them; a refresh that fetched perfectly and
// wrote nowhere is the failure mode this whole package already had once.
func TestAFetchedAdvisoryReachesTheStore(t *testing.T) {
	s := newTestStore(t)
	seedExternalAction(t, s, "actions/checkout")

	var named []string
	refresh := advisorFor(s, func(_ context.Context, packages []string) ([]advisory.Advisory, error) {
		named = packages
		return []advisory.Advisory{{
			GHSAID:   "GHSA-stored",
			Severity: "critical",
			Packages: []advisory.Package{{Ecosystem: "actions", Name: "actions/checkout"}},
		}}, nil
	}, nil)

	n, err := refresh(context.Background())
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if n != 1 {
		t.Errorf("recorded %d advisories, want 1", n)
	}
	if !reflect.DeepEqual(named, []string{"actions/checkout"}) {
		t.Errorf("asked about %v, want the packages the graph names", named)
	}

	held, err := s.AdvisoriesByReach(10, store.RepositoryScope{})
	if err != nil {
		t.Fatalf("AdvisoriesByReach: %v", err)
	}
	if len(held) == 0 {
		t.Fatal("the fetched advisory never reached the store")
	}
}

// A failure has to reach the supervisor, which is what decides that enrichment failing is not
// the crawl failing. Swallowing it here would make an outage look like "no advisories exist".
func TestAFailedFetchIsReportedRatherThanCountedAsNone(t *testing.T) {
	s := newTestStore(t)
	seedExternalAction(t, s, "actions/checkout")
	refresh := advisorFor(s, func(context.Context, []string) ([]advisory.Advisory, error) {
		return nil, errors.New("rate limit exhausted")
	}, nil)

	if _, err := refresh(context.Background()); err == nil {
		t.Error("a failed fetch was reported as a successful refresh of nothing")
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return s
}

func seedExternalAction(t *testing.T, s *store.Store, key string) {
	t.Helper()
	w := store.NewWriter(s, 0)
	owner, repo, _ := strings.Cut(key, "/")
	if err := w.AddNode(store.Node{
		Key: key, Kind: "action", Owner: owner, Repo: repo, IsExternal: true,
	}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}
