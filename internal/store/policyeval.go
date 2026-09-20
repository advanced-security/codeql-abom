package store

import (
	"errors"
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
}

// SelectorFor turns a predicate into the finding selector.
//
// One function, because a simulation and the gate that acts on it have to be the same evaluation.
// If they can disagree there are two policy engines and one of them is wrong, and nobody finds out
// until a pull request is refused for a reason the simulation never showed.
func SelectorFor(spec viewspec.Spec) FindingSelector {
	return FindingSelector{
		Rules:             spec.Rules,
		Exclude:           spec.NotRules,
		Provider:          spec.Provider,
		Severity:          spec.Severity,
		Repositories:      spec.Repositories,
		Properties:        spec.Properties,
		ExcludeProperties: spec.NotProperties,
		Mode:              spec.Mode,
	}
}

// EvaluatePolicy reports what a policy matches and what it would refuse.
//
// `unitProperty` is the custom property carrying the dividing line, needed only by a unit-scoped
// policy. Empty is not a fallback to the whole organization: enforcing a rule somewhere nobody
// scoped it to is worse than refusing to answer.
func (s *Store) EvaluatePolicy(name, unitProperty string) (PolicyOutcome, error) {
	out := PolicyOutcome{
		Policy: name, Matched: []string{}, Refused: []string{}, Waived: []Waiver{},
		UnattributedRepositories: []string{},
	}

	policy, ok, err := s.PolicyNamed(name)
	if err != nil {
		return out, err
	}
	if !ok {
		return out, fmt.Errorf("store: no policy named %q", name)
	}
	spec, err := viewspec.Parse(policy.Predicate)
	if err != nil {
		// Refuses nothing rather than everything: a gate whose policy failed to parse would
		// otherwise fail every pull request instead of none.
		return out, fmt.Errorf("store: policy %q does not parse: %w", name, err)
	}
	// Checked again here, not only where it was saved: the gate reads this, and a policy stored
	// before the rule existed would otherwise enforce the wider half of its own text forever.
	if refused := spec.UnenforceableAsSet(); len(refused) > 0 {
		return out, fmt.Errorf("store: policy %q carries %s, which it cannot enforce",
			name, strings.Join(refused, ", "))
	}

	inScope, err := s.policyScope(policy, unitProperty)
	if err != nil {
		return out, err
	}

	matched, err := s.NodesMatching(SelectorFor(spec))
	if err != nil {
		return out, err
	}
	out.Nodes, out.Findings = len(matched.Nodes), matched.Findings

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
		return out, err
	}
	for _, repo := range out.Matched {
		if w, excused := waived[strings.ToLower(repo)]; excused {
			out.Waived = append(out.Waived, w)
			continue
		}
		out.Refused = append(out.Refused, repo)
	}

	out.Unattributed, out.UnattributedRepositories, err = s.unattributedFor(spec, inScope)
	if err != nil {
		return out, err
	}
	return out, nil
}

// unattributedFor counts the findings a policy's rules name that no node carries.
//
// Queried against `abom_finding` rather than through `abom_finding_node`, because the whole point
// is to reach what the linked-node path cannot.
func (s *Store) unattributedFor(
	spec viewspec.Spec, inScope func(string) bool,
) (int, []string, error) {
	if len(spec.Rules) == 0 {
		return 0, []string{}, nil
	}
	clause, args := ruleClause("f.rule_id", spec.Rules)
	rows, err := s.db.Query(`
SELECT r.repository_full_name, COUNT(*)
  FROM abom_finding_regular f
  JOIN repository r ON r.row_id = f.repository_row_id
 WHERE `+clause+`
   AND NOT EXISTS (SELECT 1 FROM abom_finding_node n WHERE n.finding_row_id = f.row_id)
 GROUP BY r.repository_full_name
 ORDER BY r.repository_full_name`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	total := 0
	repositories := []string{}
	for rows.Next() {
		var (
			full string
			n    int
		)
		if err := rows.Scan(&full, &n); err != nil {
			return 0, nil, err
		}
		if !inScope(strings.ToLower(full)) {
			continue
		}
		total += n
		repositories = append(repositories, full)
	}
	return total, repositories, rows.Err()
}

// policyScope answers which repositories a policy is bound to.
func (s *Store) policyScope(p Policy, unitProperty string) (func(string) bool, error) {
	switch p.ScopeKind {
	case PolicyScopeRepository:
		want := strings.ToLower(p.ScopeValue)
		return func(repo string) bool { return repo == want }, nil

	case PolicyScopeUnit:
		if strings.TrimSpace(unitProperty) == "" {
			return nil, errors.New(
				"store: a unit-scoped policy needs the property carrying the dividing line; " +
					"name one with --unit-property")
		}
		members, err := s.repositoriesWithValue(unitProperty, p.ScopeValue)
		if err != nil {
			return nil, err
		}
		return func(repo string) bool { return members[repo] }, nil

	default:
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
}

// repositoriesWithValue names the owned repositories carrying one property value.
func (s *Store) repositoriesWithValue(name, value string) (map[string]bool, error) {
	rows, err := s.db.Query(`
SELECT r.repository_full_name
  FROM repository r
  JOIN abom_repository_property p ON p.repository_full_name = r.repository_full_name
 WHERE p.property_name = ? AND p.value = ?
   AND `+ownedFullName("r.repository_full_name"), name, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var full string
		if err := rows.Scan(&full); err != nil {
			return nil, err
		}
		out[strings.ToLower(full)] = true
	}
	return out, rows.Err()
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
	}
	id, err := s.RecordSimulation(run)
	if err != nil {
		return out, Simulation{}, err
	}
	run.ID = id
	return out, run, nil
}
