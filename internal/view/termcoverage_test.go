package view

import (
	"net/url"
	"sort"
	"testing"
)

// Every term the language can write is applied by some consumer.
//
// The claim this checks is that a saved view cannot carry a term *nothing* enforces. If one could,
// it would be stored, rendered as a tab, and refused by every surface that drew it — text that
// looks like a question and is answerable nowhere. Pinned rather than remembered, because the set
// grows a term at a time and the gap would open silently.
func TestEveryTermIsAppliedBySomeConsumer(t *testing.T) {
	applied := map[string]bool{}
	for _, side := range []map[string]bool{
		setTerms, graphTerms, clientTerms, inventoryTerms, repositoryTerms, advisoryTerms,
	} {
		for term := range side {
			applied[term] = true
		}
	}

	orphaned := []string{}
	for _, term := range everyTerm(t) {
		if !applied[term] {
			orphaned = append(orphaned, term)
		}
	}
	if len(orphaned) > 0 {
		sort.Strings(orphaned)
		t.Errorf("no consumer applies %v; a view carrying one is refused wherever it is drawn",
			orphaned)
	}
}

// And `IsTerm` knows about all of them, or a refusal calls a real term a typo and tells the reader
// to go looking for a spelling mistake that is not there.
func TestIsTermKnowsEveryTermTheLanguageWrites(t *testing.T) {
	for _, term := range everyTerm(t) {
		if !IsTerm(term) {
			t.Errorf("IsTerm(%q) is false, so a refusal would report it as a name ABOM never had",
				term)
		}
	}
	if IsTerm("kindz") {
		t.Error("IsTerm accepts a typo, so nothing is ever reported as one")
	}
}

// everyTerm reads the term names out of a spec that carries all of them, so this cannot drift from
// the serializer the way a hand-written list would.
func everyTerm(t *testing.T) []string {
	t.Helper()
	spec := Spec{
		Scope: "owned", Search: "x", Kinds: []string{"action"}, Origins: []string{"external"},
		Rules: []string{"r"}, NotRules: []string{"r"}, Repositories: []string{"o/r"},
		Properties:    []PropertyMatch{{Name: "k", Value: "v"}},
		NotProperties: []PropertyMatch{{Name: "k", Value: "v"}},
		Provider:      "p", Severity: "high", EdgeClass: []string{"e"},
		EdgeKinds: []string{EdgeKindJobNeedsJob},
		RefStyle:  "tag", Boundary: "cross-owner", MinReach: 1,
		Flags: []string{"findings"}, Steps: true,
	}
	held, err := url.ParseQuery(spec.String())
	if err != nil {
		t.Fatalf("parsing the canonical text: %v", err)
	}
	out := []string{}
	for key := range held {
		out = append(out, key)
	}
	if len(out) != 18 {
		t.Fatalf("the spec wrote %d terms, want all 18; a field was added and not set here", len(out))
	}
	return out
}
