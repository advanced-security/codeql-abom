package setup

import (
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func TestTheBodyAddsActionsAndRepeatsEverySettingItRead(t *testing.T) {
	body, err := Body(store.SetupClass{Class: store.SetupAdd, State: "configured",
		Languages:  []string{"javascript", "javascript-typescript", "typescript", "python"},
		QuerySuite: "default", ThreatModel: "remote", RunnerType: "labeled", RunnerLabel: "code-scanning"})
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !slices.Equal(body.Languages, []string{"actions", "javascript-typescript", "python"}) {
		t.Errorf("languages %v, want what was read, as the write names it, plus actions", body.Languages)
	}
	if body.State != "configured" || body.QuerySuite != "default" || body.ThreatModel != "remote" ||
		body.RunnerType != "labeled" || body.RunnerLabel != "code-scanning" {
		t.Errorf("body %+v changes a setting it read", body)
	}
	if missing := Removes([]string{"javascript", "javascript-typescript", "typescript", "python"}, body.Languages); len(missing) != 0 {
		t.Errorf("the body removes %v", missing)
	}
}

func TestAFreshEnablementIsActionsOnlyWithTheOperatorsSuiteAndThreatModel(t *testing.T) {
	body, err := Body(store.SetupClass{Class: store.SetupEnable, State: "not-configured",
		Languages: []string{"actions", "python"}})
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !slices.Equal(body.Languages, []string{"actions"}) || body.QuerySuite != "extended" ||
		body.ThreatModel != "remote_and_local" || body.RunnerType != "" {
		t.Errorf("fresh enablement body %+v", body)
	}
}

// A name the write does not document cannot be sent back unchanged, so the repository is held
// rather than having a language dropped or renamed on its behalf.
func TestALanguageTheWriteDoesNotNameHoldsTheRepository(t *testing.T) {
	_, err := Body(store.SetupClass{Class: store.SetupAdd, State: "configured", Languages: []string{"rust", "python"}})
	if err == nil || !strings.Contains(err.Error(), "rust") {
		t.Errorf("Body = %v, want a refusal naming rust", err)
	}
}

func TestOnlyAddAndEnableAreEverWritten(t *testing.T) {
	for _, class := range store.SetupClassOrder {
		_, err := Body(store.SetupClass{Class: class, State: "configured", Languages: []string{"python"}})
		if (err == nil) != (class == store.SetupAdd || class == store.SetupEnable) {
			t.Errorf("class %s: Body error %v", class, err)
		}
	}
}

func TestRemovesComparesAsTheWriteNamesLanguages(t *testing.T) {
	if got := Removes([]string{"javascript", "typescript", "javascript-typescript"}, []string{"javascript-typescript"}); len(got) != 0 {
		t.Errorf("javascript and typescript are javascript-typescript, got %v removed", got)
	}
	if got := Removes([]string{"python", "go"}, []string{"python", "actions"}); !slices.Equal(got, []string{"go"}) {
		t.Errorf("Removes = %v, want go", got)
	}
}
