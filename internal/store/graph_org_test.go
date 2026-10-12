package store

import (
	"reflect"
	"testing"
)

func TestAccountlessNodesNeverCreateAccountsOrHoldings(t *testing.T) {
	nodes := []GraphNode{
		{Key: "docker://alpine:3.20", Kind: "docker_image", IsExternal: true},
		{Key: "docker://registry.local:5000/tools/build", Kind: "docker_image", IsExternal: true},
		{Key: "unrecognized/reference", Kind: "unknown", IsExternal: true},
		{Key: "$/action", Kind: "unknown", IsExternal: true},
	}
	before := append([]GraphNode(nil), nodes...)
	got := attachOwners(append([]GraphNode(nil), nodes...))
	if len(ownerNodes(got)) != 0 {
		t.Errorf("accountless nodes synthesized accounts: %+v", ownerNodes(got))
	}
	if held := containmentOf(nodes); len(held) != 0 {
		t.Errorf("accountless nodes acquired holdings: %+v", held)
	}
	for _, want := range before {
		found := false
		for _, n := range got {
			if n.Key == want.Key {
				found = true
				if !reflect.DeepEqual(n, want) {
					t.Errorf("ungrouping changed the node: got %+v, want %+v", n, want)
				}
			}
		}
		if !found {
			t.Errorf("ungrouping dropped %s", want.Key)
		}
	}
}

func TestAnImageDoesNotJoinTheRealDockerAccount(t *testing.T) {
	nodes := []GraphNode{
		{Key: "docker/build-push-action", Kind: "action", IsExternal: true, Owner: "docker"},
		{Key: "docker://docker", Kind: "docker_image", IsExternal: true},
	}
	orgs := ownerNodes(attachOwners(nodes))
	if len(orgs) != 1 || orgs["docker"].Key != "org:docker" {
		t.Errorf("accounts = %+v, want only the real docker account", orgs)
	}
	want := []Containment{{OwnerKey: "org:docker", HeldKey: "docker/build-push-action", Kind: EdgeKindOrgContains}}
	if got := containmentOf(nodes); !reflect.DeepEqual(got, want) {
		t.Errorf("holdings = %+v, want %+v", got, want)
	}
}

func TestContainmentCannotDrawAnAccountAbsentFromTheProjection(t *testing.T) {
	nodes := []GraphNode{{Key: "docker://alpine:3.20", Kind: "docker_image", IsExternal: true}}
	held := []Containment{{OwnerKey: "org:docker:", HeldKey: nodes[0].Key, Kind: EdgeKindOrgContains}}
	if edges := containmentEdges(nodes, held); len(edges) != 0 {
		t.Errorf("stale containment drew edges from an absent account: %+v", edges)
	}
}

func ownerNodes(nodes []GraphNode) map[string]GraphNode {
	out := map[string]GraphNode{}
	for _, n := range nodes {
		if n.Kind == OrgNodeKind {
			out[n.Path] = n
		}
	}
	return out
}

// The bug that made repositories unreachable was that nothing pointed at them. An account must
// never arrive in the same state.
func TestOwnerNodesAreNeverIslands(t *testing.T) {
	nodes := []GraphNode{
		{Key: "octo-org/app", Kind: "repo", Repository: "octo-org/app", Origin: OriginSeed},
		{Key: "actions/checkout", Kind: "action", IsExternal: true, Owner: "actions"},
	}

	got, held := attachOwners(nodes), containmentOf(nodes)

	for owner, org := range ownerNodes(got) {
		var attached int
		for _, c := range held {
			if c.OwnerKey == org.Key || c.HeldKey == org.Key {
				attached++
			}
		}
		if attached == 0 {
			t.Errorf("account %s has no edges", owner)
		}
	}
}

// An account holding a repository under review is in scope; one known only as a dependency is
// not. This is the account-level reading of seed versus upstream, and must not invent a third.
func TestOwnerInScopeWhenItHoldsASeedRepository(t *testing.T) {
	nodes := []GraphNode{
		{Key: "octo-org/app", Kind: "repo", Repository: "octo-org/app", Origin: OriginSeed},
		{Key: "actions/checkout", Kind: "action", IsExternal: true, Owner: "actions"},
	}

	got := attachOwners(nodes)
	orgs := ownerNodes(got)

	if orgs["octo-org"].IsExternal {
		t.Error("octo-org holds a repository under review but is marked external")
	}
	if !orgs["actions"].IsExternal {
		t.Error("actions is only ever a dependency but is marked in scope")
	}
}

