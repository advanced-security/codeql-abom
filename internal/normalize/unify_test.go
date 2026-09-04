package normalize

import "testing"

// A reference to something defined in another repository must produce the same key as that
// repository's own definition of it. Without this, a shared action appears as two disconnected
// nodes and transitive blast radius is silently understated across the organization.
func TestExternalReferenceUnifiesWithItsDefinition(t *testing.T) {
	tests := []struct {
		name string
		// The `uses:` string as written by a consuming repository.
		ref string
		// How the defining repository's own file is keyed when that repository is ingested.
		defRepo string
		defPath string
	}{
		{
			name:    "reusable workflow",
			ref:     "octo-org/shared/.github/workflows/build.yml@v1",
			defRepo: "octo-org/shared",
			defPath: ".github/workflows/build.yml",
		},
		{
			name:    "composite action in a subdirectory",
			ref:     "octo-org/actions-lib/greet@v1",
			defRepo: "octo-org/actions-lib",
			defPath: "greet/action.yml",
		},
		{
			name:    "action at the repository root",
			ref:     "actions/checkout@v4",
			defRepo: "actions/checkout",
			defPath: "action.yml",
		},
		{
			name:    "action at the repository root declared as yaml",
			ref:     "actions/labeler",
			defRepo: "actions/labeler",
			defPath: "action.yaml",
		},
		{
			name:    "nested subpath action",
			ref:     "github/codeql-action/init@v3",
			defRepo: "github/codeql-action",
			defPath: "init/action.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer := ParseUses(tt.ref, "").NodeKeyIn("some-other/repo")

			var definition string
			if isWorkflowPath(tt.defPath) {
				definition = WorkflowKey(tt.defRepo, tt.defPath)
			} else {
				definition = CompositeActionKey(tt.defRepo, tt.defPath)
			}

			if consumer != definition {
				t.Errorf("reference key %q != definition key %q", consumer, definition)
			}
		})
	}
}

// isWorkflowPath mirrors how the ingester decides which container key to use.
func isWorkflowPath(path string) bool {
	return workflowPattern.MatchString(path) &&
		!(len(path) >= 10 && path[len(path)-10:] == "action.yml") &&
		!(len(path) >= 11 && path[len(path)-11:] == "action.yaml")
}

// Case differences between a reference and a definition must not split a node.
func TestKeyUnificationIsCaseInsensitive(t *testing.T) {
	ref := ParseUses("Octo-Org/Shared/.github/workflows/Build.yml@v1", "").NodeKeyIn("o/r")
	def := WorkflowKey("octo-org/shared", ".github/workflows/build.yml")
	if ref != def {
		t.Errorf("%q != %q", ref, def)
	}
}

// Local and external references to the same file must also collapse onto one node, so a
// repository that calls its own reusable workflow does not fork the graph.
func TestLocalAndExternalReferencesAgree(t *testing.T) {
	local := ParseUses("./.github/workflows/build.yml", "").NodeKeyIn("octo-org/shared")
	external := ParseUses("octo-org/shared/.github/workflows/build.yml@v1", "").NodeKeyIn("other/repo")
	if local != external {
		t.Errorf("local %q != external %q", local, external)
	}
}

// Sibling actions sharing a repository must stay distinct.
func TestUnificationDoesNotOvercollapse(t *testing.T) {
	init := ParseUses("github/codeql-action/init@v3", "").NodeKeyIn("o/r")
	analyze := ParseUses("github/codeql-action/analyze@v3", "").NodeKeyIn("o/r")
	if init == analyze {
		t.Errorf("sibling actions collapsed onto %q", init)
	}

	// A local action in one repository is not the same as a same-named one in another.
	a := ParseUses("./.github/actions/greet", "").NodeKeyIn("octo-org/a")
	b := ParseUses("./.github/actions/greet", "").NodeKeyIn("octo-org/b")
	if a == b {
		t.Errorf("local actions in different repositories collapsed onto %q", a)
	}
}
