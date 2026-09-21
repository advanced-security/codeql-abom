package view

import (
	"strings"
	"testing"
)

// The shape a result was analyzed in is part of the question, not of its presentation: the same
// rule id means a defect in code the repository holds, or exposure reached across a `uses:`
// boundary, and a reader cannot be expected to know which without asking.
func TestModeIsATermTheLanguageWrites(t *testing.T) {
	spec := Spec{Mode: ModeExtended}
	text := spec.String()
	if !strings.Contains(text, "mode=extended") {
		t.Fatalf("canonical text %q does not carry the mode", text)
	}

	back, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	if back.Mode != ModeExtended {
		t.Errorf("mode round-tripped as %q, want %q", back.Mode, ModeExtended)
	}
	if !IsTerm("mode") {
		t.Error("IsTerm(\"mode\") is false, so a refusal would report a real term as a typo")
	}
}

// A shape ABOM does not build is refused rather than narrowed to nothing. Silently keeping an
// unrecognized value is what `checkVocabulary` exists to stop: the store's filter is right to
// produce no fragment for a value it does not know, which turns the refusal into no filter at all.
func TestModeRefusesAShapeABOMDoesNotBuild(t *testing.T) {
	_, err := Parse("mode=closure")
	if err == nil {
		t.Fatal("Parse accepted mode=closure; the vocabulary is regular and extended")
	}
	for _, want := range []string{ModeRegular, ModeExtended} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %q, so a reader cannot correct it", err, want)
		}
	}
}

// Absence is not a third shape. Every aggregate on record reads `regular`, so a spec that does not
// mention the mode has to produce the text it produced before the term existed — otherwise every
// stored view and shared link changes meaning on upgrade.
func TestNotAskingAboutTheShapeWritesNothing(t *testing.T) {
	if text := (Spec{Scope: ScopeOwned}).String(); strings.Contains(text, "mode") {
		t.Errorf("a spec that never mentioned the mode wrote %q", text)
	}
}

// Which surfaces answer it, stated once. A route that cannot apply the mode must refuse the term
// by name rather than drop it: a predicate accepted and partly dropped enforces something wider
// than the reader saw.
func TestTheSurfacesThatAnswerTheModeSayWhichTheyAre(t *testing.T) {
	asked := Spec{Mode: ModeExtended}

	for name, unenforceable := range map[string][]string{
		"set":           asked.UnenforceableAsSet(),
		"graph":         asked.UnenforceableAsGraph(),
		"inventory":     asked.UnenforceableAsInventory(),
		"relationships": asked.UnenforceableAsRelationships(),
	} {
		for _, term := range unenforceable {
			if term == "mode" {
				t.Errorf("the %s path refuses the mode; it reads edges or findings, so it has "+
					"one to state", name)
			}
		}
	}

	// A repository, a workflow, a job and an advisory are not results of an analysis, so none of
	// them has a shape to narrow by. Saying so is the honest answer.
	for name, unenforceable := range map[string][]string{
		"repositories": asked.UnenforceableAsRepositories(),
		"workflows":    asked.UnenforceableAsWorkflows(),
		"jobs":         asked.UnenforceableAsJobs(),
		"advisories":   asked.UnenforceableAsAdvisories(),
	} {
		found := false
		for _, term := range unenforceable {
			if term == "mode" {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s unit neither applies the mode nor refuses it, which is the silent "+
				"drop this list exists to prevent", name)
		}
	}
}

// The graph applies it rather than handing it over. A client cannot filter by a fact the payload
// does not carry, and the edges it never received are the ones the mode excluded.
func TestTheGraphAppliesTheModeItselfRatherThanHandingItToTheBrowser(t *testing.T) {
	if clientTerms["mode"] {
		t.Error("the graph hands the mode to the browser, which cannot see the edges it dropped")
	}
	if !graphTerms["mode"] {
		t.Error("the graph does not apply the mode, so an extended edge would be drawn in a " +
			"picture that declares none")
	}
}
