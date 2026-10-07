package view

import "slices"

// Filter controls are derived from the same declarations that parse and refuse terms.

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
	// SurfaceDocument is the bill of materials, which narrows by grouping and nothing else.
	SurfaceDocument = "document"
	// SurfaceCoverage is the inventory's crawl queue view; filter terms do not narrow it.
	SurfaceCoverage = "coverage"
	// SurfaceRun is a run over repositories, built from a scope like any page.
	SurfaceRun = "run"
	// SurfacePolicy is a policy's predicate: the set path's terms, and other policies by name.
	SurfacePolicy = "policy"
	// SurfaceUses is one dependency's details page: who uses it, at which reference and commit.
	SurfaceUses = "uses"
)

// documentTerms are the filter terms a bill of materials applies.
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
	// SurfaceValues narrows Values on a surface that answers only some of them, so its control
	// offers what it answers rather than a member it refuses by name. Absent means all of them.
	SurfaceValues map[string][]string `json:"surfaceValues,omitempty"`
}

// catalogEntry is the display text that cannot be derived from term declarations.
// Help stays phrase-length because it is scanned in a dense filter list.
type catalogEntry struct {
	label      string
	help       string
	arity      string
	valuesFrom string
}

var catalogWords = map[string]catalogEntry{
	paramScope: {
		label: "Scope", arity: ArityScalar,
		help: "What you own, also what it runs, or advisory routes",
	},
	paramSearch: {
		label: "Name contains", arity: ArityScalar,
		help: "Repository, workflow or action",
	},
	paramKinds: {
		label: "Kind", arity: ArityList,
		help: "Workflow, job, step, action, image",
	},
	// Origin is how something entered the corpus, which is not the same question as who owns it,
	// so owned is the declaration's answer and a starting point you declared is both.
	paramOrigins: {
		label: "Origin", arity: ArityList,
		help: "Owned, starting point, upstream or external; what you own is a starting point too",
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
		label: "Repository Property", arity: ArityProperty, valuesFrom: "api/properties",
		help: "Keep a property value",
	},
	paramNotProperties: {
		label: "Not Repository Property", arity: ArityProperty, valuesFrom: "api/properties",
		help: "Drop a property value",
	},
	paramProvider: {
		label: "Provider", arity: ArityScalar, valuesFrom: "api/filters/provider/values",
		help: "Which analysis reported it",
	},
	// Whose rule reported it, which is a different question from which analysis did: every CodeQL
	// pack reports as CodeQL.
	paramSource: {
		label: "Source", arity: ArityList,
		help: "Upstream, ABOM's own, or custom rules",
	},
	paramSeverity: {
		label: "Severity", arity: ArityScalar, valuesFrom: "api/filters/severity/values",
		help: "As the analysis reported it",
	},
	// The rule's own score, banded as code scanning bands it. Unknown is a rule declaring none.
	paramSecuritySeverity: {
		label: "Security severity", arity: ArityList,
		help: "Critical, high, medium, low",
	},
	// What GitHub published, never whether a reference is inside the range. Asks about advisories
	// instead of findings.
	paramAdvisorySeverity: {
		label: "Advisory severity", arity: ArityList,
		help: "Live advisories, as GitHub rated them",
	},
	paramReached: {
		label: "Reached from yours", arity: ArityList,
		help: "Directly, or through a dependency",
	},
	paramPolicies: {
		label: "Any of these policies", arity: ArityList,
		help: "Refuses what any of them refuses",
	},
	// A reading's class, never a verdict about the code: covered says default setup analyzes it.
	paramCodeScanning: {
		label: "Actions code scanning", arity: ArityList,
		help: "What the setup reading says, from advanced to enable",
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
	// Verbatim, never resolved: `v4` is not `v4.2.2`, even where both name one commit today.
	paramAt: {
		label: "Reference", arity: ArityList,
		help: "Exactly as a workflow wrote it",
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
	SurfaceCoverage:      func(string) bool { return false },
	SurfaceDocument:      IsDocumentTerm,
	SurfaceRun:           appliedByRun,
	SurfacePolicy:        func(t string) bool { return policyTerms[t] },
	SurfaceUses:          func(t string) bool { return usesTerms[t] },
}

// surfaceOrder keeps the catalog stable between calls, so a control does not reorder itself and a
// response body can be compared.
var surfaceOrder = []string{
	SurfaceSet, SurfaceGraph, SurfaceInventory, SurfaceWorkflows,
	SurfaceJobs, SurfaceRepositories, SurfaceAdvisories, SurfaceRelationships,
	SurfaceDocument, SurfaceCoverage, SurfaceRun, SurfacePolicy, SurfaceUses,
}

// Refusals names, for every surface, the terms this predicate carries that the surface cannot
// apply — read from the declarations each surface enforces, so a page can disable a named scope it
// would refuse before it is chosen rather than after.
//
// Every surface is present, and one that applies the whole predicate holds an empty list: a page
// told nothing about itself cannot tell "applies all of it" from "nobody asked".
func (s Spec) Refusals() map[string][]string {
	out := make(map[string][]string, len(surfaceOrder))
	for _, surface := range surfaceOrder {
		out[surface] = append(s.carried(surfaceTerms[surface]), s.unanswered(surface)...)
	}
	return out
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
			if answered, partial := partialMembers[surface][name]; partial {
				if term.SurfaceValues == nil {
					term.SurfaceValues = map[string][]string{}
				}
				term.SurfaceValues[surface] = slices.Clone(answered)
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
	paramFlags, paramRules, paramNotRules, paramSource, paramSecuritySeverity, paramSeverity,
	paramAdvisorySeverity, paramReached, paramProvider, paramRepos,
	paramAt, paramRefStyle, paramMinReach, paramBoundary, paramEdgeClass, paramEdgeKinds,
	paramProperties, paramNotProperties, paramMode, paramSteps, paramPolicies, paramCodeScanning,
}
