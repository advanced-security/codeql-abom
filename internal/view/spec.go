// Package view names the predicate that decides which nodes and edges an operator is asking
// about, in a form three consumers can share: the Explorer's address bar, the graph API, and the
// pull request gate that fails on a non-empty match.
//
// The canonical form is a query string, so one text is simultaneously the shareable URL, the API
// parameters and the stored value. Nothing here executes the predicate; it only says what was
// asked for.
package view

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Spec is the portable half of a saved view. Presentation — how the match is drawn, what is
// selected, how far the neighborhood walk goes — is deliberately absent: a gate that carried a
// `use3d` field would have to ignore it, and a change to what is selected must not hide behind a
// change to how it is drawn.
type Spec struct {
	Scope   string
	Search  string
	Kinds   []string
	Origins []string
	Rules   []string
	// NotRules drops a node carrying any of these at all, rather than dropping the finding and
	// keeping the node: "A but not C" describes the action or workflow, not the result set.
	NotRules []string
	// Provider is the SARIF tool driver name, verbatim. It never matches a finding nobody
	// attributed.
	Provider string
	// Sources narrows to findings whose rule came from these kinds of pack, as the evaluation that
	// reported the finding recorded it. A finding whose evaluation recorded no source matches none.
	Sources  []string
	Severity string
	// SecuritySeverities narrows to findings whose rule's score falls in one of these bands. Apart
	// from Severity, which is the level a result reported: `error` spans critical and medium alike.
	SecuritySeverities []string
	// AdvisorySeverities asks about live advisories rather than findings, at the severities GitHub
	// published them. Every member is any live advisory.
	AdvisorySeverities []string
	// Reached asks about what an owned repository reaches rather than where a finding was found:
	// directly, by naming it, or transitively, through something else it uses.
	Reached []string
	// Policies names other policies, and a policy carrying it refuses what any of them refuses.
	Policies []string
	// Repositories narrows to what a caller is asking about — a pull request's repository, for the
	// gate. Compared case-insensitively wherever it is applied.
	Repositories []string
	// Properties narrows to what the organization says a repository is, by key *and* value. Several
	// keys are an AND; several values of one key are an OR, because a multi-select property matches
	// on any of its values and the store holds one row per value.
	//
	// The key travels with the value. Carrying the value alone meant the same text filtered by
	// whichever property each server was configured with, so one shared link asked two questions —
	// and a scope spanning two keys could not be written down at all.
	Properties []PropertyMatch
	// NotProperties drops an owned node where it is a *source*. A node an included property still
	// reaches stays, as a dependency: excluding one part of an organization must not sever the
	// supply chain of a part that was kept.
	NotProperties []PropertyMatch
	EdgeClass     []string
	// EdgeKinds narrows to what a relationship *is*: a step using an action, a job ordered after
	// another, a job calling a reusable workflow, a composite step using an action. Separate from
	// EdgeClass, which is what the relationship crosses — "every cross-owner `needs:`" needs both.
	EdgeKinds []string
	RefStyle  string
	Boundary  string
	// Mode narrows to how the database that produced a result was built. Absent is not a third
	// shape: every aggregate on record reads `regular`, so a spec that never mentions it asks
	// exactly what it asked before the term existed.
	Mode string
	// MinReach is a floor, and zero means the question was not asked rather than asked about zero.
	MinReach int
	Flags    []string
	// Steps keeps every individual `uses:` line rather than folding it into its container. It
	// changes which nodes exist — roughly 23k against 3.6k — so it is a predicate, not a drawing
	// choice.
	Steps bool
}

// The flags, named here so the browser, the API and the gate cannot spell them three ways.
const (
	// FlagFindings keeps what carries a finding.
	FlagFindings = "findings"
	// FlagAdvisory keeps what a published advisory names.
	FlagAdvisory = "advisory"
	// FlagUnanalyzed keeps only what has not been analyzed at every reference in use.
	FlagUnanalyzed = "unanalyzed"
)