// Reach is the furthest of an account's members. Summing would count one repository once per
// member that reaches it, inflating exactly the number the interface sizes nodes by.
func TestOwnerReachIsTheFurthestMemberNotTheSum(t *testing.T) {
	nodes := []GraphNode{
		{Key: "actions/checkout", Kind: "action", IsExternal: true, Owner: "actions",
			DependentRepos: 202},
		{Key: "actions/cache", Kind: "action", IsExternal: true, Owner: "actions",
			DependentRepos: 55},
	}

	got := attachOwners(nodes)

	if reach := ownerNodes(got)["actions"].DependentRepos; reach != 202 {
		t.Errorf("account reach is %d, want the furthest member's 202", reach)
	}
}

// Findings roll up, and the rule ids that produced them travel with the count. Losing the rules
// would leave an account carrying a verdict with nothing to attribute it to.
func TestOwnerRollupKeepsRuleAttribution(t *testing.T) {
	nodes := []GraphNode{
		{Key: "actions/checkout", Kind: "action", IsExternal: true, Owner: "actions",
			Findings: 3, Rules: []string{"actions/unpinned-tag"}},
		{Key: "actions/cache", Kind: "action", IsExternal: true, Owner: "actions",
			Findings: 2, Rules: []string{"actions/unpinned-tag", "actions/code-injection"}},
	}

	got := attachOwners(nodes)
	org := ownerNodes(got)["actions"]

	if org.Findings != 5 {
		t.Errorf("findings %d, want 5", org.Findings)
	}
	if len(org.Rules) != 2 {
		t.Errorf("rules %v, want the two distinct rule ids", org.Rules)
	}
}

// Findings sit on workflows, never on the repository node. Rolling up only what hangs directly
// beneath an account reported every account as clean.
func TestOwnerRollsUpFindingsFromBeneathTheRepository(t *testing.T) {
	nodes := []GraphNode{
		{Key: "octo-org/app", Kind: "repo", Repository: "octo-org/app", Origin: OriginSeed},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
			Repository: "octo-org/app", Origin: OriginSeed, Findings: 7,
			Rules: []string{"actions/unpinned-tag"}},
	}

	got := attachOwners(nodes)
	org := ownerNodes(got)["octo-org"]

	if org.Findings != 7 {
		t.Errorf("account findings %d, want the 7 recorded against its workflow", org.Findings)
	}
	if len(org.Rules) != 1 || org.Rules[0] != "actions/unpinned-tag" {
		t.Errorf("account rules %v, want the workflow's rule id", org.Rules)
	}
}

// A corpus need not contain a node standing for every repository. Where one is missing the
// account must still reach what it holds, or it renders as an island exactly as repositories
// once did.
func TestOwnerReachesRepositoriesWithNoNodeOfTheirOwn(t *testing.T) {
	nodes := []GraphNode{
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
			Repository: "octo-org/app", Origin: OriginSeed},
	}

	got, held := attachOwners(nodes), containmentOf(nodes)
	org := ownerNodes(got)["octo-org"]
	if org.Key == "" {
		t.Fatal("no account derived from the workflow's repository")
	}

	var attached int
	for _, c := range held {
		if c.OwnerKey == org.Key {
			attached++
		}
	}
	if attached == 0 {
		t.Error("account holds nothing; with no repository node the workflow should hang from it")
	}
}

// Attaching every workflow and step as well as the top of a repository would bury the account
// in edges that say nothing the containment tree does not already say.
func TestOwnerHoldsOnlyTheTopOfEachRepository(t *testing.T) {
	nodes := []GraphNode{
		{Key: "octo-org/app", Kind: "repo", Repository: "octo-org/app", Origin: OriginSeed},
		{Key: "octo-org/app/.github/workflows/ci.yml", Kind: "workflow",
			Repository: "octo-org/app", Origin: OriginSeed},
	}

	for _, c := range containmentOf(nodes) {
		if c.Kind == EdgeKindOrgContains && c.HeldKey != "octo-org/app" {
			t.Errorf("account holds %s directly; only the repository should hang beneath it",
				c.HeldKey)
		}
	}
}
