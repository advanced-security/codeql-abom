package events

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

var errBranch = errors.New("HTTP 502")

// orgRuleset is an organization ruleset event: the ruleset as GitHub sends it, with fields
// overridden, and on an edit what changed.
func orgRuleset(org, action string, ruleset, changes map[string]any) []byte {
	r := map[string]any{"id": 42, "name": "protect release", "target": "branch",
		"enforcement": "active", "source_type": "Organization", "source": org}
	for k, v := range ruleset {
		r[k] = v
	}
	payload := map[string]any{"action": action, "repository_ruleset": r,
		"organization": map[string]any{"login": org}, "sender": sender()}
	if changes != nil {
		payload["changes"] = changes
	}
	return encode(payload)
}

func byName(include, exclude []string) map[string]any {
	return map[string]any{"conditions": map[string]any{
		"ref_name": map[string]any{"include": []string{"~DEFAULT_BRANCH"}, "exclude": []string{}},
		"repository_name": map[string]any{
			"include": include, "exclude": exclude, "protected": false}}}
}

func byProperty(include, exclude []map[string]any) map[string]any {
	return map[string]any{"conditions": map[string]any{
		"ref_name":            map[string]any{"include": []string{"~ALL"}, "exclude": []string{}},
		"repository_property": map[string]any{"include": include, "exclude": exclude}}}
}

func property(name string, values ...string) map[string]any {
	return map[string]any{"name": name, "property_values": values, "source": "custom"}
}

// threeOwned is acme with service, api and docs, all three owned.
func threeOwned(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.owned("acme/api", "main", shaA)
	f.owned("acme/docs", "main", shaA)
	return f
}

// An organization ruleset names what it can apply to, so only those are re-read: one request each,
// where the whole listing it replaced read every repository's branches, setup and pages.
func TestAnOrganizationRulesetReReadsOnlyTheRepositoriesItCanApplyTo(t *testing.T) {
	f := threeOwned(t)
	f.branches["acme/service"] = []string{"release"}
	f.deliver("r-1", "repository_ruleset",
		orgRuleset("acme", "created", byName([]string{"SERV*", "api"}, []string{"api"}), nil))

	f.drain()

	d := f.settled("r-1", store.EventDone, ReasonBranchesRefreshed)
	if !slices.Equal(f.calls, []string{"branches acme/service"}) {
		t.Errorf("calls = %v, want acme/service read alone", f.calls)
	}
	if !strings.Contains(d.Detail, "1 of the 3") {
		t.Errorf("detail %q does not say how much of the organization it read", d.Detail)
	}
	if row := f.row("acme/service", "release"); row.Depth != 0 {
		t.Errorf("release row = %+v, want it live at depth 0", row)
	}
}

// Every repository is what ~ALL means, in the organization the ruleset belongs to.
func TestARulesetForEveryRepositoryReadsEachOnce(t *testing.T) {
	f := threeOwned(t)
	if err := f.store.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "acme", Kind: store.AccountOrganization, Enabled: true},
		{Owner: "other", Kind: store.AccountOrganization, Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	f.owned("other/thing", "main", shaA)
	f.deliver("r-1", "repository_ruleset",
		orgRuleset("acme", "deleted", byName([]string{"~ALL"}, nil), nil))

	f.drain()

	f.settled("r-1", store.EventDone, ReasonBranchesRefreshed)
	want := []string{"branches acme/api", "branches acme/docs", "branches acme/service"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
}

// Properties are judged from the values on record. A repository with none recorded may carry the
// property, so it is read too: a read too many costs a request, and one too few leaves what a
// repository protects wrong until the next listing.
func TestAnOrganizationRulesetByPropertyReadsWhatMayCarryIt(t *testing.T) {
	f := threeOwned(t)
	for repo, env := range map[string]string{"acme/service": "prod", "acme/api": "dev"} {
		if err := f.store.ReplacePropertyValues(repo, []store.PropertyValue{{Name: "env", Value: env}}); err != nil {
			t.Fatal(err)
		}
	}
	f.deliver("r-1", "repository_ruleset", orgRuleset("acme", "created",
		byProperty([]map[string]any{property("ENV", "prod")}, nil), nil))
	f.deliver("r-2", "repository_ruleset", orgRuleset("acme", "created",
		byProperty(nil, []map[string]any{property("env", "dev")}), nil))

	f.drain()

	f.settled("r-1", store.EventDone, ReasonBranchesRefreshed)
	f.settled("r-2", store.EventDone, ReasonBranchesRefreshed)
	want := []string{"branches acme/docs", "branches acme/service",
		"branches acme/docs", "branches acme/service"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want docs and service for each", f.calls)
	}
}

// What a ruleset applied to before an edit is not in the payload, and neither are repository ids
// ABOM never recorded, so each reads every owned repository of the organization: still one branch
// read each, and nothing else.
func TestARulesetWhoseTargetsAreNotKnownReadsEveryOwnedRepository(t *testing.T) {
	for name, payload := range map[string][]byte{
		"conditions changed": orgRuleset("acme", "edited", byName([]string{"api"}, nil),
			map[string]any{"conditions": map[string]any{"updated": []any{}}}),
		"by repository id": orgRuleset("acme", "created", map[string]any{"conditions": map[string]any{
			"repository_id": map[string]any{"repository_ids": []int{1, 2}}}}, nil),
		"no conditions": orgRuleset("acme", "created", nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			f := threeOwned(t)
			f.deliver("r-1", "repository_ruleset", payload)

			f.drain()

			d := f.settled("r-1", store.EventDone, ReasonBranchesRefreshed)
			want := []string{"branches acme/api", "branches acme/docs", "branches acme/service"}
			if !slices.Equal(f.calls, want) {
				t.Errorf("calls = %v, want %v", f.calls, want)
			}
			if !strings.Contains(d.Detail, "all 3 owned repositories, because") {
				t.Errorf("detail %q does not say why every repository was read", d.Detail)
			}
		})
	}
}

