package serve

import (
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The Discovery grid previews five names and dead-ends at "and N more" for 291 of the corpus's
// name-and-version pairs. This is the route the rest of the list arrives on.
func TestNodeUsersNamesEveryRepositoryAtOneReference(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	users := decode[store.NodeUsers](t,
		get(t, srv, "/api/node/actions/checkout/-/users?ref=v4"))
	if users.Total != 1 || len(users.Repos) != 1 {
		t.Fatalf("users = %+v, want the one repository naming v4", users)
	}
	if users.Repos[0].FullName != "octo-org/app" {
		t.Errorf("repository = %q, want octo-org/app", users.Repos[0].FullName)
	}
}

// The reference is the unit. A route that ignored it would answer for the package and look
// right, because the package's list is a superset of every version's.
func TestNodeUsersIsScopedToTheReferenceAsked(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	users := decode[store.NodeUsers](t,
		get(t, srv, "/api/node/actions/checkout/-/users?ref=v3"))
	if users.Total != 0 {
		t.Errorf("v3 is used by nobody here, got %+v", users.Repos)
	}
}

// An omitted ref is the default branch, which is a real answer 1,253 queue rows give — not an
// instruction to ignore the reference.
func TestNodeUsersWithNoReferenceAsksAboutTheDefaultBranch(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	users := decode[store.NodeUsers](t, get(t, srv, "/api/node/actions/checkout/-/users"))
	if users.Ref != "" {
		t.Errorf("Ref = %q, want the empty default-branch reference", users.Ref)
	}
	if users.Total != 0 {
		t.Errorf("nothing names it without a reference, got %+v", users.Repos)
	}
}

// A node key contains slashes, and the sub-route is recognized by a suffix. A key that happens
// to end in something similar must not be mistaken for one.
func TestNodeUsersDoesNotSwallowAKeyThatIsNotASubRoute(t *testing.T) {
	srv, s := newServer(t)
	seedGraph(t, s)

	detail := decode[store.NodeDetail](t, get(t, srv, "/api/node/actions/checkout"))
	if detail.Key != "actions/checkout" {
		t.Errorf("Key = %q, want the node itself", detail.Key)
	}
}
