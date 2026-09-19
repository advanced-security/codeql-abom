package store

import (
	"sort"
	"strings"
)

// OrgNodeKind is the account a dependency lives under. GitHub does not distinguish a user from
// an organization in a path, and neither does this.
const OrgNodeKind = "org"

// OrgKey is the node key for an account.
const orgKeyPrefix = "org:"

func OrgKey(owner string) string { return orgKeyPrefix + owner }

// attachOwners adds one node per account.
//
// Owners exist in the data only as a string column, so nothing could be asked across them:
// which accounts a corpus depends on, how far one account reaches, what dropping one would
// cost. Deriving the node here rather than storing it keeps it a view over the same facts.
//
// What an account holds is not decided here. It is read from abom_containment, so a filtered
// canvas draws a subset of what is stored rather than recomputing holdings against whichever
// nodes survived.
func attachOwners(nodes []GraphNode) []GraphNode {
	orgs := map[string]*GraphNode{}

	for i := range nodes {
		n := &nodes[i]
		owner := ownerOf(n)
		if owner == "" {
			continue
		}

		org, ok := orgs[owner]
		if !ok {
			org = &GraphNode{
				Key:   OrgKey(owner),
				Kind:  OrgNodeKind,
				Path:  owner,
				Owner: owner,
				Rules: []string{},
				// An account is external until a repository under review proves otherwise.
				IsExternal: true,
				Analyzed:   true,
				// An account is a grouping, not something anything depends on, so its reach is
				// not a pending measurement waiting to arrive.
				ReachKnown: true,
			}
			orgs[owner] = org
		}

		// Seed repositories are the corpus under review; an account holding one is in scope.
		if n.Kind == "repo" && n.Origin == OriginSeed {
			org.IsExternal = false
			org.Origin = OriginSeed
		}

		// Findings sit on workflows and steps, never on the repository node itself, so rolling
		// up only what hangs directly beneath an account would report every account as clean.
		org.Findings += n.Findings
		org.Rules = append(org.Rules, n.Rules...)
		// Reach is the furthest of its members rather than the sum, which would count a
		// repository once for every member that reaches it.
		if n.DependentRepos > org.DependentRepos {
			org.DependentRepos = n.DependentRepos
		}
		if !n.Analyzed {
			org.Analyzed = false
		}
	}

	for _, org := range orgs {
		org.Rules = dedupeSorted(org.Rules)
		nodes = append(nodes, *org)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })

	return nodes
}

// ownerOf reports the account a node belongs to, and empty when it belongs to none.
//
// Every node under a repository counts towards its account's totals, or an account would report
// no findings at all: findings are recorded against workflows and steps, never the repository.
func ownerOf(n *GraphNode) string {
	switch {
	case n.Kind == OrgNodeKind:
		return ""
	case n.Repository != "":
		owner, _, ok := strings.Cut(n.Repository, "/")
		if !ok {
			return ""
		}
		return owner
	case n.IsExternal:
		if n.Owner != "" {
			return n.Owner
		}
		// Docker images and anything else without a parsed owner keep their first segment.
		owner, _, ok := strings.Cut(n.Key, "/")
		if !ok {
			return ""
		}
		return owner
	default:
		return ""
	}
}

// holdsDirectly reports whether an account should draw an edge to this node.
//
// Only the top of each repository or external dependency hangs from the account. Wiring every
// workflow and step to it as well would bury the account in edges the containment tree already
// expresses, which is the same reason repository containment stops at what it directly holds.
func holdsDirectly(n *GraphNode, represented map[string]bool) bool {
	if n.Kind == "repo" {
		return true
	}
	if n.IsExternal && n.Container == "" && n.Repository == "" {
		return true
	}
	// A repository with no node of its own would otherwise leave its account unattached.
	return n.Repository != "" && n.Container == "" && !represented[n.Repository]
}

func dedupeSorted(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	sort.Strings(in)
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}