// A tag or push ruleset, or one switched off, protects no branch; switching one on or off does.
func TestARulesetThatProtectsNoBranchReadsNothing(t *testing.T) {
	for name, payload := range map[string][]byte{
		"tag":      orgRuleset("acme", "created", map[string]any{"target": "tag"}, nil),
		"push":     orgRuleset("acme", "deleted", map[string]any{"target": "push"}, nil),
		"disabled": orgRuleset("acme", "created", map[string]any{"enforcement": "disabled"}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			f := threeOwned(t)
			f.deliver("r-1", "repository_ruleset", payload)

			f.drain()

			if d := f.settled("r-1", store.EventIgnored, ReasonProtectsNoBranch); d.Detail == "" {
				t.Error("ignored without saying why")
			}
			if len(f.calls) != 0 {
				t.Errorf("calls = %v, want nothing read", f.calls)
			}
		})
	}

	t.Run("switched off", func(t *testing.T) {
		f := threeOwned(t)
		f.deliver("r-1", "repository_ruleset", orgRuleset("acme", "edited",
			map[string]any{"enforcement": "disabled", "conditions": byName([]string{"api"}, nil)["conditions"]},
			map[string]any{"enforcement": map[string]any{"from": "active"}}))

		f.drain()

		f.settled("r-1", store.EventDone, ReasonBranchesRefreshed)
		if !slices.Equal(f.calls, []string{"branches acme/api"}) {
			t.Errorf("calls = %v, want acme/api re-read", f.calls)
		}
	})
}

// A read that fails keeps that repository's recorded answer, and the delivery says so; the others
// still take theirs.
func TestAFailedReadAmongARulesetsTargetsKeepsItsAnswer(t *testing.T) {
	f := threeOwned(t)
	if _, err := f.store.SetProtectedBranches("acme/api", []string{"release"}); err != nil {
		t.Fatal(err)
	}
	f.branches["acme/service"] = []string{"hotfix"}
	f.branchErrs["acme/api"] = errBranch
	f.deliver("r-1", "repository_ruleset",
		orgRuleset("acme", "created", byName([]string{"api", "service"}, nil), nil))

	f.drain()

	d := f.settled("r-1", store.EventFailed, ReasonBranchReadFailed)
	if !strings.Contains(d.Detail, "acme/api") || !strings.Contains(d.Detail, "HTTP 502") {
		t.Errorf("detail %q does not name the failed read", d.Detail)
	}
	if _, found, _ := f.store.QueueRow("acme/api", "release"); !found {
		t.Error("a failed read removed a protected branch's row")
	}
	if row := f.row("acme/service", "hotfix"); row.Depth != 0 {
		t.Errorf("hotfix row = %+v, want the successful read applied", row)
	}
}

// A payload that does not carry the ruleset says nothing about what it applies to, so the
// organization is listed whole, as before.
func TestARulesetEventWithoutItsRulesetListsTheOrganizationWhole(t *testing.T) {
	f := threeOwned(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("r-1", "repository_ruleset", encode(map[string]any{"action": "created",
		"organization": map[string]any{"login": "acme"}, "sender": sender()}))

	drainWith(t, p)

	f.settled("r-1", store.EventDone, ReasonFullListingRequested)
	if !slices.Equal(a.calls, []string{"list whole acme"}) || len(f.calls) != 0 {
		t.Errorf("asked %v and read %v, want acme listed whole and nothing read", a.calls, f.calls)
	}
}

// A ruleset in a shape this build does not read is not a reason to lose the event.
func TestARulesetThisBuildCannotReadListsTheOrganizationWhole(t *testing.T) {
	f := threeOwned(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("r-1", "repository_ruleset",
		orgRuleset("acme", "created", map[string]any{"conditions": []int{1}}, nil))

	drainWith(t, p)

	f.settled("r-1", store.EventDone, ReasonFullListingRequested)
	if !slices.Equal(a.calls, []string{"list whole acme"}) || len(f.calls) != 0 {
		t.Errorf("asked %v and read %v, want acme listed whole and nothing read", a.calls, f.calls)
	}
}

// A ruleset that names nothing owned has nothing to re-read.
func TestARulesetNamingNothingOwnedReadsNothing(t *testing.T) {
	f := threeOwned(t)
	f.deliver("r-1", "repository_ruleset",
		orgRuleset("acme", "created", byName([]string{"somebody-elses"}, nil), nil))

	f.drain()

	if d := f.settled("r-1", store.EventIgnored, ReasonNotOwned); !strings.Contains(d.Detail, "none of the 3") {
		t.Errorf("detail %q does not say it applies to none of the owned repositories", d.Detail)
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %v, want nothing read", f.calls)
	}
}
