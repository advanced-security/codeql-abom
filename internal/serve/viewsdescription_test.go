package serve

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// Every named scope says what it holds, wherever it is offered: a tooltip reading "Open Floating
// references on this page." says nothing the chip does not. A built-in says it in its own words; a
// kept scope by what its predicate asks, so the words cannot drift from the question.
func TestEveryNamedScopeSaysWhatItHolds(t *testing.T) {
	srv, db := unitServer(t)
	if err := db.SaveView(store.SavedView{
		Name: "crossings", Predicate: "boundary=cross-owner&scope=owned",
	}); err != nil {
		t.Fatalf("SaveView: %v", err)
	}

	saved := false
	for _, v := range namedViews(t, srv) {
		if strings.TrimSpace(v.Description) == "" {
			t.Errorf("%s is listed without a description", v.Name)
		}
		if b, ok := viewspec.BuiltinNamed(v.Name); ok && v.Builtin && v.Description != b.Description {
			t.Errorf("built-in %s is described as %q, want its own %q", v.Name, v.Description, b.Description)
		}
		if v.Name == "crossings" {
			saved = true
			for _, words := range []string{"Boundary", "cross-owner", "what you own"} {
				if !strings.Contains(v.Description, words) {
					t.Errorf("the kept scope is described as %q, which leaves out %q", v.Description, words)
				}
			}
		}
	}
	if !saved {
		t.Fatal("the kept scope is not in the catalog at all")
	}
}
