package store

import "sort"

// FindingFacet is one kind of finding a graph node carries: the rule, who reported it and how
// severe the analysis said it was, with how many findings share all of them.
type FindingFacet struct {
	Rule string `json:"rule"`
	// Provider is the SARIF tool driver, verbatim. Empty where no producer was recorded, which is
	// most findings older than the column and is never relabeled as CodeQL.
	Provider string `json:"provider,omitempty"`
	// Severity is the level the result reported: `error`, `warning` or `recommendation`.
	Severity string `json:"severity,omitempty"`
	// SecuritySeverity is the rule's own `@security-severity` score. Absent where the rule
	// declares none.
	SecuritySeverity *float64 `json:"securitySeverity,omitempty"`
	// Band is that score read as code scanning reads it, so no client keeps the thresholds.
	Band  string `json:"band"`
	Count int    `json:"count"`
}

// facetKey is everything that makes two facets the same kind of finding.
type facetKey struct {
	rule, provider, severity string
	security                 float64
	scored                   bool
}

func (f FindingFacet) key() facetKey {
	k := facetKey{rule: f.Rule, provider: f.Provider, severity: f.Severity}
	if f.SecuritySeverity != nil {
		k.security, k.scored = *f.SecuritySeverity, true
	}
	return k
}

// mergeFacets adds each facet of `more` into `into`, summing the counts of the same kind.
func mergeFacets(into, more []FindingFacet) []FindingFacet {
	at := make(map[facetKey]int, len(into))
	for i, f := range into {
		at[f.key()] = i
	}
	for _, f := range more {
		if i, seen := at[f.key()]; seen {
			into[i].Count += f.Count
			continue
		}
		at[f.key()] = len(into)
		into = append(into, f)
	}
	sortFacets(into)
	return into
}

// sortFacets orders by rule and then by the rest, so two reads of one corpus are one payload.
func sortFacets(facets []FindingFacet) {
	sort.Slice(facets, func(i, j int) bool {
		a, b := facets[i].key(), facets[j].key()
		switch {
		case a.rule != b.rule:
			return a.rule < b.rule
		case a.severity != b.severity:
			return a.severity < b.severity
		case a.provider != b.provider:
			return a.provider < b.provider
		case a.scored != b.scored:
			return !a.scored
		default:
			return a.security < b.security
		}
	})
}