// propertySeparator divides a key from a value. Only the first one divides, so a value may contain
// it; GitHub property names cannot.
const propertySeparator = ":"

// PropertyMatch is one custom property key and one value a repository must carry for it.
type PropertyMatch struct {
	Name  string
	Value string
}

func (p PropertyMatch) String() string { return p.Name + propertySeparator + p.Value }

// The parameter names are the text. Changing one changes every stored view and every shared link,
// so they are listed once here rather than spelled at each use.
const (
	paramScope            = "scope"
	paramSearch           = "q"
	paramKinds            = "kinds"
	paramOrigins          = "origins"
	paramRules            = "rules"
	paramNotRules         = "notRules"
	paramRepos            = "repositories"
	paramProperties       = "properties"
	paramNotProperties    = "notProperties"
	paramProvider         = "provider"
	paramSource           = "source"
	paramSeverity         = "severity"
	paramSecuritySeverity = "securitySeverity"
	paramAdvisorySeverity = "advisorySeverity"
	paramReached          = "reached"
	paramPolicies         = "policies"
	paramMode             = "mode"
	paramEdgeClass        = "edges"
	paramEdgeKinds        = "edgeKinds"
	paramRefStyle         = "ref"
	paramBoundary         = "boundary"
	paramMinReach         = "minReach"
	paramFlags            = "flags"
	paramSteps            = "steps"
)

// String writes the canonical text: keys in a fixed order, list values sorted and deduplicated,
// defaults omitted. Two specs asking the same question produce the same text, which is what lets
// it be a storage key and a thing two of can be compared.
func (s Spec) String() string {
	q := url.Values{}
	for _, f := range []struct {
		key   string
		value string
	}{
		{paramScope, s.Scope},
		{paramSearch, s.Search},
		{paramRefStyle, s.RefStyle},
		{paramBoundary, s.Boundary},
		{paramProvider, s.Provider},
		{paramSeverity, s.Severity},
		{paramMode, s.Mode},
	} {
		if f.value != "" {
			q.Set(f.key, f.value)
		}
	}
	for _, f := range []struct {
		key    string
		values []string
	}{
		{paramKinds, s.Kinds},
		{paramOrigins, s.Origins},
		{paramRules, s.Rules},
		{paramNotRules, s.NotRules},
		{paramRepos, s.Repositories},
		{paramEdgeClass, s.EdgeClass},
		{paramEdgeKinds, s.EdgeKinds},
		{paramFlags, s.Flags},
		{paramSource, s.Sources},
		{paramSecuritySeverity, s.SecuritySeverities},
		{paramAdvisorySeverity, s.AdvisorySeverities},
		{paramReached, s.Reached},
		{paramPolicies, s.Policies},
	} {
		if v := normalize(f.values); len(v) > 0 {
			q.Set(f.key, strings.Join(v, ","))
		}
	}
	// One parameter per match rather than a comma-joined list: a property value is operator text,
	// and a value containing a comma must not become two values. The store refuses to reproduce
	// that limitation in its schema, so the text does not reintroduce it.
	for _, f := range []struct {
		key     string
		matches []PropertyMatch
	}{
		{paramProperties, s.Properties},
		{paramNotProperties, s.NotProperties},
	} {
		for _, m := range normalizeMatches(f.matches) {
			q.Add(f.key, m.String())
		}
	}
	if s.MinReach > 0 {
		q.Set(paramMinReach, strconv.Itoa(s.MinReach))
	}
	if s.Steps {
		q.Set(paramSteps, "true")
	}
	return q.Encode()
}

