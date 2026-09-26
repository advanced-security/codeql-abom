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
	// is not one. Measured on the reference corpus: `facebook/install-dotslash` holds 15 such
	// findings, which is the difference between 40 repositories and 41.
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

	policy, ok, err := s.PolicyNamed(name)
	if err != nil {
		return out, ev, err
	}
	if !ok {
		return out, ev, fmt.Errorf("store: no policy named %q", name)
	}
	// Refused rather than evaluated without it: the scope is still what the policy says, and
	// answering for the whole organization would enforce it somewhere nobody scoped it to.
	if policy.scopeKind != "" && policy.scopeKind != policyScopeOrganization {
		return out, ev, fmt.Errorf(
			"store: policy %q is bound to %s %q by a retired scope nothing has written into its "+
				"predicate; %s", name, policy.scopeKind, policy.scopeValue,
			unmigrated(policy.scopeKind, unitProperty))
	}
	spec, err := viewspec.Parse(policy.Predicate)
	if err != nil {
		// Refuses nothing rather than everything: a gate whose policy failed to parse would
		// otherwise fail every pull request instead of none.
		return out, ev, fmt.Errorf("store: policy %q does not parse: %w", name, err)
	}
	// Checked again here, not only where it was saved: the gate reads this, and a policy stored
	// before the rule existed would otherwise enforce the wider half of its own text forever.
	if refused := spec.UnenforceableAsPolicy(); len(refused) > 0 {
		return out, ev, fmt.Errorf("store: policy %q carries %s, which it cannot enforce",
			name, strings.Join(refused, ", "))
	}
	if err := spec.SetConflict(); err != nil {
		return out, ev, fmt.Errorf("store: policy %q: %w", name, err)
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

	seen := map[string]bool{}
	for _, node := range matched.Nodes {
		for _, repo := range node.Repositories {
			key := strings.ToLower(repo)
			if seen[key] || !inScope(key) {
				continue
			}
			seen[key] = true
			out.Matched = append(out.Matched, repo)
		}
	}
	sort.Strings(out.Matched)

	waived, err := s.WaivedRepositories(name)
	if err != nil {
		return out, ev, err
	}
	for _, repo := range out.Matched {
		if w, excused := waived[strings.ToLower(repo)]; excused {
			out.Waived = append(out.Waived, w)
			continue
		}
		out.Refused = append(out.Refused, repo)
	}

	ids, repos, err := s.unattributedFor(spec, inScope)
	if err != nil {
		return out, ev, err
	}
	for _, id := range ids {
		ev.unattributed[id] = true
	}
	for _, repo := range repos {
		ev.unattributedRepos[repo] = true
	}
	out.Unattributed, out.UnattributedRepositories = len(ids), repos
	return out, ev, nil
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
	ev := newEvidence()
	matched := map[string]string{}
	refused := map[string]bool{}
	excusedBy := map[string]Waiver{}
	for _, member := range members {
		if visiting[member] {
			return newOutcome(out.Policy), newEvidence(), fmt.Errorf(
				"store: policy %q names %q, which names it back, so neither has an answer",
				out.Policy, member)
		}
		got, memberEvidence, err := s.evaluatePolicy(member, unitProperty, visiting)
		if err != nil {
			return newOutcome(out.Policy), newEvidence(),
				fmt.Errorf("store: policy %q names %q: %w", out.Policy, member, err)
		}
		out.Members = append(out.Members, got)
		ev = ev.merge(memberEvidence)
		for _, repo := range got.Matched {
			matched[strings.ToLower(repo)] = repo
		}
		for _, repo := range got.Refused {
			refused[strings.ToLower(repo)] = true
		}
		for _, w := range got.Waived {
			if _, first := excusedBy[strings.ToLower(w.Repository)]; !first {
				excusedBy[strings.ToLower(w.Repository)] = w
			}
		}
	}

	own, err := s.WaivedRepositories(out.Policy)
	if err != nil {
		return out, ev, err
	}
	for _, repo := range matched {
		out.Matched = append(out.Matched, repo)
	}
	sort.Strings(out.Matched)
	for _, repo := range out.Matched {
		key := strings.ToLower(repo)
		switch w, excused := own[key]; {
		case refused[key] && excused:
			out.Waived = append(out.Waived, w)
		case refused[key]:
			out.Refused = append(out.Refused, repo)
		default:
			out.Waived = append(out.Waived, excusedBy[key])
		}
	}

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
	return out, ev, nil
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
