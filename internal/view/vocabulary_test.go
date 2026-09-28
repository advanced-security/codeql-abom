package view

import (
	"strings"
	"testing"
)

// Fixed-vocabulary terms must be enforced or refused, never dropped into an unfiltered answer.
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

// Accepted vocabulary must still parse.
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