// Parse reads the canonical text, refusing anything it does not understand.
//
// An unknown or empty parameter is an error rather than a value to skip. A predicate that quietly
// ignored a typo would match everything, and the gate consuming it fails a pull request on a
// non-empty match — so the silent reading is the dangerous one.
func Parse(text string) (Spec, error) {
	q, err := url.ParseQuery(strings.TrimPrefix(text, "?"))
	if err != nil {
		return Spec{}, fmt.Errorf("this filter cannot be read: %q: %w", text, err)
	}

	var s Spec
	scalars := map[string]*string{
		paramScope:    &s.Scope,
		paramSearch:   &s.Search,
		paramRefStyle: &s.RefStyle,
		paramBoundary: &s.Boundary,
		paramProvider: &s.Provider,
		paramSeverity: &s.Severity,
		paramMode:     &s.Mode,
	}
	lists := map[string]*[]string{
		paramKinds:            &s.Kinds,
		paramOrigins:          &s.Origins,
		paramRules:            &s.Rules,
		paramNotRules:         &s.NotRules,
		paramRepos:            &s.Repositories,
		paramEdgeClass:        &s.EdgeClass,
		paramEdgeKinds:        &s.EdgeKinds,
		paramFlags:            &s.Flags,
		paramSource:           &s.Sources,
		paramSecuritySeverity: &s.SecuritySeverities,
		paramAdvisorySeverity: &s.AdvisorySeverities,
		paramReached:          &s.Reached,
		paramPolicies:         &s.Policies,
	}
	matches := map[string]*[]PropertyMatch{
		paramProperties:    &s.Properties,
		paramNotProperties: &s.NotProperties,
	}

	for key, values := range q {
		if target, ok := matches[key]; ok {
			parsed, err := parseMatches(key, values)
			if err != nil {
				return Spec{}, err
			}
			*target = parsed
			continue
		}
		raw := strings.Join(values, ",")
		if raw == "" {
			return Spec{}, fmt.Errorf(
				"%q has no value; absent and empty are different questions", key)
		}
		switch {
		case scalars[key] != nil:
			if err := checkVocabulary(key, raw); err != nil {
				return Spec{}, err
			}
			*scalars[key] = raw
		case lists[key] != nil:
			members := normalize(strings.Split(raw, ","))
			for _, member := range members {
				if err := checkVocabulary(key, member); err != nil {
					return Spec{}, err
				}
			}
			*lists[key] = members
		case key == paramMinReach:
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return Spec{}, fmt.Errorf("%s must be a whole number, got %q", key, raw)
			}
			s.MinReach = n
		case key == paramSteps:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return Spec{}, fmt.Errorf("%s must be true or false, got %q", key, raw)
			}
			s.Steps = b
		default:
			return Spec{}, fmt.Errorf("ABOM has no filter called %q", key)
		}
	}
	return s, nil
}

// Equal reports whether two specs ask the same question, regardless of how either was built.
func (s Spec) Equal(other Spec) bool { return s.String() == other.String() }

// IsTerm reports whether a name is part of the filter language at all.
//
// The difference a reader needs: a term ABOM has and this answer does not apply is a question in
// the wrong place, and a name ABOM has never heard of is a typo. Refusing both the same way tells
// somebody to go looking for a surface that does not exist.
func IsTerm(name string) bool { return termNames[name] }

