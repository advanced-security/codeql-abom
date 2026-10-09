package store

import (
	"fmt"
	"sort"
	"strings"

	viewspec "github.com/advanced-security/codeql-abom/internal/view"
)

// PolicyOutcome is what a policy says about the corpus right now.
//
// Matched and Refused are separate on purpose. A waiver excuses a repository; it does not pretend
// the finding is not there, and folding the two would make an excused repository indistinguishable
// from a clean one — which is precisely what the audit answer exists to prevent.
type PolicyOutcome struct {
	Policy string `json:"policy"`
	// Matched is every repository in scope the predicate selected, waivers included.
	Matched []string `json:"matched"`
	// Refused is what the policy would stop: matched, minus the ones a waiver covers.
	Refused []string `json:"refused"`
	// Waived names the excused repositories with the waiver doing the excusing, so a reader can
	// see what is being let through and on whose authority.
	Waived []Waiver `json:"waived"`
	// Nodes and Findings are the evidence behind the match, counted once each.
	Nodes    int `json:"nodes"`
	Findings int `json:"findings"`
	// Unattributed counts findings the policy's rules select that no node carries, and which the
	// evaluation therefore cannot see. The gate cannot refuse what it cannot see; a reader told
	// what would be refused, and not told what was invisible, has a complete-looking answer that
	// is not one.
	Unattributed             int      `json:"unattributed"`
	UnattributedRepositories []string `json:"unattributedRepositories"`
	// Population is what the policy asks about: findings, advisories, or other policies.
	Population string `json:"population"`
	// Advisories counts matched live advisories once each. Absent for a policy that never asked.
	Advisories *int `json:"advisories,omitempty"`
	// Unreached counts live advisories at the severities asked that nothing under review reaches.
	// Nothing is refused for them, and a reader told only what was refused would read the rest of
	// GitHub's advisories as not applying.
	Unreached *int `json:"unreached,omitempty"`
	// Members is each named policy's own outcome, in the order the predicate writes them.
	Members []PolicyOutcome `json:"members,omitempty"`
}

// PopulationPolicies is a superset's population: whatever the policies it names ask about.
const PopulationPolicies = "policies"

// SelectorFor turns a predicate into the finding selector.
//
// One function, because a simulation and the gate that acts on it have to be the same evaluation.
// If they can disagree there are two policy engines and one of them is wrong, and nobody finds out
// until a pull request is refused for a reason the simulation never showed.
func SelectorFor(spec viewspec.Spec) FindingSelector {
	return FindingSelector{
		Rules:              spec.Rules,
		Exclude:            spec.NotRules,
		Tags:               spec.Tags,
		NotTags:            spec.NotTags,
		Provider:           spec.Provider,
		Severity:           spec.Severity,
		SecuritySeverities: spec.SecuritySeverities,
		Sources:            spec.Sources,
		Repositories:       spec.Repositories,
		Properties:         spec.Properties,
		ExcludeProperties:  spec.NotProperties,
		Mode:               spec.Mode,
		AdvisorySeverities: spec.AdvisorySeverities,
		Reached:            spec.Reached,
	}
}

// EvaluatePolicy reports what a policy matches and what it would refuse.
//
// A policy speaks for the repositories the operator owns, narrowed by its own predicate and by
// nothing else. `unitProperty` names the property a policy saved before the retirement was scoped
// by; it is read only to say what would migrate one.
func (s *Store) EvaluatePolicy(name, unitProperty string) (PolicyOutcome, error) {
	out, _, err := s.evaluatePolicy(name, unitProperty, map[string]bool{})
	return out, err
}

// evidence is what an outcome counted, by identity, so a superset counts its members' union once.
type evidence struct {
	nodes, advisories, unreached, unattributedRepos map[string]bool
	findings, unattributed                          map[int64]bool
	askedAdvisories                                 bool
}

func newEvidence() evidence {
	return evidence{
		nodes: map[string]bool{}, advisories: map[string]bool{}, unreached: map[string]bool{},
		unattributedRepos: map[string]bool{}, findings: map[int64]bool{},
		unattributed: map[int64]bool{},
	}
}

