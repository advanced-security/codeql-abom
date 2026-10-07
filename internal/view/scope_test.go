package view

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Ownership and the supply chain it runs are two questions with two spellings. One value meaning
// both is how "What you own" came to list another organization's actions.
func TestTheLanguageSpellsWhatYouOwnAndWhatItRunsApart(t *testing.T) {
	if got, want := Vocabulary(paramScope), []string{ScopeOwned, ScopeSupplyChain, ScopeExposure}; !slices.Equal(got, want) {
		t.Fatalf("scope accepts %v, want %v", got, want)
	}
	spec, err := Parse("scope=supply-chain")
	if err != nil {
		t.Fatalf("scope=supply-chain does not parse: %v", err)
	}
	if spec.Scope != ScopeSupplyChain || spec.String() != "scope=supply-chain" {
		t.Errorf("scope=supply-chain read as %q and wrote %q", spec.Scope, spec.String())
	}
}

// Who uses a dependency is a set of repositories, and the supply chain is not one the uses surface
// names: refused by member, never answered as the owned repositories under the closure's name.
func TestTheUsesSurfaceRefusesTheSupplyChainByMember(t *testing.T) {
	if got := (Spec{Scope: ScopeSupplyChain}).UnenforceableAsUses(); !slices.Equal(got, []string{"scope=supply-chain"}) {
		t.Errorf("the uses surface refuses %v of the supply chain, want the member", got)
	}
	if got := (Spec{Scope: ScopeOwned}).UnenforceableAsUses(); len(got) != 0 {
		t.Errorf("the uses surface refuses %v of what you own", got)
	}
}

// A kept scope has no author's description, so it is described by what it asks: every term, by
// the label the filter controls show it under.
func TestADescriptionNamesEveryTermAPredicateCarries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "every-term.txt"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	described := spec.Description()
	for _, term := range Catalog() {
		if !strings.Contains(described, term.Label) {
			t.Errorf("the description leaves out %s (%s): %s", term.Name, term.Label, described)
		}
	}
}

// Each scope is described by what it holds, so the description of a kept scope asking the owned
// question cannot read as the closure.
func TestADescriptionSaysWhatEachScopeHolds(t *testing.T) {
	for scope, want := range map[string]string{
		ScopeOwned:       "what you own",
		ScopeSupplyChain: "what you own and what it runs",
		ScopeExposure:    "advisory routes",
	} {
		got := (Spec{Scope: scope}).Description()
		if !strings.Contains(got, want) {
			t.Errorf("scope=%s is described as %q, want it to say %q", scope, got, want)
		}
	}
	if got := (Spec{Scope: ScopeOwned}).Description(); strings.Contains(got, "runs") {
		t.Errorf("what you own is described as %q, which claims what it runs", got)
	}
}
