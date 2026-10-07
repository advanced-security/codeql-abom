package view

import (
	"net/url"
	"slices"
	"strings"
)

// scopeWords is what each scope holds, in the words a description uses for it.
var scopeWords = map[string]string{
	ScopeOwned:       "what you own",
	ScopeSupplyChain: "what you own and what it runs",
	ScopeExposure:    "advisory routes",
}

// Description says what a predicate keeps, for a scope nobody wrote one for: every term it carries,
// under the label its filter control shows, in the order the catalog offers them.
//
// Read from the canonical text rather than from the fields, so a term added to the language is
// described by its name until the catalog labels it, never left out.
func (s Spec) Description() string {
	phrases := s.TermPhrases()
	if len(phrases) == 0 {
		return "Keeps everything: this scope narrows nothing."
	}
	return "Keeps " + strings.Join(phrases, "; ") + "."
}

// TermPhrases says each term a predicate carries as a reader reads it, `Label: values`, in the
// order the catalog offers them.
func (s Spec) TermPhrases() []string {
	held, err := url.ParseQuery(s.String())
	if err != nil || len(held) == 0 {
		return nil
	}
	var unlabeled []string
	for name := range held {
		if !slices.Contains(catalogOrder, name) {
			unlabeled = append(unlabeled, name)
		}
	}
	slices.Sort(unlabeled)
	names := append(slices.Clone(catalogOrder), unlabeled...)
	parts := make([]string, 0, len(held))
	for _, name := range names {
		values, ok := held[name]
		if !ok {
			continue
		}
		label := catalogWords[name].label
		if label == "" {
			label = name
		}
		parts = append(parts, label+": "+describedValues(name, values))
	}
	return parts
}

// describedValues spells a term's values: a scope by what it holds, a list member by member, and
// operator text such as a property value exactly as written.
func describedValues(name string, values []string) string {
	if name == paramScope && len(values) == 1 {
		if words, ok := scopeWords[values[0]]; ok {
			return words
		}
	}
	if catalogWords[name].arity == ArityList && len(values) == 1 {
		return strings.ReplaceAll(values[0], ",", ", ")
	}
	return strings.Join(values, ", ")
}
