package store

// Finding providers. SARIF is the surface, and every document names the tool that wrote it in
// `runs[].tool.driver.name`. That value is stored verbatim: SARIF and code scanning already keep
// tool names unique within a repository, so it is an identity rather than a label to tidy up.
// Presentation folds spelling; the backend does not. See plans/db/finding-providers.md.
const (
	// ProviderCodeQL is the name CodeQL writes into its own SARIF.
	ProviderCodeQL = "CodeQL"
)

// reproducibleProviders is the whole of what "reproducible" means: given the recorded provenance,
// the same finding can be produced again. Absence is not a claim either way, which is why
// KnownProvider is a separate question.
var reproducibleProviders = map[string]bool{
	ProviderCodeQL: true,
}

// Reproducible reports whether a provider's findings can be produced again from what was recorded.
//
// False for a provider nobody registered, because a build that does not know a provider cannot
// vouch for it. Callers that need to tell "not reproducible" from "not asked" use KnownProvider.
func Reproducible(name string) bool { return reproducibleProviders[name] }

// KnownProvider reports whether this build recognizes the provider at all.
//
// The distinction matters where the plan says it does: *unknown* is a state, never a default. A
// finding nobody attributed and a finding from a provider that cannot be reproduced are different
// claims, and collapsing them turns "we did not ask" into "it cannot be done".
func KnownProvider(name string) bool {
	_, ok := reproducibleProviders[name]
	return ok
}
