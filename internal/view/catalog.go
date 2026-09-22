package view

import "slices"

// What a filter control is built from.
//
// The browser asks for this rather than keeping its own list of terms, because two declarations
// drift: a term added to the language would never reach the control, and one removed from it would
// stay there offering a filter the server refuses by name. Everything here is derived from the
// same declarations that enforce it — the arity from how the spec writes the value, the values
// from `vocabulary`, the surfaces from the term maps each side already keeps.
//
// The words are here and not in the browser for the same reason. A term with no label reaches a
// reader as a query-string key, and `notProperties` is not a question anybody asked.

// The surfaces a term can be applied on. Named so a control can leave out what the surface it is
// drawn on would refuse, rather than offering it and producing a refusal on use.
const (
	SurfaceSet           = "set"
	SurfaceGraph         = "graph"
	SurfaceInventory     = "inventory"
	SurfaceWorkflows     = "workflows"
	SurfaceJobs          = "jobs"
	SurfaceRepositories  = "repositories"
	SurfaceAdvisories    = "advisories"
	SurfaceRelationships = "relationships"
	// SurfaceDocument is the bill of materials, which narrows by grouping and nothing else. It is
	// a surface like the rest so an export can be refused with the terms it cannot apply named,
	// rather than quietly dropping them and handing somebody the whole organization.
	SurfaceDocument = "document"
)

// documentTerms are the filter terms a bill of materials applies. Unexported and read through the
// two functions below: an exported map is writable by every package that imports it, and a
// declaration this file exists to be the only copy of must not be one an importer can edit.
var documentTerms = map[string]bool{
	paramProperties:    true,
	paramNotProperties: true,
}

// IsDocumentTerm reports whether a bill of materials narrows by this term. The endpoint refuses
// everything else by name, and must refuse exactly this set rather than a copy of it.
func IsDocumentTerm(name string) bool { return documentTerms[name] }

