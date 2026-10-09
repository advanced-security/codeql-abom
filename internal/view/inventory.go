package view

// inventoryTerms are node-listing terms; relationship-only terms stay out of this unit.
// `repositories` keeps the set path's meaning: the repository a finding sits in.
var inventoryTerms = map[string]bool{
	paramSearch:           true,
	paramKinds:            true,
	paramOrigins:          true,
	paramMinReach:         true,
	paramScope:            true,
	paramFlags:            true,
	paramRefStyle:         true,
	paramRules:            true,
	paramNotRules:         true,
	paramTags:             true,
	paramNotTags:          true,
	paramProvider:         true,
	paramSource:           true,
	paramSeverity:         true,
	paramSecuritySeverity: true,
	paramRepos:            true,
	paramProperties:       true,
	paramNotProperties:    true,
	// A listing narrows by what was found on a node, and a finding has a shape.
	paramMode: true,
	// Dependencies some workflow names at exactly this reference.
	paramAt: true,
}

// UnenforceableAsInventory names terms a node listing cannot apply.
func (s Spec) UnenforceableAsInventory() []string {
	return s.carried(func(t string) bool { return inventoryTerms[t] })
}

// workflowTerms and jobTerms are consumer terms; dependency terms do not apply.
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

// partialMembers names list-term members a surface answers when it answers fewer than the term allows.
var partialMembers = map[string]map[string][]string{
	SurfaceWorkflows:    {paramFlags: {FlagFindings}},
	SurfaceJobs:         {paramFlags: {FlagFindings}},
	SurfaceRepositories: {paramFlags: {FlagFindings}},
	// Who uses a dependency is a set of repositories: owned names one, an advisory route does not,
	// and a repository is yours by the declaration or entered the record as a starting point or
	// upstream, never as external.
	SurfaceUses: {paramScope: {ScopeOwned}, paramOrigins: {"owned", "seed", "upstream"}},
}

// UnenforceableAsWorkflows names the terms the workflows unit cannot apply.
func (s Spec) UnenforceableAsWorkflows() []string {
	return append(s.carried(func(t string) bool { return workflowTerms[t] }),
		s.unanswered(SurfaceWorkflows)...)
}

// UnenforceableAsJobs names the terms the jobs unit cannot apply.
func (s Spec) UnenforceableAsJobs() []string {
	return append(s.carried(func(t string) bool { return jobTerms[t] }),
		s.unanswered(SurfaceJobs)...)
}

// repositoryTerms are the terms the repositories unit applies.
// `scope` selects repositories directly, unlike node-only terms such as kind or reach.
var repositoryTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramFlags:         true,
	paramProperties:    true,
	paramNotProperties: true,
	paramCodeScanning:  true,
}

// runRepositoryTerms choose a run's repositories; list-only findings flags are not a run population.
var runRepositoryTerms = map[string]bool{
	paramSearch:        true,
	paramScope:         true,
	paramProperties:    true,
	paramNotProperties: true,
}

// UnenforceableAsRepositories names the terms the repositories unit cannot apply.
func (s Spec) UnenforceableAsRepositories() []string {
	return append(s.carried(func(t string) bool { return repositoryTerms[t] }),
		s.unanswered(SurfaceRepositories)...)
}

// UnenforceableAsRun names terms a repository run cannot apply.
func (s Spec) UnenforceableAsRun() []string {
	return s.carried(appliedByRun)
}

func appliedByRun(t string) bool { return runRepositoryTerms[t] || setTerms[t] && !runRefuses[t] }

// runRefuses are set terms a run's population is not drawn through: a run covers repositories
// where findings sit, and neither an advisory nor a route is one.
var runRefuses = map[string]bool{paramAdvisorySeverity: true, paramReached: true}

// AsksRepositoriesUnit reports whether the predicate carries a term the repositories unit applies,
// which makes a run's population exactly what that unit lists for it.
func (s Spec) AsksRepositoriesUnit() bool {
	return len(s.carried(func(t string) bool { return !runRepositoryTerms[t] })) > 0
}

// advisoryTerms are the terms the advisories unit applies.
var advisoryTerms = map[string]bool{
	paramProperties:       true,
	paramNotProperties:    true,
	paramAdvisorySeverity: true,
	paramReached:          true,
}

// UnenforceableAsAdvisories names the terms the advisories unit cannot apply.
func (s Spec) UnenforceableAsAdvisories() []string {
	return s.carried(func(t string) bool { return advisoryTerms[t] })
}

// relationshipTerms are the terms the relationships unit applies.
// `rules` applies because these rows are the `uses:` lines findings sit on.
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

// usesTerms are what one dependency's details page applies to who uses it. The terms that chose
// which dependencies an inventory listed — kind, reach, findings on them — are refused by name
// there rather than dropped, so a reader arriving from a narrowed list is told what stopped.
var usesTerms = map[string]bool{
	paramAt:            true,
	paramRefStyle:      true,
	paramReached:       true,
	paramScope:         true,
	paramOrigins:       true,
	paramProperties:    true,
	paramNotProperties: true,
}

// UnenforceableAsUses names the terms a dependency's details page cannot apply.
func (s Spec) UnenforceableAsUses() []string {
	return append(s.carried(func(t string) bool { return usesTerms[t] }), s.unanswered(SurfaceUses)...)
}

// UnenforceableAsNode names the terms one node's own details cannot apply. What it holds and what it
// uses are read at a reference, and nothing else narrows them.
func (s Spec) UnenforceableAsNode() []string {
	return s.carried(func(t string) bool { return t == paramAt })
}

// UnenforceableAsRelationships names the terms the relationships unit cannot apply.
func (s Spec) UnenforceableAsRelationships() []string {
	return s.carried(func(t string) bool { return relationshipTerms[t] })
}
