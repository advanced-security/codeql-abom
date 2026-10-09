package store

import (
	"database/sql"
	"fmt"
)

// FilterValue is one value a term can take in this corpus, with how much of the corpus takes it.
//
// The count is not decoration. A rule that reported once and a rule that reported ten thousand
// times are not equally likely to be what somebody came to filter by, and an alphabetical list
// says they are.
type FilterValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// corpusDerivedValues maps a filter term to the column that holds its values.
//
// Only terms whose values the corpus decides are here. A term with a fixed vocabulary is answered
// from the language, where it is already declared, and asking the store for one is a caller
// mistake rather than an empty result — the two read identically to whoever gets the list.
var corpusDerivedValues = map[string]struct{ column, from string }{
	"rules":    {"rule_id", "abom_finding"},
	"notRules": {"rule_id", "abom_finding"},
	"tags":     {"tag", findingTagRows},
	"notTags":  {"tag", findingTagRows},
	"severity": {"severity", "abom_finding"},
	"provider": {"provider", "abom_finding"},
	"kinds":    {"node_kind", "abom_node"},
}

// findingTagRows is one row per finding and tag its rule declared, and one empty row for a finding
// whose rule declared none, so those are counted as uncovered rather than vanishing.
var findingTagRows = `(SELECT t.tag AS tag FROM abom_finding_any_mode f
	LEFT JOIN abom_query_run_rule_tag t ON t.query_run_id = f.query_run_id AND t.rule_id = f.rule_id)`

// countedElsewhere are terms a control reads values for from an endpoint of its own, counted here
// so nothing that offers values goes unranked. A property term counts keys rather than values:
// the key is the first of its two choices, and what a control ranks is what it opens onto.
var countedElsewhere = map[string]struct{ column, from string }{
	"repositories":  {"repository_full_name", "repository"},
	"properties":    {"property_name", ownedPropertyRows},
	"notProperties": {"property_name", ownedPropertyRows},
}

// ownedPropertyRows are the property values of owned repositories, the only ones a filter may name.
var ownedPropertyRows = `(SELECT p.property_name FROM abom_repository_property p WHERE ` +
	ownedFullName("p.repository_full_name") + `)`

// FilterValueCounts reports how many values each corpus-derived term has.
//
// A term is absent when its values are not the store's to know, and present with zero when the
// corpus genuinely holds none. The two are different answers: absent means nobody counted, and
// ranking a term down for it would send a reader past a filter that works.
func (s *Store) FilterValueCounts() (map[string]int, error) {
	counts := make(map[string]int, len(corpusDerivedValues)+len(countedElsewhere))
	for _, source := range []map[string]struct{ column, from string }{
		corpusDerivedValues, countedElsewhere,
	} {
		for term, from := range source {
			query := fmt.Sprintf(
				`SELECT COUNT(DISTINCT %s) FROM %s WHERE %s IS NOT NULL AND %s <> ''`,
				from.column, from.from, from.column, from.column)
			var n int
			if err := s.db.QueryRow(query).Scan(&n); err != nil {
				return nil, fmt.Errorf("counting the values of %s: %w", term, err)
			}
			counts[term] = n
		}
	}
	return counts, nil
}

// FilterValueSet is what a term can take, and how much of the population it cannot speak for.
//
// The two travel together deliberately. A value list is built from rows that recorded a value, so
// a column most rows left empty produces a short, confident list describing a fraction — and
// `provider` is exactly that: one value over findings that predate the column. Offering the empty
// string as a value would turn the absence of a record into a category, so the rows it cannot
// account for are counted instead.
type FilterValueSet struct {
	Values []FilterValue `json:"values"`
	// Uncovered is rows recording no value at all. Zero is a measurement — the list is the whole
	// population — which is why it is always present rather than omitted when empty.
	Uncovered int `json:"uncovered"`
}

// FilterValues reports the values a term takes in this corpus, most used first.
//
// Empty values are left out rather than offered: an unattributed provider is a finding nobody
// recorded a producer for, and offering "" as something to filter by would turn the absence of a
// record into a category. How many were left out is returned beside them.
func (s *Store) FilterValues(term string) (FilterValueSet, error) {
	source, ok := corpusDerivedValues[term]
	if !ok {
		return FilterValueSet{}, fmt.Errorf("store: %q does not take its values from the corpus", term)
	}
	present := fmt.Sprintf("%s IS NOT NULL AND %s <> ''", source.column, source.column)

	values, err := queryAll(s, "reading the values of "+term, func(r *sql.Rows) (FilterValue, error) {
		var v FilterValue
		err := r.Scan(&v.Value, &v.Count)
		return v, err
	}, fmt.Sprintf(
		`SELECT %s, COUNT(*) FROM %s WHERE %s GROUP BY %s ORDER BY COUNT(*) DESC, %s`,
		source.column, source.from, present, source.column, source.column))
	if err != nil {
		return FilterValueSet{}, err
	}

	set := FilterValueSet{Values: values}
	if err := s.db.QueryRow(fmt.Sprintf(
		`SELECT COUNT(*) FROM %s WHERE NOT (%s)`, source.from, present)).Scan(&set.Uncovered); err != nil {
		return FilterValueSet{}, fmt.Errorf("counting rows with no %s: %w", term, err)
	}
	return set, nil
}
