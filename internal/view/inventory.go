package view

// inventoryTerms are the terms a node listing applies.
//
// A listing is a set question asked of one table, so it narrows by what a node is, how far it
// reaches, and what was found on it. It cannot answer the graph's questions — a boundary and an
// edge class are properties of a relationship, and there are no relationships in a list.
//
// `repositories` takes the set path's meaning: the repository a *finding* sits in. "Which
// dependencies does this repository use" is a different question, answered by `scope` and by the
// graph, and one word meaning both would be the divergence this split exists to prevent.
var inventoryTerms = map[string]bool{
	paramSearch:        true,
	paramKinds:         true,
	paramOrigins:       true,
	paramMinReach:      true,
	paramScope:         true,
	paramFlags:         true,
	paramRefStyle:      true,
	paramRules:         true,
	paramNotRules:      true,
	paramProvider:      true,
	paramSeverity:      true,
	paramRepos:         true,
	paramProperties:    true,
	paramNotProperties: true,
	// A listing narrows by what was found on a node, and a finding has a shape.
	paramMode: true,
}

// UnenforceableAsInventory names the terms a predicate carries that a node listing cannot apply.
//
// Declared here beside the other two sides rather than in the endpoint, so adding a term to the
// language forces a decision about the listing instead of defaulting it to silently ignored.
func (s Spec) UnenforceableAsInventory() []string {
	return s.carried(func(t string) bool { return inventoryTerms[t] })
}

// workflowTerms and jobTerms are what the workflow and job units apply.
//
// Neither narrows by anything about being depended upon — reach, reference style and origin are
// questions about a dependency, and a workflow is a consumer. `flags=findings` is the one flag that
// means anything here, and the rest are refused by name like any other term this surface cannot
// answer.
var workflowTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramFlags:         true,
	paramProperties:    true,
	paramNotProperties: true,
}

var jobTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramFlags:         true,
	paramProperties:    true,
	paramNotProperties: true,
}

// UnenforceableAsWorkflows names the terms the workflows unit cannot apply.
func (s Spec) UnenforceableAsWorkflows() []string {
	return s.carried(func(t string) bool { return workflowTerms[t] })
}

// UnenforceableAsJobs names the terms the jobs unit cannot apply.
func (s Spec) UnenforceableAsJobs() []string {
	return s.carried(func(t string) bool { return jobTerms[t] })
}

// repositoryTerms are the terms the repositories unit applies.
//
// Far fewer than the node listing, because most of the language is about nodes: a repository has
// no kind, no reference style and no reach of its own. Refusing them by name is what tells a reader
// their scope is not narrowing this unit, rather than leaving them to assume it did.
//
// `scope` is here because a repository is exactly what it selects. Refusing it made the built-in
// "What you own" tab answer with an empty grid under a heading promising the repositories you own.
var repositoryTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramProperties:    true,
	paramNotProperties: true,
}

// UnenforceableAsRepositories names the terms the repositories unit cannot apply.
func (s Spec) UnenforceableAsRepositories() []string {
	return s.carried(func(t string) bool { return repositoryTerms[t] })
}

// UnenforceableAsRun names the terms a run cannot apply.
//
// A run covers repositories: the ones the repositories unit lists, the ones named outright, and
// where the set path's findings sit, in the shape `mode` names. Everything else describes a node
// or a relationship, and a run has neither to narrow.
func (s Spec) UnenforceableAsRun() []string {
	return s.carried(func(t string) bool { return repositoryTerms[t] || setTerms[t] })
}

// AsksRepositoriesUnit reports whether the predicate carries a term the repositories unit applies,
// which makes a run's population exactly what that unit lists for it.
func (s Spec) AsksRepositoriesUnit() bool {
	return len(s.carried(func(t string) bool { return !repositoryTerms[t] })) > 0
}

// advisoryTerms are the terms the advisories unit applies.
//
// A row is an advisory rather than a node, so the node half of the language says nothing about it.
// Even the search term is absent: it matches a node key, and an advisory has none.
var advisoryTerms = map[string]bool{
	paramProperties:    true,
	paramNotProperties: true,
}

// UnenforceableAsAdvisories names the terms the advisories unit cannot apply.
func (s Spec) UnenforceableAsAdvisories() []string {
	return s.carried(func(t string) bool { return advisoryTerms[t] })
}

// relationshipTerms are the terms the relationships unit applies.
//
// A row here is a `uses:` line rather than a node, so the node half of the language says nothing
// about it: a kind, a reach floor and a reference style are facts about an end, and an edge has
// two. What it does apply is what a relationship is — its kind, what it crosses, and how its
// reference is written — which is the half no other unit can answer.
//
// `rules` is here because a rule fires on a line, not on a node. Every `actions/unpinned-tag`
// finding sits on a `uses:`, and no unit lists the steps they sit on.
var relationshipTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramBoundary:      true,
	paramEdgeKinds:     true,
	paramRefStyle:      true,
	paramRules:         true,
	paramNotRules:      true,
	paramProperties:    true,
	paramNotProperties: true,
	// A row here is an edge, and an edge was observed by a run that states its shape.
	paramMode: true,
}

// UnenforceableAsRelationships names the terms the relationships unit cannot apply.
func (s Spec) UnenforceableAsRelationships() []string {
	return s.carried(func(t string) bool { return relationshipTerms[t] })
}