// The terms whose values are fixed in code rather than read from the corpus.
//
// Listed here because this package is below every consumer of the language: the store's own
// constants are checked against these rather than the other way round. A rule, a saved view and a
// shared link all parse through here, so this is the only place a value can be refused once.
//
// Deliberately not every term. `q`, `rules`, `repositories`, `provider`, `severity` and the
// property matches take values out of the data, where an unknown one is a question with no
// answer rather than a question the language cannot express.
const (
	ScopeOwned    = "owned"
	ScopeExposure = "exposure"

	BoundarySameRepository = "same-repository"
	BoundarySameOwner      = "same-owner"
	BoundaryCrossOwner     = "cross-owner"

	RefStyleAny = "any"
	RefStyleSHA = "sha"
	RefStyleTag = "tag"

	// How the database a result came from was built. `regular` is one repository's own checkout,
	// which is every database ABOM has built; `extended` is an owned repository plus its staged
	// closure, where the same rule id reports exposure across a `uses:` boundary instead of a
	// defect in code the repository holds. The store's constants are checked against these.
	ModeRegular  = "regular"
	ModeExtended = "extended"

	// Where the rule behind a result came from, read from the pack that supplied it: GitHub's
	// maintained packs, ABOM's own, the operator's, or several packs one invocation drew the same
	// rule id from. Never read from the rule id, whose prefix is a convention anybody can adopt.
	SourceUpstream = "upstream"
	SourceAbom     = "abom"
	SourceCustom   = "custom"
	SourceMixed    = "mixed"

	// What a relationship is. Four because the extraction produces four, and a reader filtering
	// by one is asking a question the aggregates already act on: `job-needs-job` is ordering
	// rather than use, and every dependency traversal excludes it.
	EdgeKindStepUsesAction          = "step-uses-action"
	EdgeKindJobNeedsJob             = "job-needs-job"
	EdgeKindJobUsesReusableWorkflow = "job-uses-reusable-workflow"
	EdgeKindCompositeStepUsesAction = "composite-step-uses-action"
)

// EdgeKinds is the vocabulary of `edgeKinds`, in the order a reader should be offered it: what a
// workflow does, then what it orders, then the two indirections.
//
// A list rather than a scalar, so "uses of any kind" is one question.
func EdgeKinds() []string {
	return []string{
		EdgeKindStepUsesAction, EdgeKindJobNeedsJob,
		EdgeKindJobUsesReusableWorkflow, EdgeKindCompositeStepUsesAction,
	}
}

var vocabulary = map[string][]string{
	paramScope:    {ScopeOwned, ScopeExposure},
	paramBoundary: {BoundarySameRepository, BoundarySameOwner, BoundaryCrossOwner},
	paramRefStyle: {RefStyleAny, RefStyleSHA, RefStyleTag},
	paramMode:     {ModeRegular, ModeExtended},
	// The lists are refused member by member, and one member outside refuses the whole term: a
	// member is a fact ABOM records or a structure its extraction produces, so one it cannot
	// produce is a question with no answer rather than one the corpus has not reached — and
	// keeping the members that were recognized answers a narrower question than was written.
	//
	// What has been *used* is never the check. `kinds` was read from the corpus, which is right
	// for offering and wrong for refusing: an account's node is drawn by the graph and stored
	// nowhere, so the corpus list lacked the one kind every grouping starts from.
	paramSource: {SourceUpstream, SourceAbom, SourceCustom, SourceMixed},
	// In the order a reader meets them: the containers, what a workflow uses, then what only a
	// focus draws.
	paramKinds: {
		"org", "repo", "workflow", "reusable_workflow", "composite_action", "action",
		"docker_image", "job", "step",
	},
	// How a node entered the corpus. `external` is a node belonging to no repository, which is
	// what the graph and the browser both call one.
	paramOrigins:          {"seed", "upstream", "external"},
	paramFlags:            {FlagFindings, FlagAdvisory, FlagUnanalyzed},
	paramSecuritySeverity: Bands(),
	paramAdvisorySeverity: AdvisorySeverities(),
	paramReached:          {ReachedDirect, ReachedTransitive},
	// The browser's spelling of what a relationship crosses, which is not `boundary`'s: that term
	// is the store's and says `cross-owner`. Two words for one fact is a debt; refusing the wrong
	// one by name is what stops it from filtering nothing.
	paramEdgeClass: {"sameRepo", "sameOwner", "crossOwner"},
	paramEdgeKinds: EdgeKinds(),
}

// Vocabulary reports the values a term allows, or nothing if the corpus decides them.
//
// Exported so a control can offer them rather than asking a reader to type one: the same
// declaration that refuses a wrong value is the one that says what the right ones are. A copy,
// because a caller that sorts or appends to what it was handed would otherwise change what the
// language accepts for every later caller.
func Vocabulary(term string) []string { return slices.Clone(vocabulary[term]) }

