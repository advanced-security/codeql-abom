package view

import (
	"net/url"
	"slices"
	"testing"
)

// The canonical form is the query string, so one text is simultaneously the shareable URL, the API
// parameters and the stored value. Every test here defends that identity: if a spec and its text
// stop agreeing, all three consumers disagree with each other and none of them says so.

func TestASpecSurvivesBeingWrittenAndReadBack(t *testing.T) {
	want := Spec{
		Scope:     "owned",
		Search:    "checkout",
		Kinds:     []string{"action", "workflow"},
		Origins:   []string{"seed"},
		Rules:     []string{"abom/unpinned", "actions/missing-workflow-permissions"},
		EdgeClass: []string{"external"},
		RefStyle:  "pinned",
		Boundary:  "external",
		MinReach:  5,
		Flags:     []string{"advisory", "findings"},
	}

	got, err := Parse(want.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", want.String(), err)
	}
	if !got.Equal(want) {
		t.Errorf("round trip lost something:\n got %+v\nwant %+v", got, want)
	}
}

// Two operators who built the same predicate by different routes must produce the same text, or it
// cannot be a storage key, a cache key, or a thing you compare two of.
func TestTheSamePredicateAlwaysWritesTheSameText(t *testing.T) {
	a := Spec{Kinds: []string{"workflow", "action"}, Flags: []string{"findings", "advisory"}}
	b := Spec{Kinds: []string{"action", "workflow"}, Flags: []string{"advisory", "findings"}}

	if a.String() != b.String() {
		t.Errorf("order changed the text:\n%q\n%q", a.String(), b.String())
	}
}

// A value named twice is one value. Left alone it would make two texts for one predicate.
func TestARepeatedValueIsOneValue(t *testing.T) {
	got, err := Parse("kinds=action,action,workflow")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got.Kinds) != 2 {
		t.Errorf("Kinds = %v, want two", got.Kinds)
	}
}

// An empty predicate is the whole corpus, and says so by writing nothing. A spec that serialized
// its defaults would put "everything" in every URL and every stored row.
func TestAnEmptyPredicateWritesNothing(t *testing.T) {
	if got := (Spec{}).String(); got != "" {
		t.Errorf("empty spec = %q, want empty", got)
	}
	got, err := Parse("")
	if err != nil {
		t.Fatalf("Parse(\"\"): %v", err)
	}
	if !got.Equal(Spec{}) {
		t.Errorf("Parse(\"\") = %+v, want the zero spec", got)
	}
}

// The whole point of the separation: a gate that acquired a `use3d` field would have to ignore it,
// and a change to what is *selected* must not hide behind a change to how it is *drawn*.
func TestPresentationIsNotPartOfThePredicate(t *testing.T) {
	for _, presentation := range []string{
		"selection=a/b", "hops=2", "direction=out", "groupBy=owner",
		"collapsed=true", "use3d=true", "animate=false", "view=mine",
	} {
		if _, err := Parse(presentation); err == nil {
			t.Errorf("Parse(%q) was accepted; presentation is not a predicate", presentation)
		}
	}
}

// A typo that silently matched everything is the worst possible failure for a gate that fails a
// pull request on a non-empty match.
func TestAnUnknownParameterIsRefusedRatherThanIgnored(t *testing.T) {
	if _, err := Parse("kindz=action"); err == nil {
		t.Fatal("an unknown parameter was accepted")
	}
}

func TestAMalformedReachIsRefused(t *testing.T) {
	for _, bad := range []string{"minReach=lots", "minReach=-1"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) was accepted", bad)
		}
	}
}

// Zero is the default and absent is the same question, so it must not appear in the text.
func TestAReachOfZeroIsNotWritten(t *testing.T) {
	if got := (Spec{MinReach: 0}).String(); got != "" {
		t.Errorf("minReach=0 wrote %q, want empty", got)
	}
	if got := (Spec{MinReach: 1}).String(); got == "" {
		t.Error("minReach=1 wrote nothing")
	}
}