func (e evidence) merge(other evidence) evidence {
	for _, pair := range []struct{ into, from map[string]bool }{
		{e.nodes, other.nodes}, {e.advisories, other.advisories}, {e.unreached, other.unreached},
		{e.unattributedRepos, other.unattributedRepos},
	} {
		for k := range pair.from {
			pair.into[k] = true
		}
	}
	for _, pair := range []struct{ into, from map[int64]bool }{
		{e.findings, other.findings}, {e.unattributed, other.unattributed},
	} {
		for k := range pair.from {
			pair.into[k] = true
		}
	}
	e.askedAdvisories = e.askedAdvisories || other.askedAdvisories
	return e
}

func newOutcome(name string) PolicyOutcome {
	return PolicyOutcome{
		Policy: name, Matched: []string{}, Refused: []string{}, Waived: []Waiver{},
		UnattributedRepositories: []string{},
	}
}

// evaluatePolicy is EvaluatePolicy with the supersets being evaluated above it, so a policy that
// names one of them back is refused rather than followed forever.
func (s *Store) evaluatePolicy(
	name, unitProperty string, visiting map[string]bool,
) (PolicyOutcome, evidence, error) {
	out, ev := newOutcome(name), newEvidence()

	spec, err := s.policySpec(name, unitProperty)
	if err != nil {
		return out, ev, err
	}
	if len(spec.Policies) > 0 {
		visiting[name] = true
		defer delete(visiting, name)
		return s.evaluateSuperset(out, spec.Policies, unitProperty, visiting)
	}

	inScope, err := s.ownedRepositoryTest()
	if err != nil {
		return out, ev, err
	}

	sel := SelectorFor(spec)
	matched, err := s.NodesMatching(sel)
	if err != nil {
		return out, ev, err
	}
	out, ev, err = s.policyMatchOutcome(out, ev, matched, sel, inScope)
	if err != nil {
		return out, ev, err
	}
	if err := s.applyPolicyWaivers(name, &out); err != nil {
		return out, ev, err
	}

	ids, repos, err := s.unattributedFor(spec, inScope)
	if err != nil {
		return out, ev, err
	}
	addUnattributed(&out, &ev, ids, repos)
	return out, ev, nil
}

func (s *Store) policySpec(name, unitProperty string) (viewspec.Spec, error) {
	policy, ok, err := s.PolicyNamed(name)
	if err != nil {
		return viewspec.Spec{}, err
	}
	if !ok {
		return viewspec.Spec{}, fmt.Errorf("store: no policy named %q", name)
	}
	// Refused rather than evaluated without it: the scope is still what the policy says, and
	// answering for the whole organization would enforce it somewhere nobody scoped it to.
	if policy.scopeKind != "" && policy.scopeKind != policyScopeOrganization {
		return viewspec.Spec{}, fmt.Errorf(
			"store: policy %q is bound to %s %q by a retired scope nothing has written into its "+
				"predicate; %s", name, policy.scopeKind, policy.scopeValue,
			unmigrated(policy.scopeKind, unitProperty))
	}
	spec, err := viewspec.Parse(policy.Predicate)
	if err != nil {
		// Refuses nothing rather than everything: a gate whose policy failed to parse would
		// otherwise fail every pull request instead of none.
		return viewspec.Spec{}, fmt.Errorf("store: policy %q does not parse: %w", name, err)
	}
	// Checked again here, not only where it was saved: the gate reads this, and a policy stored
	// before the rule existed would otherwise enforce the wider half of its own text forever.
	if refused := spec.UnenforceableAsPolicy(); len(refused) > 0 {
		return viewspec.Spec{}, fmt.Errorf("store: policy %q carries %s, which it cannot enforce",
			name, strings.Join(refused, ", "))
	}
	if err := spec.SetConflict(); err != nil {
		return viewspec.Spec{}, fmt.Errorf("store: policy %q: %w", name, err)
	}
	return spec, nil
}