// checkVocabulary refuses a value the term does not have.
//
// Silently keeping one is the failure this exists to stop: `store.boundaryFilter` is right to
// return an empty fragment for a value it does not recognize, because a query string must never
// reach the statement — but that made an unrecognized boundary filter nothing at all, and
// `store.scopeFilter` falls through to the whole corpus the same way.
//
// The refusal names the alternatives because the near-misses are real: the browser draws
// `crossOwner` while the store spells it `cross-owner`, and version pinning says `commit` where
// the graph says `sha`.
func checkVocabulary(term, value string) error {
	allowed, ok := vocabulary[term]
	if !ok || slices.Contains(allowed, value) {
		return nil
	}
	return fmt.Errorf("%s does not take %q; it takes %s",
		term, value, strings.Join(allowed, ", "))
}

// AppliedByExplorer reports whether the graph applies a term, or hands it to whoever draws it.
//
// Which makes the Explorer the surface a refused term can be redirected to — and, for a term
// neither side applies, makes it honest to say that nothing here answers it.
func AppliedByExplorer(name string) bool { return graphTerms[name] || clientTerms[name] }

var termNames = map[string]bool{
	paramScope: true, paramSearch: true, paramKinds: true, paramOrigins: true,
	paramRules: true, paramNotRules: true, paramRepos: true, paramProperties: true,
	paramNotProperties: true, paramProvider: true, paramSeverity: true, paramEdgeClass: true,
	paramRefStyle: true, paramBoundary: true, paramMinReach: true, paramFlags: true,
	paramSteps: true, paramEdgeKinds: true, paramMode: true, paramSource: true,
	paramSecuritySeverity: true, paramAdvisorySeverity: true, paramReached: true, paramPolicies: true,
}

// setTerms are the terms the set path applies — `/api/match`, and a policy, which are one
// evaluation rather than two.
//
// Declared here rather than in each consumer because the consumers disagreed: the endpoint refused
// seven terms by name, applied seven, and dropped three without a word, while the policy engine
// refused nothing and dropped ten. A predicate accepted and partly dropped enforces something wider
// than the reader saw, and the widest version of it is the one that blocks a pull request.
var setTerms = map[string]bool{
	paramRules:            true,
	paramNotRules:         true,
	paramProvider:         true,
	paramSource:           true,
	paramSeverity:         true,
	paramSecuritySeverity: true,
	paramAdvisorySeverity: true,
	paramReached:          true,
	paramRepos:            true,
	paramProperties:       true,
	paramNotProperties:    true,
	paramMode:             true,
}

// UnenforceableAsSet names the terms a predicate carries that the set path cannot apply, sorted so
// two runs produce one message.
//
// Read from the canonical text rather than from the fields, so a term that can be written down is
// one this knows about: adding a parameter without deciding its side is then impossible rather than
// merely discouraged.
func (s Spec) UnenforceableAsSet() []string {
	return s.carried(func(t string) bool { return setTerms[t] })
}

// graphTerms are the terms the graph applies itself.
var graphTerms = map[string]bool{
	paramScope:         true,
	paramBoundary:      true,
	paramEdgeKinds:     true,
	paramOrigins:       true,
	paramMinReach:      true,
	paramRefStyle:      true,
	paramFlags:         true,
	paramSteps:         true,
	paramProperties:    true,
	paramNotProperties: true,
	paramMode:          true,
}

