package view

import (
	"strings"
	"testing"
)

// A term whose values are fixed in code is enforced or refused, never dropped.
//
// `boundary` was the case that bit: `store.boundaryFilter` returns an empty SQL fragment for any
// value it does not recognize — correctly, because a query string must not reach the statement —
// so an unrecognized value silently filtered nothing. That was survivable while a refusal said
// little; it stopped being survivable once a refused term started naming the Explorer as the
// surface that answers it, because the unfiltered graph now arrives under a promise that the
// filter was applied.
//
// `scope` is the same defect with a wider blast radius: `store.scopeFilter` switches on it and
// falls through to no filter at all, so one transposed letter answers a question about the
// operator's own supply chain with somebody else's.
func TestAValueOutsideATermsVocabularyIsRefused(t *testing.T) {
	for _, text := range []string{
		"boundary=nonsense",
		"boundary=crossOwner", // the browser's spelling, not the stored one
		"scope=ownd",          // one transposed letter, and the whole corpus comes back
		"scope=everything",    // the reading that was deliberately removed
		"ref=commit",          // pinning's vocabulary; the graph says `sha`
		"ref=sha256",
	} {
		if _, err := Parse(text); err == nil {
			t.Errorf("Parse(%q) was accepted, so it filters nothing and says nothing", text)
		}
	}
}

// And the vocabulary itself still parses, or the refusal has eaten the language.
func TestEveryValueATermAcceptsIsStillAccepted(t *testing.T) {
	for _, text := range []string{
		"boundary=same-repository",
		"boundary=same-owner",
		"boundary=cross-owner",
		"scope=owned",
		"scope=exposure",
		"ref=any",
		"ref=sha",
		"ref=tag",
	} {
		if _, err := Parse(text); err != nil {
			t.Errorf("Parse(%q): %v", text, err)
		}
	}
}

// The refusal has to name what it would have accepted. "Not a valid boundary" leaves a reader
// guessing between the store's spelling and the browser's, which is exactly the pair that differ.
func TestARefusedValueNamesTheOnesThatWouldHaveWorked(t *testing.T) {
	_, err := Parse("boundary=nonsense")
	if err == nil {
		t.Fatal("accepted")
	}
	for _, want := range []string{"boundary", "nonsense", "same-repository", "cross-owner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %s", want, err)
		}
	}
}