func (s *Store) policyMatchOutcome(
	out PolicyOutcome, ev evidence, matched FindingMatch, sel FindingSelector, inScope func(string) bool,
) (PolicyOutcome, evidence, error) {
	out.Population = matched.Population
	out.Nodes, out.Findings = len(matched.Nodes), matched.Findings
	for _, node := range matched.Nodes {
		ev.nodes[node.Key] = true
	}
	for _, id := range matched.findingIDs {
		ev.findings[id] = true
	}
	if matched.Population == PopulationAdvisories {
		ev.askedAdvisories = true
		for _, id := range matched.advisoryIDs {
			ev.advisories[id] = true
		}
		unreached, err := s.advisoriesReachingNothingIDs(sel.AdvisorySeverities)
		if err != nil {
			return out, ev, err
		}
		for _, id := range unreached {
			ev.unreached[id] = true
		}
		advisories, none := len(ev.advisories), len(ev.unreached)
		out.Advisories, out.Unreached = &advisories, &none
	}
	out.Matched = repositoriesMatchedByPolicy(matched.Nodes, inScope)
	return out, ev, nil
}

func repositoriesMatchedByPolicy(nodes []MatchedNode, inScope func(string) bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, node := range nodes {
		for _, repo := range node.Repositories {
			key := strings.ToLower(repo)
			if seen[key] || !inScope(key) {
				continue
			}
			seen[key] = true
			out = append(out, repo)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) applyPolicyWaivers(name string, out *PolicyOutcome) error {
	waived, err := s.WaivedRepositories(name)
	if err != nil {
		return err
	}
	for _, repo := range out.Matched {
		if w, excused := waived[strings.ToLower(repo)]; excused {
			out.Waived = append(out.Waived, w)
			continue
		}
		out.Refused = append(out.Refused, repo)
	}
	return nil
}

func addUnattributed(out *PolicyOutcome, ev *evidence, ids []int64, repos []string) {
	for _, id := range ids {
		ev.unattributed[id] = true
	}
	for _, repo := range repos {
		ev.unattributedRepos[repo] = true
	}
	out.Unattributed, out.UnattributedRepositories = len(ids), repos
}

// evaluateSuperset refuses what any named policy refuses.
//
// A member's waivers are part of what that member refuses, and the superset's own excuse a
// repository from the union; neither reaches past the policy it was granted against. The evidence
// is the members' union, counted once each, because two members can match one finding.
func (s *Store) evaluateSuperset(
	out PolicyOutcome, members []string, unitProperty string, visiting map[string]bool,
) (PolicyOutcome, evidence, error) {
	out.Population = PopulationPolicies
	acc, err := s.supersetMembers(out.Policy, members, unitProperty, visiting)
	if err != nil {
		return newOutcome(out.Policy), newEvidence(), err
	}
	out.Members = acc.members
	ev := acc.evidence

	own, err := s.WaivedRepositories(out.Policy)
	if err != nil {
		return out, ev, err
	}
	out.Matched = sortedMatched(acc.matched)
	sort.Strings(out.Matched)
	for _, repo := range out.Matched {
		key := strings.ToLower(repo)
		switch w, excused := own[key]; {
		case acc.refused[key] && excused:
			out.Waived = append(out.Waived, w)
		case acc.refused[key]:
			out.Refused = append(out.Refused, repo)
		default:
			out.Waived = append(out.Waived, acc.excusedBy[key])
		}
	}

	finishSupersetEvidence(&out, ev)
	return out, ev, nil
}

type supersetAccum struct {
	evidence  evidence
	members   []PolicyOutcome
	matched   map[string]string
	refused   map[string]bool
	excusedBy map[string]Waiver
}

func newSupersetAccum() supersetAccum {
	return supersetAccum{
		evidence:  newEvidence(),
		matched:   map[string]string{},
		refused:   map[string]bool{},
		excusedBy: map[string]Waiver{},
	}
}

func (s *Store) supersetMembers(
	policy string, members []string, unitProperty string, visiting map[string]bool,
) (supersetAccum, error) {
	acc := newSupersetAccum()
	for _, member := range members {
		if visiting[member] {
			return acc, fmt.Errorf(
				"store: policy %q names %q, which names it back, so neither has an answer",
				policy, member)
		}
		got, memberEvidence, err := s.evaluatePolicy(member, unitProperty, visiting)
		if err != nil {
			return acc, fmt.Errorf("store: policy %q names %q: %w", policy, member, err)
		}
		acc.add(got, memberEvidence)
	}
	return acc, nil
}

func (a *supersetAccum) add(got PolicyOutcome, ev evidence) {
	a.members = append(a.members, got)
	a.evidence = a.evidence.merge(ev)
	for _, repo := range got.Matched {
		a.matched[strings.ToLower(repo)] = repo
	}
	for _, repo := range got.Refused {
		a.refused[strings.ToLower(repo)] = true
	}
	for _, w := range got.Waived {
		if _, first := a.excusedBy[strings.ToLower(w.Repository)]; !first {
			a.excusedBy[strings.ToLower(w.Repository)] = w
		}
	}
}

func sortedMatched(matched map[string]string) []string {
	out := make([]string, 0, len(matched))
	for _, repo := range matched {
		out = append(out, repo)
	}
	return out
}

func finishSupersetEvidence(out *PolicyOutcome, ev evidence) {
	out.Nodes, out.Findings = len(ev.nodes), len(ev.findings)
	out.Unattributed = len(ev.unattributed)
	for repo := range ev.unattributedRepos {
		out.UnattributedRepositories = append(out.UnattributedRepositories, repo)
	}
	sort.Strings(out.UnattributedRepositories)
	if ev.askedAdvisories {
		advisories, none := len(ev.advisories), len(ev.unreached)
		out.Advisories, out.Unreached = &advisories, &none
	}
}

// unattributedFor lists the findings a policy's rules name that no node carries, and the owned
// repositories holding them.
//
// Queried against `abom_finding` rather than through `abom_finding_node`, because the whole point
// is to reach what the linked-node path cannot.
func (s *Store) unattributedFor(
	spec viewspec.Spec, inScope func(string) bool,
) ([]int64, []string, error) {
	if len(spec.Rules) == 0 && spec.Mode == "" {
		return nil, []string{}, nil
	}
	clause, args := "1", []any{}
	if len(spec.Rules) > 0 {
		clause, args = ruleClause("f.rule_id", spec.Rules)
	}
	rows, err := s.db.Query(`
SELECT r.repository_full_name, f.row_id
  FROM `+findingSource(spec.Mode)+` f
  JOIN repository r ON r.row_id = f.repository_row_id
 WHERE `+clause+`
   AND NOT EXISTS (SELECT 1 FROM abom_finding_node n WHERE n.finding_row_id = f.row_id)
 ORDER BY r.repository_full_name`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	ids := []int64{}
	repositories := []string{}
	for rows.Next() {
		var (
			full string
			id   int64
		)
		if err := rows.Scan(&full, &id); err != nil {
			return nil, nil, err
		}
		if !inScope(strings.ToLower(full)) {
			continue
		}
		ids = append(ids, id)
		if n := len(repositories); n == 0 || repositories[n-1] != full {
			repositories = append(repositories, full)
		}
	}
	return ids, repositories, rows.Err()
}

// ownedRepositoryTest answers whether a repository is one the operator owns, which is what every
// policy speaks for.
func (s *Store) ownedRepositoryTest() (func(string) bool, error) {
	owned, err := s.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	in := make(map[string]bool, len(owned))
	for _, r := range owned {
		in[strings.ToLower(r)] = true
	}
	return func(repo string) bool { return in[repo] }, nil
}

// unmigrated says what would write a retired scope into its policy's predicate.
func unmigrated(kind, unitProperty string) string {
	if kind == policyScopeUnit && strings.TrimSpace(unitProperty) == "" {
		return "start the server with --group-property naming the property it was scoped by"
	}
	return "its predicate already names repositories or values the scope excludes, " +
		"so it has to be rewritten by hand"
}

// SimulatePolicy evaluates a policy and records what it found, with nothing at stake.
func (s *Store) SimulatePolicy(name, unitProperty string) (PolicyOutcome, Simulation, error) {
	out, err := s.EvaluatePolicy(name, unitProperty)
	if err != nil {
		return out, Simulation{}, err
	}
	run := Simulation{
		Policy:       name,
		Nodes:        out.Nodes,
		Findings:     out.Findings,
		Repositories: len(out.Matched),
		Refused:      len(out.Refused),
		Waived:       len(out.Waived),
		Advisories:   out.Advisories,
	}
	id, err := s.RecordSimulation(run)
	if err != nil {
		return out, Simulation{}, err
	}
	run.ID = id
	return out, run, nil
}
