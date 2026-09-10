package view

// Builtin is a named question that ships with the product.
//
// Code constants rather than rows, deliberately: they are part of what ABOM claims a supply chain
// review looks like, they have to exist on a database nobody has configured, and improving one
// should arrive in a release rather than being a migration against operator data.
type Builtin struct {
	Name      string `json:"name"`
	Title     string `json:"title"`
	Predicate string `json:"predicate"`
}

// Builtins are the four questions the interface already answers, named so an API caller and the
// pull request gate can ask them without reconstructing the predicate.
//
// Each predicate is written in the canonical form `Spec.String()` produces, and
// `TestEveryBuiltinIsAlreadyCanonical` holds them to it: a built-in that had to be normalized on
// read would compare unequal to the same question typed by hand.
var Builtins = []Builtin{
	{
		Name:  "owned-findings",
		Title: "What you own, with findings",
		// The arrival view. The closure alone is a readable population and an unreadable
		// picture; findings is the half that makes it one.
		Predicate: "flags=findings&scope=owned",
	},
	{
		Name:      "owned",
		Title:     "What you own",
		Predicate: "scope=owned",
	},
	{
		Name:      "advisory-routes",
		Title:     "Advisory routes",
		Predicate: "scope=exposure",
	},
	{
		Name:  "coverage-gaps",
		Title: "Unpinned and unanalyzed coverage gaps",
		// A floating reference nothing has analyzed is the pair that matters: either alone is
		// common and unremarkable.
		Predicate: "flags=unanalyzed&ref=tag",
	},
}

// BuiltinNamed reports the built-in of that name, if there is one.
func BuiltinNamed(name string) (Builtin, bool) {
	for _, b := range Builtins {
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