// A search term is operator text and will contain spaces, slashes and ampersands.
func TestASearchTermSurvivesCharactersThatWouldBreakTheText(t *testing.T) {
	want := Spec{Search: "owner/repo & things=maybe"}
	got, err := Parse(want.String())
	if err != nil {
		t.Fatalf("Parse(%q): %v", want.String(), err)
	}
	if got.Search != want.Search {
		t.Errorf("Search = %q, want %q", got.Search, want.Search)
	}
}

// Equality is by predicate, not by the order somebody happened to build it in.
func TestTwoSpecsAreEqualWhenTheyAskTheSameQuestion(t *testing.T) {
	a := Spec{Rules: []string{"b", "a"}}
	b := Spec{Rules: []string{"a", "b"}}
	if !a.Equal(b) {
		t.Error("specs differing only in order were reported as different")
	}
	if a.Equal(Spec{Rules: []string{"a"}}) {
		t.Error("specs asking different questions were reported as equal")
	}
}

// Absent and empty are different questions everywhere else in this store, and here too: a flag
// list nobody set is not a flag list somebody emptied.
func TestAnEmptyValueIsRefusedRatherThanTreatedAsAbsent(t *testing.T) {
	if _, err := Parse("kinds="); err == nil {
		t.Error("an empty value was accepted; absent and empty are different questions")
	}
}

// Steps changes which nodes exist rather than how they are drawn — 23k against 3.6k — so it is a
// predicate the server applies, not presentation.
func TestStepsIsPartOfThePredicate(t *testing.T) {
	if got := (Spec{Steps: true}).String(); got != "steps=true" {
		t.Errorf("String() = %q, want %q", got, "steps=true")
	}
	// False is the default and absent asks the same question, so it must not be written.
	if got := (Spec{Steps: false}).String(); got != "" {
		t.Errorf("steps=false wrote %q, want empty", got)
	}
	got, err := Parse("steps=true")
	if err != nil || !got.Steps {
		t.Errorf("Parse(steps=true) = %+v, %v", got, err)
	}
}

func TestAStepsValueThatIsNotABooleanIsRefused(t *testing.T) {
	if _, err := Parse("steps=yes"); err == nil {
		t.Error("steps=yes was accepted")
	}
}

// The cross-language contract. Each text is asserted verbatim here and again in the interface's
// ViewSpecTests, because the same string is read by both. If the two implementations drift, a
// shared URL means one thing to the browser and another to the API, and nothing reports it.
func TestTheCanonicalTextIsExactlyThis(t *testing.T) {
	for _, c := range []struct {
		spec      Spec
		canonical string
	}{
		{Spec{Kinds: []string{"workflow", "action"}}, "kinds=action%2Cworkflow"},
		{
			Spec{Scope: "owned", Flags: []string{"findings", "advisory"}},
			"flags=advisory%2Cfindings&scope=owned",
		},
		{Spec{Search: "checkout", MinReach: 5}, "minReach=5&q=checkout"},
		{
			Spec{RefStyle: "pinned", Boundary: "external", EdgeClass: []string{"external"}},
			"boundary=external&edges=external&ref=pinned",
		},
		{
			Spec{Origins: []string{"upstream", "seed"}, Rules: []string{"abom/unpinned"}},
			"origins=seed%2Cupstream&rules=abom%2Funpinned",
		},
		{
			Spec{Rules: []string{"actions/*"}, NotRules: []string{"abom/noisy"}},
			"notRules=abom%2Fnoisy&rules=actions%2F%2A",
		},
		{
			Spec{Provider: "CodeQL", Severity: "error"},
			"provider=CodeQL&severity=error",
		},
		{
			Spec{Repositories: []string{"octo-org/tool", "octo-org/app"}},
			"repositories=octo-org%2Fapp%2Cocto-org%2Ftool",
		},
	} {
		if got := c.spec.String(); got != c.canonical {
			t.Errorf("String() = %q, want %q", got, c.canonical)
		}
		back, err := Parse(c.canonical)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.canonical, err)
			continue
		}
		if got := back.String(); got != c.canonical {
			t.Errorf("reparsing %q produced %q", c.canonical, got)
		}
	}
}

