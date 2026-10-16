package harness

import (
	"strings"
	"testing"
)

func TestAuthoringAndDraftingAskForAnonymizedReproductions(t *testing.T) {
	for name, text := range map[string]string{
		"authoring": rules(Bounds{workspace: "/pack"}, nil, "acme/query"),
		"drafting":  (drafter{query: "acme/query"}).rules(),
	} {
		for _, required := range []string{"anonymized", "behavior", "private production"} {
			if !strings.Contains(text, required) {
				t.Errorf("%s guidance does not mention %q", name, required)
			}
		}
	}
}
