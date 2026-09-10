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
	Severity string
	// Repositories narrows to what a caller is asking about — a pull request's repository, for the
	// gate. Compared case-insensitively wherever it is applied.
	Repositories []string
	EdgeClass    []string
	RefStyle     string
	Boundary     string
	// MinReach is a floor, and zero means the question was not asked rather than asked about zero.
	MinReach int
	Flags    []string
	// Steps keeps every individual `uses:` line rather than folding it into its container. It
	// changes which nodes exist — roughly 23k against 3.6k — so it is a predicate, not a drawing
	// choice.
	Steps bool
}

// FlagUnanalyzed keeps only what has not been analyzed at every reference in use. Named here so
// the browser, the API and the gate cannot spell it three ways.
const FlagUnanalyzed = "unanalyzed"

// The parameter names are the text. Changing one changes every stored view and every shared link,
// so they are listed once here rather than spelled at each use.
const (
	paramScope     = "scope"
	paramSearch    = "q"
	paramKinds     = "kinds"
	paramOrigins   = "origins"
	paramRules     = "rules"
	paramNotRules  = "notRules"
	paramRepos     = "repositories"
	paramProvider  = "provider"
	paramSeverity  = "severity"
	paramEdgeClass = "edges"
	paramRefStyle  = "ref"
	paramBoundary  = "boundary"
	paramMinReach  = "minReach"
	paramFlags     = "flags"
	paramSteps     = "steps"
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
		{paramFlags, s.Flags},
	} {
		if v := normalize(f.values); len(v) > 0 {
			q.Set(f.key, strings.Join(v, ","))
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
		return Spec{}, fmt.Errorf("view: unreadable filter %q: %w", text, err)
	}

	var s Spec
	scalars := map[string]*string{
		paramScope:    &s.Scope,
		paramSearch:   &s.Search,
		paramRefStyle: &s.RefStyle,
		paramBoundary: &s.Boundary,
		paramProvider: &s.Provider,
		paramSeverity: &s.Severity,
	}
	lists := map[string]*[]string{
		paramKinds:     &s.Kinds,
		paramOrigins:   &s.Origins,
		paramRules:     &s.Rules,
		paramNotRules:  &s.NotRules,
		paramRepos:     &s.Repositories,
		paramEdgeClass: &s.EdgeClass,
		paramFlags:     &s.Flags,
	}

	for key, values := range q {
		raw := strings.Join(values, ",")
		if raw == "" {
			return Spec{}, fmt.Errorf(
				"view: %q has no value; absent and empty are different questions", key)
		}
		switch {
		case scalars[key] != nil:
			*scalars[key] = raw
		case lists[key] != nil:
			*lists[key] = normalize(strings.Split(raw, ","))
		case key == paramMinReach:
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				return Spec{}, fmt.Errorf("view: %s must be a whole number, got %q", key, raw)
			}
			s.MinReach = n
		case key == paramSteps:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return Spec{}, fmt.Errorf("view: %s must be true or false, got %q", key, raw)
			}
			s.Steps = b
		default:
			return Spec{}, fmt.Errorf("view: unknown filter %q", key)
		}
	}
	return s, nil
}

// Equal reports whether two specs ask the same question, regardless of how either was built.
func (s Spec) Equal(other Spec) bool { return s.String() == other.String() }

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
