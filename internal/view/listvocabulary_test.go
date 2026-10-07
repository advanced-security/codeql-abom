package view

import (
	"slices"
	"strings"
	"testing"
)

// A fixed list term refuses unknown members and refuses the whole term.
func TestAListMemberOutsideItsVocabularyRefusesTheWholeTerm(t *testing.T) {
	for text, allowed := range map[string]string{
		"kinds=banana":                  "composite_action",
		"kinds=action,banana":           "docker_image",
		"origins=yours":                 "owned", // the reader's word, where the term asks the declaration's
		"flags=banana":                  "unanalyzed",
		"flags=findings,pinned":         "advisory",
		"edges=cross-owner":             "crossOwner", // the store's spelling, where the browser's is asked
		"edgeKinds=uses":                "step-uses-action",
		"edgeKinds=job-needs-job,needs": "job-needs-job",
	} {
		_, err := Parse(text)
		if err == nil {
			t.Errorf("Parse(%q) was accepted, so it narrows by something nobody can have meant", text)
			continue
		}
		term, _, _ := strings.Cut(text, "=")
		if !strings.Contains(err.Error(), term) || !strings.Contains(err.Error(), allowed) {
			t.Errorf("Parse(%q) = %q, want the term and the members it takes named", text, err)
		}
	}
}

// Every member the graph and the listings actually produce still parses, or the refusal has eaten
// the language.
func TestEveryListMemberTheCorpusProducesIsStillAccepted(t *testing.T) {
	for _, text := range []string{
		"kinds=org,repo,workflow,reusable_workflow,composite_action,action,docker_image,job,step",
		"origins=owned,seed,upstream,external",
		"flags=findings,advisory,unanalyzed",
		"edges=sameRepo,sameOwner,crossOwner",
		"edgeKinds=step-uses-action,job-needs-job,job-uses-reusable-workflow,composite-step-uses-action",
	} {
		if _, err := Parse(text); err != nil {
			t.Errorf("Parse(%q): %v", text, err)
		}
	}
}

// The declaration a refusal is checked against is the list a control offers. A corpus-derived list
// is what has been used; this is what is allowed, and only the second can be refused against.
func TestAListTermOffersTheMembersItAllows(t *testing.T) {
	for _, name := range []string{paramKinds, paramOrigins, paramFlags, paramEdgeClass, paramEdgeKinds} {
		var term *Term
		for _, candidate := range Catalog() {
			if candidate.Name == name {
				term = &candidate
				break
			}
		}
		if term == nil {
			t.Fatalf("the catalog does not describe %q", name)
		}
		if len(term.Values) == 0 {
			t.Errorf("%q offers no members, so its control is a text box that refuses every typo", name)
		}
	}
}

// Units that answer only `findings` refuse the other flags by name.
func TestAUnitRefusesTheFlagsItCannotAnswer(t *testing.T) {
	spec, err := Parse("flags=advisory,findings,unanalyzed")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"flags=advisory", "flags=unanalyzed"}
	for surface, got := range map[string][]string{
		SurfaceWorkflows: spec.UnenforceableAsWorkflows(),
		SurfaceJobs:      spec.UnenforceableAsJobs(),
	} {
		if !slices.Equal(got, want) {
			t.Errorf("%s refuses %v, want %v", surface, got, want)
		}
		if refusals := spec.Refusals()[surface]; !slices.Equal(refusals, want) {
			t.Errorf("%s is said to refuse %v, want %v", surface, refusals, want)
		}
	}

	answered, err := Parse("flags=findings")
	if err != nil {
		t.Fatal(err)
	}
	if got := answered.UnenforceableAsWorkflows(); len(got) != 0 {
		t.Errorf("the workflows unit refuses %v of the one flag it answers", got)
	}
	if got := spec.UnenforceableAsInventory(); len(got) != 0 {
		t.Errorf("the dependencies listing refuses %v, and it answers every flag", got)
	}
}

// And the control on that unit offers only the member it answers, so a refusal is not the first
// thing a reader learns about the choice.
func TestTheCatalogOffersAUnitOnlyTheFlagsItAnswers(t *testing.T) {
	for _, term := range Catalog() {
		if term.Name != paramFlags {
			continue
		}
		for _, surface := range []string{SurfaceWorkflows, SurfaceJobs} {
			if got := term.SurfaceValues[surface]; !slices.Equal(got, []string{FlagFindings}) {
				t.Errorf("flags offers %v on %s, want only findings", got, surface)
			}
		}
		if _, narrowed := term.SurfaceValues[SurfaceInventory]; narrowed {
			t.Error("flags is narrowed on the dependencies listing, which answers all three")
		}
		return
	}
	t.Fatal("the catalog does not describe flags")
}

// The graph narrows by `unanalyzed`, a fact about the node, and hands `findings` and `advisory` to
// whoever draws it. It claimed all three, and `/api/graph/full?flags=findings` answered the whole
// scope to any caller that was not the Explorer, with nothing saying it had been handed the work.
func TestTheGraphNamesTheFlagsItHandsToTheClient(t *testing.T) {
	for text, want := range map[string][]string{
		"flags=findings&scope=owned": {"flags"},
		"flags=advisory":             {"flags"},
		"flags=findings,unanalyzed":  {"flags"},
		"flags=unanalyzed":           {},
	} {
		spec, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		if got := spec.ClientApplied(); !slices.Equal(got, want) {
			t.Errorf("%s: ClientApplied = %v, want %v", text, got, want)
		}
		if got := spec.UnenforceableAsGraph(); len(got) != 0 {
			t.Errorf("%s: the graph refuses %v, and it applies every flag on one side or the other",
				text, got)
		}
	}
}

// A finding's provider and severity are handed to whoever draws the graph, like its rule. The
// Explorer carried both in its address and filtered by neither, so a scope asking for errors drew
// warnings under a chip that said otherwise.
func TestTheGraphHandsAFindingsProviderAndSeverityToTheClient(t *testing.T) {
	spec, err := Parse("provider=CodeQL&severity=error&rules=actions/*")
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.ClientApplied(); !slices.Equal(got, []string{"provider", "rules", "severity"}) {
		t.Errorf("ClientApplied = %v, want provider, rules and severity", got)
	}
	if got := spec.UnenforceableAsGraph(); len(got) != 0 {
		t.Errorf("UnenforceableAsGraph = %v, want nothing", got)
	}
}
