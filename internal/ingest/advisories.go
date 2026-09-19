package ingest

import (
	"github.com/advanced-security/codeql-abom/internal/advisory"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Advisories converts fetched advisories into the form the store records.
//
// Here rather than beside either type, because the two are deliberately separate: the fetcher
// must not depend on the schema, and the schema must not be shaped by an API response. Both the
// crawl and the fixture corpus read it, so a recorded response and a live one cannot be stored
// differently.
func Advisories(advisories []advisory.Advisory) []store.Advisory {
	out := make([]store.Advisory, 0, len(advisories))
	for _, a := range advisories {
		converted := store.Advisory{
			GHSAID:         a.GHSAID,
			CVEID:          a.CVEID,
			Severity:       a.Severity,
			Summary:        a.Summary,
			URL:            a.URL,
			CVSSv3Score:    a.CVSSv3Score,
			CVSSv3:         a.CVSSv3,
			CVSSv4Score:    a.CVSSv4Score,
			CVSSv4:         a.CVSSv4,
			EPSSPercentage: a.EPSSPercentage,
			EPSSPercentile: a.EPSSPercentile,
			HasEPSS:        a.HasEPSS,
			CWEs:           a.CWEs,
			PublishedAt:    a.PublishedAt,
			UpdatedAt:      a.UpdatedAt,
			// Carried rather than filtered: a retraction is a fact about the advisory, and
			// dropping it here would leave nothing able to explain a finding's disappearance.
			WithdrawnAt: a.WithdrawnAt,
		}
		for _, p := range a.Packages {
			converted.Packages = append(converted.Packages, store.AdvisoryPackage{
				Ecosystem:       p.Ecosystem,
				Name:            p.Name,
				VulnerableRange: p.VulnerableRange,
				FirstPatched:    p.FirstPatched,
			})
		}
		out = append(out, converted)
	}
	return out
}
