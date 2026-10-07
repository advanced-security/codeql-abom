package events

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// ruleset is what an organization ruleset event says about where the ruleset applies.
type ruleset struct {
	Target      string `json:"target"`
	Enforcement string `json:"enforcement"`
	Conditions  *struct {
		RepositoryName *struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"repository_name"`
		RepositoryID       json.RawMessage `json:"repository_id"`
		RepositoryProperty *struct {
			Include []propertyTarget `json:"include"`
			Exclude []propertyTarget `json:"exclude"`
		} `json:"repository_property"`
	} `json:"conditions"`
}

type propertyTarget struct {
	Name   string   `json:"name"`
	Values []string `json:"property_values"`
	Source string   `json:"source"`
}

type rulesetEvent struct {
	actionEvent
	Ruleset *ruleset                   `json:"repository_ruleset"`
	Changes map[string]json.RawMessage `json:"changes"`
}

// organizationRuleset re-reads the protected branches of the owned repositories a ruleset can
// apply to. Only branch protection can move, so one branch read each replaces the whole listing,
// which also read every repository's setup and the listing's pages.
//
// The set is a superset of GitHub's whenever it cannot be exact: a read too many costs one request,
// and one too few leaves what a repository protects wrong until the next listing.
func (p *Processor) organizationRuleset(ctx context.Context, d store.EventDelivery, e rulesetEvent,
	org string) (outcome, error) {
	what := fmt.Sprintf("%s %s across organization %s", d.Event, e.Action, org)
	if e.Ruleset == nil || p.Branches == nil {
		return p.listAccountWhole(what, org)
	}
	declared, err := p.declaredAccount(org)
	if err != nil || !declared {
		return settle(store.EventIgnored, ReasonNotOwned, "%s, which is not a declared account", what), err
	}
	if !e.protectsBranches() {
		return settle(store.EventIgnored, ReasonProtectsNoBranch,
			"%s: a %s ruleset that is %s protects no branch", what, e.Ruleset.Target, e.Ruleset.Enforcement), nil
	}
	owned, err := p.ownedIn(org)
	if err != nil {
		return outcome{}, err
	}
	values, err := p.Store.PropertyValuesByRepository()
	if err != nil {
		return outcome{}, err
	}
	targets, unsure := e.targets(owned, values)
	if len(targets) == 0 {
		return settle(store.EventIgnored, ReasonNotOwned,
			"%s: it applies to none of the %d owned repositories", what, len(owned)), nil
	}
	return p.refreshTargets(ctx, what, targets, len(owned), unsure)
}

// protectsBranches reports whether the ruleset can move what a branch protects. A change to its
// target or enforcement can, whatever it is now.
func (e rulesetEvent) protectsBranches() bool {
	if _, ok := e.Changes["target"]; ok {
		return true
	}
	if _, ok := e.Changes["enforcement"]; ok {
		return true
	}
	if e.Ruleset.Target != "" && !strings.EqualFold(e.Ruleset.Target, "branch") {
		return false
	}
	return !strings.EqualFold(e.Ruleset.Enforcement, "disabled")
}

// targets is the owned repositories the ruleset can apply to, and why every one was taken when
// what it applies to cannot be told from the payload.
func (e rulesetEvent) targets(owned []string, values map[string][]store.PropertyValue) ([]string, string) {
	if _, ok := e.Changes["conditions"]; ok {
		return owned, "what it applied to before the edit is not in the event"
	}
	c := e.Ruleset.Conditions
	switch {
	case c == nil:
		return owned, "it names no condition"
	case len(c.RepositoryID) > 0 && string(c.RepositoryID) != "null":
		return owned, "it names repository ids, which ABOM does not record"
	}
	out := []string{}
	for _, full := range owned {
		_, name, _ := strings.Cut(full, "/")
		if c.RepositoryName != nil && !namedBy(name, c.RepositoryName.Include, c.RepositoryName.Exclude) {
			continue
		}
		if c.RepositoryProperty != nil &&
			!carries(values[strings.ToLower(full)], c.RepositoryProperty.Include, c.RepositoryProperty.Exclude) {
			continue
		}
		out = append(out, full)
	}
	return out, ""
}

// namedBy matches a repository name as GitHub's fnmatch patterns do, ignoring case. A pattern Go
// cannot parse includes rather than excludes.
func namedBy(name string, include, exclude []string) bool {
	matches := func(pattern string, malformed bool) bool {
		if pattern == "~ALL" {
			return true
		}
		ok, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
		if err != nil {
			return malformed
		}
		return ok
	}
	if !slices.ContainsFunc(include, func(p string) bool { return matches(p, true) }) {
		return false
	}
	return !slices.ContainsFunc(exclude, func(p string) bool { return matches(p, false) })
}

// carries judges a repository against property conditions from the values on record. Includes are
// taken as any and excludes as all, the widest reading of either, and a repository with no values
// recorded, or a property ABOM does not read, is taken to match an include and not an exclude.
func carries(recorded []store.PropertyValue, include, exclude []propertyTarget) bool {
	matches := func(t propertyTarget, unknown bool) bool {
		if len(recorded) == 0 || (t.Source != "" && !strings.EqualFold(t.Source, "custom")) {
			return unknown
		}
		return slices.ContainsFunc(recorded, func(v store.PropertyValue) bool {
			return strings.EqualFold(v.Name, t.Name) && slices.Contains(t.Values, v.Value)
		})
	}
	if len(include) > 0 && !slices.ContainsFunc(include, func(t propertyTarget) bool { return matches(t, true) }) {
		return false
	}
	return len(exclude) == 0 || !allOf(exclude, func(t propertyTarget) bool { return matches(t, false) })
}

func allOf[T any](items []T, ok func(T) bool) bool {
	for _, it := range items {
		if !ok(it) {
			return false
		}
	}
	return true
}

// ownedIn is the owned repositories of one organization, in name order.
func (p *Processor) ownedIn(org string) ([]string, error) {
	owned, err := p.Store.OwnedRepositories()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, full := range owned {
		if owner, _, _ := strings.Cut(full, "/"); strings.EqualFold(owner, org) {
			out = append(out, full)
		}
	}
	slices.Sort(out)
	return out, nil
}

// refreshTargets reads each target's protected branches. A read that fails records nothing for
// that repository, and the delivery fails so the reason is kept.
func (p *Processor) refreshTargets(ctx context.Context, what string, targets []string, owned int,
	unsure string) (outcome, error) {
	scope := fmt.Sprintf("%d of the %d owned repositories it can apply to", len(targets), owned)
	if unsure != "" {
		scope = fmt.Sprintf("all %d owned repositories, because %s", owned, unsure)
	}
	queued, removed := 0, 0
	var failed []string
	for _, repo := range targets {
		branches, err := p.Branches(ctx, repo)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", repo, err))
			continue
		}
		q, err := p.Store.SetProtectedBranches(repo, branches)
		if err != nil {
			return outcome{}, err
		}
		queued, removed = queued+q.Queued, removed+q.Removed
	}
	if queued > 0 {
		p.wake()
	}
	if len(failed) > 0 {
		return settle(store.EventFailed, ReasonBranchReadFailed,
			"%s: re-read %s; %d failed, whose recorded answers stand (%s); queued %d, removed %d",
			what, scope, len(failed), strings.Join(failed, "; "), queued, removed), nil
	}
	return settle(store.EventDone, ReasonBranchesRefreshed, "%s: re-read %s [%s]; queued %d, removed %d",
		what, scope, sample(targets, 10), queued, removed), nil
}

// sample names up to limit items and counts the rest, so a detail stays one readable line.
func sample(items []string, limit int) string {
	if len(items) <= limit {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(items[:limit], ", "), len(items)-limit)
}
