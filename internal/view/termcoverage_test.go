package view

import (
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
		setTerms, graphTerms, clientTerms, inventoryTerms, repositoryTerms, advisoryTerms, policyTerms,
		usesTerms,
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
	held, err := url.ParseQuery(specCarryingEveryTerm().String())
	if err != nil {
		t.Fatalf("parsing the canonical text: %v", err)
	}
	out := []string{}
	for key := range held {
		out = append(out, key)
	}
	if fields := reflect.TypeOf(Spec{}).NumField(); len(out) != fields {
		t.Fatalf("the spec wrote %d terms, want all %d; a field was added and not set here", len(out), fields)
	}
	return out
}

// Every value is one both parsers accept, and a list term with a fixed vocabulary carries all of it,
// so the text this writes holds the browser to every term and every value the picker can offer.
func specCarryingEveryTerm() Spec {
	return Spec{
		Scope: "owned", Search: "x", Kinds: Vocabulary(paramKinds), Origins: Vocabulary(paramOrigins),
		Rules: []string{"r"}, NotRules: []string{"r"}, Tags: []string{"t"}, NotTags: []string{"t"},
		Repositories:  []string{"o/r"},
		Properties:    []PropertyMatch{{Name: "k", Value: "v"}},
		NotProperties: []PropertyMatch{{Name: "k", Value: "v"}},
		Provider:      "p", Severity: "high", EdgeClass: Vocabulary(paramEdgeClass),
		SecuritySeverities: Vocabulary(paramSecuritySeverity),
		AdvisorySeverities: Vocabulary(paramAdvisorySeverity),
		Reached:            Vocabulary(paramReached),
		Policies:           []string{"p"},
		CodeScanning:       Vocabulary(paramCodeScanning),
		Sources:            Vocabulary(paramSource),
		EdgeKinds:          Vocabulary(paramEdgeKinds),
		RefStyle:           "tag", Boundary: "cross-owner", MinReach: 1,
		Flags: Vocabulary(paramFlags), Steps: true, Mode: ModeRegular,
		At: []string{"v4"},
	}
}

// The browser reads the very text Go writes for a spec carrying every term, from the file both
// suites open. Two hand-kept lists let a term reach the filter picker that the browser could not
// parse, and choosing it took the whole interface down.
func TestTheBrowserReadsTheTextCarryingEveryTerm(t *testing.T) {
	spec := specCarryingEveryTerm()
	fields := reflect.ValueOf(spec)
	for i := range fields.NumField() {
		if fields.Field(i).IsZero() {
			t.Errorf("specCarryingEveryTerm leaves %s unset, so nothing carries that term to the browser",
				fields.Type().Field(i).Name)
		}
	}
	text := spec.String()
	parsed, err := Parse(text)
	if err != nil {
		t.Fatalf("Go refuses the text it wrote, %q: %v", text, err)
	}
	if again := parsed.String(); again != text {
		t.Errorf("the text does not survive a round trip:\n wrote %s\n reads %s", text, again)
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "every-term.txt"))
	if err != nil {
		t.Fatalf("reading the text the browser's ViewSpecTests parse: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != text {
		t.Errorf("testdata/every-term.txt is not what Go writes; make it exactly\n%s\nso the browser is held to it", text)
	}
}