// clientTerms are the terms the graph hands to the browser rather than applying.
//
// Not a euphemism for dropped. Applied per node the finding terms sever 74% to 99% of their own
// match's edges, because the graph is bipartite by kind, so the Explorer takes one payload and
// filters it without asking again. A caller that is not the Explorer has to be told which terms it
// was handed, which is what `ClientApplied` is for.
//
// `provider`, `severity` and `securitySeverity` are applied per finding, never per node: the payload
// carries each node's distinct (rule, provider, severity, score) combinations, so
// `rules=A&severity=error` keeps a node only when one finding is both, exactly as the set path
// reads it.
var clientTerms = map[string]bool{
	paramKinds:            true,
	paramSearch:           true,
	paramRules:            true,
	paramNotRules:         true,
	paramProvider:         true,
	paramSeverity:         true,
	paramSecuritySeverity: true,
	paramEdgeClass:        true,
}

// clientMembers are the members of a term the graph applies that it hands to the browser anyway.
//
// `unanalyzed` is a fact about a node and the server narrows by it. `findings` and `advisory` are
// the finding terms' problem again — a workflow carries findings and the actions it uses mostly do
// not — so the browser keeps what a relationship needs and greys it, which a server that dropped
// nodes could not.
var clientMembers = map[string][]string{
	paramFlags: {FlagFindings, FlagAdvisory},
}

// ClientApplied names the terms a predicate carries that the graph leaves to whoever draws it,
// whole or in part.
func (s Spec) ClientApplied() []string {
	out := s.carried(func(t string) bool { return !clientTerms[t] })
	for term, handed := range clientMembers {
		if slices.ContainsFunc(s.members(term), func(m string) bool { return slices.Contains(handed, m) }) {
			out = append(out, term)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// UnenforceableAsGraph names the terms the graph neither applies nor hands to the client.
//
// `repositories` was in exactly this state: serialized into shareable links by the interface,
// applied by nothing, and answered with the whole corpus — 2,640 nodes with it and without it.
func (s Spec) UnenforceableAsGraph() []string {
	return s.carried(func(t string) bool { return graphTerms[t] || clientTerms[t] })
}

// carried names the terms this predicate holds that `keep` rejects, sorted.
func (s Spec) carried(keep func(string) bool) []string {
	held, err := url.ParseQuery(s.String())
	if err != nil {
		return nil
	}
	out := []string{}
	for key := range held {
		if !keep(key) {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// members are the values one list term carries, read from the canonical text for the same reason
// `carried` is.
func (s Spec) members(term string) []string {
	held, err := url.ParseQuery(s.String())
	if err != nil || held.Get(term) == "" {
		return nil
	}
	return strings.Split(held.Get(term), ",")
}

// unanswered names, as `term=member`, what a surface refuses of a list term it answers in part.
func (s Spec) unanswered(surface string) []string {
	out := []string{}
	for term, answered := range partialMembers[surface] {
		for _, m := range s.members(term) {
			if !slices.Contains(answered, m) {
				out = append(out, term+"="+m)
			}
		}
	}
	sort.Strings(out)
	return out
}

// parseMatches reads one `key:value` per parameter occurrence.
//
// Either half missing is refused rather than defaulted: a term carrying only a value would match
// against whichever property the server happened to be configured with, which is the silent
// widening this package exists to prevent.
func parseMatches(key string, values []string) ([]PropertyMatch, error) {
	out := make([]PropertyMatch, 0, len(values))
	for _, raw := range values {
		name, value, found := strings.Cut(raw, propertySeparator)
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !found || name == "" || value == "" {
			return nil, fmt.Errorf(
				"%s must be name%svalue, got %q", key, propertySeparator, raw)
		}
		out = append(out, PropertyMatch{Name: name, Value: value})
	}
	return normalizeMatches(out), nil
}

// normalizeMatches sorts and deduplicates, for the same reason normalize does.
func normalizeMatches(matches []PropertyMatch) []PropertyMatch {
	seen := map[PropertyMatch]bool{}
	out := make([]PropertyMatch, 0, len(matches))
	for _, m := range matches {
		if m.Name == "" || m.Value == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// normalize sorts, deduplicates and drops blanks, so the order somebody happened to click in
// cannot produce a second text for one predicate.
func normalize(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
