package view

import (
	"slices"
	"testing"
)

// A run covers repositories, so it applies terms that choose them and names every other term.
func TestARunNamesEveryTermThatChoosesNoRepository(t *testing.T) {
	spec, err := Parse("scope=owned&flags=findings&ref=tag&kinds=action&minReach=2")
	if err != nil {
		t.Fatal(err)
	}

	got := spec.UnenforceableAsRun()
	want := []string{"flags", "kinds", "minReach", "ref"}
	if !slices.Equal(got, want) {
		t.Errorf("a run refuses %v, want %v", got, want)
	}
}

// A named scope's run covers what the repositories unit lists for it, so a scope that unit can
// list is one a run can cover, and one it refuses is refused for the run with the same terms.
func TestABuiltinARunCoversIsOneTheRepositoriesUnitLists(t *testing.T) {
	runnable := 0
	for _, b := range Builtins() {
		spec, err := Parse(b.Predicate)
		if err != nil {
			t.Fatal(err)
		}
		run, unit := spec.UnenforceableAsRun(), spec.UnenforceableAsRepositories()
		if !slices.Equal(run, unit) {
			t.Errorf("%s: a run refuses %v and the repositories unit refuses %v", b.Name, run, unit)
		}
		if len(run) == 0 {
			runnable++
		}
	}
	if runnable == 0 {
		t.Error("no built-in is runnable, so the comparison proves nothing")
	}
}

// The set path's findings terms still narrow a run to where the findings sit, and names chosen one
// by one still choose, or the Inventory's own selection is refused.
func TestARunAppliesNamesFindingsAndShapeAsWellAsTheUnitsTerms(t *testing.T) {
	spec, err := Parse("repositories=octo-org/app&rules=actions/*&notRules=abom/noisy" +
		"&provider=CodeQL&severity=error&mode=extended&q=app&scope=owned" +
		"&properties=team:pay&notProperties=team:ops")
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.UnenforceableAsRun(); len(got) != 0 {
		t.Errorf("a run refuses %v, want nothing; each of these chooses repositories", got)
	}
}

// A run takes the repositories unit's population only when its scope asks that unit's question.
// A bare list of names or a findings term alone keeps its own meaning.
func TestARunListsRepositoriesOnlyWhenItsScopeAsksTheUnitsQuestion(t *testing.T) {
	for predicate, want := range map[string]bool{
		"scope=owned":                 true,
		"q=pay":                       true,
		"properties=team:pay":         true,
		"notProperties=team:ops":      true,
		"scope=owned&rules=actions/*": true,
		"repositories=octo-org/app":   false,
		"rules=actions/*":             false,
		"mode=extended":               false,
		"":                            false,
	} {
		spec, err := Parse(predicate)
		if err != nil {
			t.Fatal(err)
		}
		if got := spec.AsksRepositoriesUnit(); got != want {
			t.Errorf("%q asks the repositories unit = %v, want %v", predicate, got, want)
		}
	}
}
