package serve

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// A reader cannot filter by a rule whose name they have to already know. The control offered a
// free-text box against `actions/unpinned-tag`-shaped identifiers, which is a memory test rather
// than a filter.
func TestATermsValuesAreServedFromTheCorpus(t *testing.T) {
	s, _ := newServer(t)

	rec := get(t, s, "/api/filters/rules/values")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var set struct {
		Values []struct{} `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("decoding: %v (body %s)", err, rec.Body.String())
	}
}

// How much of the population a value list cannot speak for travels with it. A column most rows
// left empty produces a short, confident list describing a fraction, and nothing on the list says
// so — which is how filtering by the only provider on offer silently answers for a fourteenth of
// the corpus.
func TestAValueListIsServedWithWhatItCannotSpeakFor(t *testing.T) {
	s, _ := newServer(t)

	var set struct {
		Uncovered *int `json:"uncovered"`
	}
	body := get(t, s, "/api/filters/provider/values").Body.Bytes()
	if err := json.Unmarshal(body, &set); err != nil {
		t.Fatalf("decoding: %v (body %s)", err, body)
	}
	if set.Uncovered == nil {
		t.Error("the answer does not say how many rows record no provider")
	}
}

// An unknown term is refused by name rather than answered with an empty list. Empty reads as "the
// corpus contains none of those", which is a measurement nobody took — and it is the same defect
// as a filter that parses and is then ignored.
func TestAnUnknownTermsValuesAreRefusedRatherThanEmpty(t *testing.T) {
	s, _ := newServer(t)

	if code := get(t, s, "/api/filters/notAFilter/values").Code; code != 400 {
		t.Errorf("status %d, want a refusal naming the term", code)
	}
}

// A term the language declares a vocabulary for is answered from that vocabulary, not from the
// corpus. Reading it from the data would offer only the values something happens to have used, so
// a scope nobody has looked at yet would be missing from the control that selects it.
func TestATermWithAFixedVocabularyIsAnsweredFromTheLanguage(t *testing.T) {
	s, _ := newServer(t)

	rec := get(t, s, "/api/filters/scope/values")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var set struct {
		Values []struct {
			Value string `json:"value"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &set); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	var declared []string
	for _, term := range viewspec.Catalog() {
		if term.Name == "scope" {
			declared = term.Values
		}
	}
	var got []string
	for _, v := range set.Values {
		got = append(got, v.Value)
	}
	if len(declared) == 0 || !slices.Equal(got, declared) {
		t.Fatalf("got %v, want the %v the language declares", got, declared)
	}
}

// Every term the catalog says has values somewhere has to have them there, or a control follows
// the pointer and gets a refusal. This is the guard that caught nine phantom endpoints when the
// catalog was first written.
func TestEveryTermTheCatalogSendsAControlToAnswers(t *testing.T) {
	s, _ := newServer(t)

	for _, term := range viewspec.Catalog() {
		if term.ValuesFrom == "" {
			continue
		}
		rec := get(t, s, "/"+term.ValuesFrom)
		if rec.Code == 404 {
			t.Errorf("term %q sends a control to %q, which is not served",
				term.Name, term.ValuesFrom)
		}
	}
}

// A term a reader picks from a list must not also be typed blind. The two together would let one
// spelling be offered and another accepted, and the accepted one narrows to nothing while
// reporting success.
func TestATermOffersItsValuesOrIsTypedButNotBoth(t *testing.T) {
	for _, term := range viewspec.Catalog() {
		if len(term.Values) > 0 && term.ValuesFrom != "" {
			t.Errorf("term %q both declares values and names a place to read them", term.Name)
		}
	}
}

func TestTheRuleTermSendsAControlSomewhere(t *testing.T) {
	for _, term := range viewspec.Catalog() {
		if term.Name != "rules" {
			continue
		}
		if len(term.Values) == 0 && term.ValuesFrom == "" {
			t.Error("the CodeQL rule term offers nothing, so it can only be typed from memory")
		}
		return
	}
	t.Fatal("no rules term in the catalog")
}

func TestValuesEndpointIsReadOnly(t *testing.T) {
	s, _ := newServer(t)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/filters/rules/values", nil))
	if rec.Code == 200 {
		t.Error("a write reached the values endpoint")
	}
}

// How many values a term has, so a control can say so before a reader opens it. Nineteen terms is
// too many to open one at a time to find the four worth using.
func TestTheCountOfEachTermsValuesIsServed(t *testing.T) {
	s, _ := newServer(t)

	rec := get(t, s, "/api/filters/counts")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var counts map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	// A declared vocabulary is counted from the language, which needs no corpus at all.
	if counts["scope"] != len(viewspec.Vocabulary("scope")) {
		t.Errorf("scope counted %d, want the %d the language declares",
			counts["scope"], len(viewspec.Vocabulary("scope")))
	}
}

// A term nobody can count is absent from the answer, never zero. Zero is a measurement — "this
// filter has nothing to offer" — and free text has no values to have none of.
func TestATermThatCannotBeCountedIsAbsentRatherThanZero(t *testing.T) {
	s, _ := newServer(t)

	var counts map[string]int
	if err := json.Unmarshal(get(t, s, "/api/filters/counts").Body.Bytes(), &counts); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if _, present := counts["q"]; present {
		t.Error("free text was counted, though its values are whatever somebody types")
	}
}

// Every term the catalog says has values must be countable, or a control ranks it by a number it
// never got and a real filter reads as an empty one.
func TestEveryTermWithValuesIsCounted(t *testing.T) {
	s, _ := newServer(t)

	var counts map[string]int
	if err := json.Unmarshal(get(t, s, "/api/filters/counts").Body.Bytes(), &counts); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, term := range viewspec.Catalog() {
		if len(term.Values) == 0 && term.ValuesFrom == "" {
			continue
		}
		if _, present := counts[term.Name]; !present {
			t.Errorf("term %q offers values but is not counted", term.Name)
		}
	}
}
