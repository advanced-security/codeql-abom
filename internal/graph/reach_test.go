package graph

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// reachOf reads the details page's table for a node and keeps each repository's shortest route.
func reachOf(t *testing.T, s *store.Store, key string) map[string]Use {
	t.Helper()
	page := usesOf(t, s, key, UsesQuery{Limit: 10000})
	out := map[string]Use{}
	for _, u := range page.Items {
		if r, ok := out[u.Repository]; !ok || u.Hops < r.Hops {
			out[u.Repository] = u
		}
	}
	return out
}

// The question Dependabot cannot answer: not "do you depend on this package" but "does anything
// you run actually reach this step". A step runs when the workflow or action holding it is
// invoked, so reaching the step means reaching its container.
func TestAStepIsReachedByWhoeverInvokesItsContainer(t *testing.T) {
	s := fixture(t)

	got := reachOf(t, s, "shared-step")

	// shared-step sits in octo-org/shared's reusable workflow, which app and service both call.
	for _, repo := range []string{"octo-org/app", "octo-org/service"} {
		r, ok := got[repo]
		if !ok {
			t.Errorf("%s does not reach the step, but calls the workflow holding it", repo)
			continue
		}
		if r.Hops != 1 {
			t.Errorf("%s hops = %d, want 1 for calling the container directly", repo, r.Hops)
		}
	}
}

// The repository the step lives in runs it without depending on anything, so it is reached at
// no hops at all. Reporting it at one hop would say it calls itself.
func TestTheRepositoryHoldingAStepReachesItAtNoHops(t *testing.T) {
	s := fixture(t)

	got := reachOf(t, s, "shared-step")

	r, ok := got["octo-org/shared"]
	if !ok {
		t.Fatal("the repository holding the step was not reported as reaching it")
	}
	if r.Hops != 0 {
		t.Errorf("hops = %d, want 0: it does not depend on itself to run its own step", r.Hops)
	}
}

// A repository that reaches nothing of the sort must not appear. An over-broad answer here would
// be worse than none: it would send someone to fix a dependency they do not have.
func TestARepositoryThatDoesNotReachTheStepIsNotListed(t *testing.T) {
	s := fixture(t)

	got := reachOf(t, s, "lib-step")

	if _, ok := got["octo-org/service"]; ok {
		t.Error("octo-org/service was listed, but nothing it runs reaches octo-org/lib/greet")
	}
	if _, ok := got["octo-org/app"]; !ok {
		t.Error("octo-org/app was not listed, but it uses octo-org/lib/greet")
	}
}

// The route is the point. Knowing a repository is affected without knowing through what leaves
// the reader unable to tell whether the fix is theirs to make.
func TestReachCarriesTheRouteTaken(t *testing.T) {
	s := fixture(t)

	got := reachOf(t, s, "shared-step")

	r := got["octo-org/app"]
	if len(r.Path) == 0 {
		t.Fatal("no route was recorded")
	}
	var joined string
	for _, hop := range r.Path {
		joined += hop + " "
	}
	if !contains(r.Path, "octo-org/shared/.github/workflows/build.yml") {
		t.Errorf("route %q does not name the workflow the step sits in", joined)
	}
}

// Asking about something nothing runs is a legitimate question with an empty answer, not an
// error: "nobody reaches this" is exactly what a reader may be checking.
func TestAnUnreachedNodeAnswersEmptyRatherThanFailing(t *testing.T) {
	s := fixture(t)

	got := usesOf(t, s, "actions/checkout/nothing-uses-this", UsesQuery{})
	if len(got.Items) != 0 || got.Total != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// An action is reached directly by whoever names it, with no container indirection.
func TestAnActionIsReachedByItsUsers(t *testing.T) {
	s := fixture(t)

	got := reachOf(t, s, "actions/checkout")

	if r, ok := got["octo-org/tool"]; !ok || r.Hops != 1 {
		t.Errorf("octo-org/tool = %+v (present=%v), want a direct use at 1 hop", r, ok)
	}
	if _, ok := got["octo-org/app"]; !ok {
		t.Error("octo-org/app reaches actions/checkout through the shared workflow")
	}
}

func contains(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}
