// Package advisory reads GitHub Security Advisories for the actions ecosystem.
//
// Advisories enrich the graph; they do not adjudicate it. An advisory is recorded as what it
// is — an external statement attributed to a GHSA identifier, exactly as a CodeQL result is
// attributed to a rule id. ABOM does not derive a vulnerable/not-vulnerable verdict of its own
// from one, because deciding whether a particular use is affected is what `actions/*` queries
// are for. What ABOM adds is reach: which repositories depend on the affected action, and with
// which references, so a human can judge.
package advisory

import (
	"encoding/json"
	"fmt"
	"io"
)

// Advisory is one published security advisory.
type Advisory struct {
	GHSAID      string
	CVEID       string
	Severity    string
	Summary     string
	URL         string
	CVSSv3Score float64
	CVSSv3      string
	CVSSv4Score float64
	CVSSv4      string
	// EPSSPercentage is the modelled probability of exploitation in the next 30 days, and
	// EPSSPercentile its rank among all scored vulnerabilities. Both are absent for recent
	// advisories, which is meaningful: no model output yet, not a score of zero.
	EPSSPercentage float64
	EPSSPercentile float64
	HasEPSS        bool
	CWEs           []string
	PublishedAt    string
	UpdatedAt      string
	// WithdrawnAt is set when GitHub has retracted the advisory.
	WithdrawnAt string
	Packages    []Package
}

// Package is one affected package and the range the advisory covers.
//
// Names are repository-scoped: an advisory naming github/codeql-action covers every action in
// that repository, including subpath actions such as github/codeql-action/init.
type Package struct {
	Ecosystem string
	Name      string
	// VulnerableRange is a human-readable constraint such as ">= 4.0.0, < 4.1.3".
	VulnerableRange string
	// FirstPatched is empty when no fix has been published.
	FirstPatched string
}

// Withdrawn reports whether GitHub has retracted the advisory.
func (a Advisory) Withdrawn() bool { return a.WithdrawnAt != "" }

// wire mirrors the API response so the parsed form stays free of its shape.
type wire struct {
	GHSAID         string  `json:"ghsa_id"`
	CVEID          *string `json:"cve_id"`
	Severity       string  `json:"severity"`
	Summary        string  `json:"summary"`
	HTMLURL        string  `json:"html_url"`
	PublishedAt    *string `json:"published_at"`
	UpdatedAt      *string `json:"updated_at"`
	WithdrawnAt    *string `json:"withdrawn_at"`
	CVSSSeverities *struct {
		V3 *struct {
			Vector *string `json:"vector_string"`
			Score  float64 `json:"score"`
		} `json:"cvss_v3"`
		V4 *struct {
			Vector *string `json:"vector_string"`
			Score  float64 `json:"score"`
		} `json:"cvss_v4"`
	} `json:"cvss_severities"`
	EPSS *struct {
		Percentage float64 `json:"percentage"`
		Percentile float64 `json:"percentile"`
	} `json:"epss"`
	CWEs []struct {
		ID string `json:"cwe_id"`
	} `json:"cwes"`
	Vulnerabilities []struct {
		Package *struct {
			Ecosystem string `json:"ecosystem"`
			Name      string `json:"name"`
		} `json:"package"`
		VulnerableVersionRange *string `json:"vulnerable_version_range"`
		FirstPatchedVersion    *string `json:"first_patched_version"`
	} `json:"vulnerabilities"`
}

// Parse decodes an advisories response.
//
// Decoding streams so that a full page never has to be held twice, matching how the rest of the
// pipeline reads results.
func Parse(r io.Reader) ([]Advisory, error) {
	dec := json.NewDecoder(r)

	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading advisories: %w", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, fmt.Errorf("expected an array of advisories, got %v", tok)
	}

	var out []Advisory
	for dec.More() {
		var w wire
		if err := dec.Decode(&w); err != nil {
			return nil, fmt.Errorf("decoding advisory: %w", err)
		}
		out = append(out, w.advisory())
	}

	// The closing bracket must be consumed or a caller reusing the reader desynchronises.
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("closing advisories array: %w", err)
	}
	return out, nil
}

func (w wire) advisory() Advisory {
	a := Advisory{
		GHSAID:      w.GHSAID,
		CVEID:       deref(w.CVEID),
		Severity:    w.Severity,
		Summary:     w.Summary,
		URL:         w.HTMLURL,
		PublishedAt: deref(w.PublishedAt),
		UpdatedAt:   deref(w.UpdatedAt),
		WithdrawnAt: deref(w.WithdrawnAt),
	}

	if c := w.CVSSSeverities; c != nil {
		if c.V3 != nil {
			a.CVSSv3Score, a.CVSSv3 = c.V3.Score, deref(c.V3.Vector)
		}
		if c.V4 != nil {
			a.CVSSv4Score, a.CVSSv4 = c.V4.Score, deref(c.V4.Vector)
		}
	}
	if e := w.EPSS; e != nil {
		a.EPSSPercentage, a.EPSSPercentile, a.HasEPSS = e.Percentage, e.Percentile, true
	}
	for _, c := range w.CWEs {
		a.CWEs = append(a.CWEs, c.ID)
	}
	for _, v := range w.Vulnerabilities {
		if v.Package == nil {
			continue
		}
		a.Packages = append(a.Packages, Package{
			Ecosystem:       v.Package.Ecosystem,
			Name:            v.Package.Name,
			VulnerableRange: deref(v.VulnerableVersionRange),
			FirstPatched:    deref(v.FirstPatchedVersion),
		})
	}
	return a
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