// DocumentTermNames lists those terms, sorted so a caller building a parameter set from them gets
// the same answer every time.
func DocumentTermNames() []string {
	names := make([]string, 0, len(documentTerms))
	for name := range documentTerms {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// How a term's value is written, which is what a control needs to know to collect one.
const (
	// ArityScalar is one value, replaced when another is chosen.
	ArityScalar = "scalar"
	// ArityList is several values, each added and removed on its own.
	ArityList = "list"
	// ArityProperty is a key and a value together, written once per pair.
	ArityProperty = "property"
	// ArityNumber is a whole number, and a floor rather than a match.
	ArityNumber = "number"
	// ArityFlag is on or absent. Absent is not "off": it is a question nobody asked.
	ArityFlag = "flag"
)

// Term is one filter a reader can apply, and everything a control needs to offer it.
type Term struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Arity string `json:"arity"`
	// Values is the closed list a term allows, when it has one. Absent means the values come from
	// the corpus rather than from the language, and `ValuesFrom` says where to read them.
	Values []string `json:"values,omitempty"`
	// ValuesFrom is the endpoint a control reads this term's values from, for terms whose values
	// are whatever the organization happens to contain. Empty means the value is typed.
	ValuesFrom string `json:"valuesFrom,omitempty"`
	// Surfaces are the sides that apply this term. A control on a surface not named here leaves
	// the term out rather than offering a filter that would be refused.
	Surfaces []string `json:"surfaces"`
}

// catalogEntry is the half of a term that cannot be derived: what to call it, and what to say
// about it. Everything else is read from the declarations that enforce the term.
//
// The help is a phrase rather than a sentence. It is read in a list of nineteen, where a reader is
// scanning for the one they want and not studying any of them: full sentences made the list wider
// than the table it filters and taller than the window. What a term means in detail belongs to the
// comments here, not to the control.
type catalogEntry struct {
	label      string
	help       string
	arity      string
	valuesFrom string
}

var catalogWords = map[string]catalogEntry{
	paramScope: {
		label: "Scope", arity: ArityScalar,
		help: "Owned accounts, or advisory routes",
	},
	paramSearch: {
		label: "Name contains", arity: ArityScalar,
		help: "Repository, workflow or action",
	},
	paramKinds: {
		label: "Kind", arity: ArityList,
		help: "Workflow, job, step, action, image",
	},
	// Origin is how something entered the corpus, which is not the same question as who owns it.
	paramOrigins: {
		label: "Origin", arity: ArityList,
		help: "Starting point, upstream or dependency",
	},
	paramRules: {
		label: "CodeQL rule", arity: ArityList, valuesFrom: "api/filters/rules/values",
		help: "Keep what a rule reported",
	},
	paramNotRules: {
		label: "Not CodeQL rule", arity: ArityList, valuesFrom: "api/filters/notRules/values",
		help: "Drop what a rule reported",
	},
	// The repository a finding sits in, never which repositories use a dependency — that is what
	// Scope and the graph answer, and conflating them reads as reach.
	paramRepos: {
		label: "Finding repository", arity: ArityList, valuesFrom: "api/repositories",
		help: "Where a finding sits",
	},
	paramProperties: {
		label: "Repository property", arity: ArityProperty, valuesFrom: "api/properties",
		help: "Keep a property value",
	},
	paramNotProperties: {
		label: "Not repository property", arity: ArityProperty, valuesFrom: "api/properties",
		help: "Drop a property value",
	},
	paramProvider: {
		label: "Provider", arity: ArityScalar, valuesFrom: "api/filters/provider/values",
		help: "Which analysis reported it",
	},
	paramSeverity: {
		label: "Severity", arity: ArityScalar, valuesFrom: "api/filters/severity/values",
		help: "As the analysis reported it",
	},
	// Absent is not a third shape: everything on record reads regular.
	paramMode: {
		label: "Analysis shape", arity: ArityScalar,
		help: "How the database was built",
	},
	paramEdgeClass: {
		label: "Relationship crosses", arity: ArityList,
		help: "Same repository, same account, or other",
	},
	paramEdgeKinds: {
		label: "Relationship kind", arity: ArityList,
		help: "Uses, needs, or calls",
	},
	// Lexical only: whether a reference is safe is a query's verdict, never this term's. A node
	// referenced both ways matches either.
	paramRefStyle: {
		label: "Reference style", arity: ArityScalar,
		help: "Commit SHA or tag",
	},
	paramBoundary: {
		label: "Boundary", arity: ArityScalar,
		help: "Which side of ownership",
	},
	// A floor, so zero is the question not being asked rather than a match on nothing.
	paramMinReach: {
		label: "Used by at least", arity: ArityNumber,
		help: "This many repositories or more",
	},
	paramFlags: {
		label: "Has", arity: ArityList,
		help: "Findings, advisory, or never analyzed",
	},
	// Changes which nodes exist, so it narrows rather than redraws.
	paramSteps: {
		label: "Individual steps", arity: ArityFlag,
		help: "Keep every uses: line",
	},
}

// surfaceTerms is each side's own declaration, read rather than restated. The graph goes through
// AppliedByExplorer because a term it hands to the browser is still a term it applies.
var surfaceTerms = map[string]func(string) bool{
	SurfaceSet:           func(t string) bool { return setTerms[t] },
	SurfaceGraph:         AppliedByExplorer,
	SurfaceInventory:     func(t string) bool { return inventoryTerms[t] },
	SurfaceWorkflows:     func(t string) bool { return workflowTerms[t] },
	SurfaceJobs:          func(t string) bool { return jobTerms[t] },
	SurfaceRepositories:  func(t string) bool { return repositoryTerms[t] },
	SurfaceAdvisories:    func(t string) bool { return advisoryTerms[t] },
	SurfaceRelationships: func(t string) bool { return relationshipTerms[t] },
	SurfaceDocument:      IsDocumentTerm,
}

// surfaceOrder keeps the catalog stable between calls, so a control does not reorder itself and a
// response body can be compared.
var surfaceOrder = []string{
	SurfaceSet, SurfaceGraph, SurfaceInventory, SurfaceWorkflows,
	SurfaceJobs, SurfaceRepositories, SurfaceAdvisories, SurfaceRelationships,
	SurfaceDocument,
}

// Catalog describes every term the language writes, in the order a control should offer them.
func Catalog() []Term {
	out := make([]Term, 0, len(catalogOrder))
	for _, name := range catalogOrder {
		words := catalogWords[name]
		term := Term{
			Name:       name,
			Label:      words.label,
			Help:       words.help,
			Arity:      words.arity,
			Values:     Vocabulary(name),
			ValuesFrom: words.valuesFrom,
		}
		for _, surface := range surfaceOrder {
			if surfaceTerms[surface](name) {
				term.Surfaces = append(term.Surfaces, surface)
			}
		}
		out = append(out, term)
	}
	return out
}

// catalogOrder is the order a reader meets the terms: what to look at, then what was found on it,
// then how it is referenced, then the shapes of the question itself.
var catalogOrder = []string{
	paramScope, paramSearch, paramKinds, paramOrigins,
	paramFlags, paramRules, paramNotRules, paramSeverity, paramProvider, paramRepos,
	paramRefStyle, paramMinReach, paramBoundary, paramEdgeClass, paramEdgeKinds,
	paramProperties, paramNotProperties, paramMode, paramSteps,
}