// A scope is the ground between "one repository" and "the whole account", and it enters the
// predicate language once so the Explorer, the API and the gate cannot spell it three ways.
//
// The key is carried with the value. Without it the same text means different things on two
// servers configured with different properties, and `business_id:ABC` *and* `app_name:ABC app`
// cannot be asked at all — which is what a business unit, an application and a campaign each are.
func TestAScopeNamesBothTheKeyAndTheValue(t *testing.T) {
	spec, err := Parse("properties=business_id:3&properties=app_name:ps-codeql&scope=owned")
	if err != nil {
		t.Fatal(err)
	}
	want := []PropertyMatch{{Name: "app_name", Value: "ps-codeql"}, {Name: "business_id", Value: "3"}}
	if !slices.Equal(spec.Properties, want) {
		t.Fatalf("Properties = %v, want %v", spec.Properties, want)
	}

	again, err := Parse(spec.String())
	if err != nil {
		t.Fatalf("re-parsing %q: %v", spec.String(), err)
	}
	if !slices.Equal(again.Properties, spec.Properties) {
		t.Errorf("round trip turned %v into %v", spec.Properties, again.Properties)
	}
}

// Exclusion follows the precedent `notRules` set, rather than inventing a second negation.
func TestAScopeCanExcludeAsWellAsInclude(t *testing.T) {
	spec, err := Parse("properties=business_id:3&notProperties=business_id:0")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spec.Properties, []PropertyMatch{{Name: "business_id", Value: "3"}}) {
		t.Errorf("Properties = %v", spec.Properties)
	}
	if !slices.Equal(spec.NotProperties, []PropertyMatch{{Name: "business_id", Value: "0"}}) {
		t.Errorf("NotProperties = %v", spec.NotProperties)
	}
}

// One repeated parameter per match, never a comma-joined list: a property value is operator text
// and a value containing a comma must not become two values. The store refuses to reproduce that
// limitation in its schema and the language must not reintroduce it in the text.
func TestAPropertyValueMayContainACommaAndASpace(t *testing.T) {
	spec, err := Parse("properties=" + url.QueryEscape("app_name:Billing, Payments and Fraud"))
	if err != nil {
		t.Fatal(err)
	}
	want := []PropertyMatch{{Name: "app_name", Value: "Billing, Payments and Fraud"}}
	if !slices.Equal(spec.Properties, want) {
		t.Fatalf("Properties = %v, want the one value whole", spec.Properties)
	}
	again, err := Parse(spec.String())
	if err != nil {
		t.Fatalf("re-parsing %q: %v", spec.String(), err)
	}
	if !slices.Equal(again.Properties, want) {
		t.Errorf("round trip split the value: %v", again.Properties)
	}
}

// A value may itself contain the separator, so only the first one divides the term.
func TestOnlyTheFirstSeparatorDividesAPropertyTerm(t *testing.T) {
	spec, err := Parse("properties=" + url.QueryEscape("owner:octo-org:platform"))
	if err != nil {
		t.Fatal(err)
	}
	want := []PropertyMatch{{Name: "owner", Value: "octo-org:platform"}}
	if !slices.Equal(spec.Properties, want) {
		t.Errorf("Properties = %v, want %v", spec.Properties, want)
	}
}

// A term missing either half would match on whatever the server happened to be configured with,
// which is the silent widening `Parse` exists to refuse.
func TestAPropertyTermMissingEitherHalfIsRefused(t *testing.T) {
	for _, bad := range []string{
		"properties=business_id", "properties=:3", "properties=business_id:",
		"notProperties=business_id", "properties=" + url.QueryEscape(" :3"),
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) was accepted; a scope needs both a key and a value", bad)
		}
	}
}

// `units` named a value and left the key to server configuration. Nothing parses it now: this
// project is unpublished, so a week-old term is replaced rather than carried as a second spelling.
func TestUnitsIsNoLongerATerm(t *testing.T) {
	if _, err := Parse("units=3"); err == nil {
		t.Error("units= was accepted; the key must travel with the value")
	}
}
