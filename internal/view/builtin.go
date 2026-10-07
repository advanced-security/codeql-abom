package view

import "slices"

// Builtin is a named question that ships with the product.
//
// Code constants rather than rows, deliberately: they are part of what ABOM claims a supply chain
// review looks like, they have to exist on a database nobody has configured, and improving one
// should arrive in a release rather than being a migration against operator data.
type Builtin struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Predicate   string `json:"predicate"`
	Description string `json:"description"`
}

// Builtins reports the questions the interface answers repeatedly, named so an API caller and the
// pull request gate can ask them without reconstructing the predicate, and so the interface can
// offer them as tabs rather than as pages that each invent their own scope.
//
// A copy, because `BuiltinNamed` resolves a saved policy's predicate from this list: an importer
// that edited what it was handed would change what an existing policy means, for every later
// caller, with nothing reporting it.
func Builtins() []Builtin { return slices.Clone(builtins) }

// Each predicate is written in the canonical form `Spec.String()` produces, and
// `TestEveryBuiltinIsAlreadyCanonical` holds them to it: a built-in that had to be normalized on
// read would compare unequal to the same question typed by hand — which is also what lets a
// surface tell whether the scope it is showing *is* one of these.
var builtins = []Builtin{
	{
		Name:      "owned-findings",
		Title:     "What you own, with findings",
		Predicate: "flags=findings&scope=owned",
		Description: "What the repositories you declared hold, where a CodeQL finding sits: their " +
			"workflows, jobs and actions. Nothing they only run from elsewhere.",
	},
	{
		Name:      "owned",
		Title:     "What you own",
		Predicate: "scope=owned",
		Description: "Only what the repositories you declared hold: the repositories, their " +
			"workflows, jobs and actions. Actions and workflows they run from elsewhere are not " +
			"yours, so they are left out.",
	},
	{
		Name:  "supply-chain-findings",
		Title: "Yours and what it runs, with findings",
		// The closure alone is a readable population and an unreadable picture; findings is the
		// half that makes it one.
		Predicate: "flags=findings&scope=supply-chain",
		Description: "What the repositories you declared hold and every action, workflow and image " +
			"they run, directly or through another action, where a CodeQL finding sits.",
	},
	{
		Name:      "supply-chain",
		Title:     "Yours and what it runs",
		Predicate: "scope=supply-chain",
		Description: "What the repositories you declared hold and every action, workflow and image " +
			"they run, directly or through another action. Another repository's own CI is left out.",
	},
	{
		Name:  "unpinned",
		Title: "Floating references",
		// Lexical: what is referenced by a tag or branch rather than a commit. Whether that is
		// safe is `actions/unpinned-tag`'s call, never this catalog's.
		Predicate: "ref=tag",
		Description: "What a workflow or action references by a tag or branch rather than a commit " +
			"SHA, so what it runs can change without an edit. Whether that is safe is a CodeQL " +
			"rule's call.",
	},
	{
		Name:      "advisory-routes",
		Title:     "Advisory routes",
		Predicate: "scope=exposure",
		Description: "Only the routes by which an analyzed repository reaches an action a published " +
			"advisory names, and what lies on them.",
	},
	{
		Name:  "exposure-findings",
		Title: "Advisory routes, with findings",
		// The picture the Explorer used to open on before arrival became a choice. The closure
		// alone draws more than the ceiling renders unasked; findings is the half that makes it
		// a picture rather than a refusal.
		Predicate: "flags=findings&scope=exposure",
		Description: "The routes by which an analyzed repository reaches an action a published " +
			"advisory names, where a CodeQL finding sits.",
	},
	{
		Name:  "coverage-gaps",
		Title: "Unpinned and unanalyzed coverage gaps",
		// A floating reference nothing has analyzed is the pair that matters: either alone is
		// common and unremarkable.
		Predicate: "flags=unanalyzed&ref=tag",
		Description: "What is referenced by a tag or branch and has never been analyzed: a " +
			"reference that can move, which nothing has read.",
	},
}

// BuiltinNamed reports the built-in of that name, if there is one.
func BuiltinNamed(name string) (Builtin, bool) {
	for _, b := range builtins {
		if b.Name == name {
			return b, true
		}
	}
	return Builtin{}, false
}

// IsBuiltin reports whether a name belongs to the product rather than to the operator.
func IsBuiltin(name string) bool {
	_, ok := BuiltinNamed(name)
	return ok
}
