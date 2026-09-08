# An edge says what boundary it crosses

**Status:** implemented. Independent of the ownership work — this is a structural property of the
edges already stored, true regardless of who is running ABOM. `api/bill-of-materials.md` needs it.

## What reconciling against the code corrected

- **Four edge kinds, not three.** The table below listed `step-uses-action`,
  `composite-step-uses-action` and `job-needs-job`. The corpus also has
  **`job-uses-reusable-workflow` (2,273)** — a job calling a reusable workflow, which is the single
  most boundary-interesting kind, since calling one in your own repository, a sibling's, or a
  stranger's are three different things.
- **The classification already existed — in the browser.** `ui/wwwroot/js/topology.js` has
  `edgeClass()`, returning exactly `sameRepo` / `sameOwner` / `crossOwner`, derived from node keys
  on every load, and the Explorer's filter panel already had checkboxes for all three. "Boundary
  classification: none" was true of the store and false of the product. So this was never about
  inventing a classification; it was about making one authoritative so the Explorer and the bill
  of materials cannot give different answers. `boundaryClass(stored, s, t)` now prefers the stored
  value and keeps the derivation only as the fallback for edges nobody observed — containment
  edges, and the merged edges a collapsed view builds.
- **"Written at ingest" was the wrong shape.** The `Edge` struct carries the *containing*
  repository and not the target's owner and repo, so the writer cannot classify without a lookup
  it does not have. It is a derived, idempotent pass instead, run from `RebuildAggregates`
  alongside the other derivations. Phases 2 and 3 collapse into one: filling the column and
  backfilling are the same statement.

## Why this exists

`abom_edge.repository_row_id` names the repository that **contains** the reference, not the one it
targets. Nothing in the store said whether a dependency stays inside one repository, crosses to a
sibling under the same account, or leaves for someone else's account entirely.

Those are three different pieces of work and three different risks. A workflow calling a reusable
workflow in its own repository is a refactor away from being inlined. The same call across two
repositories of one organization is a coordination problem inside a team. The same call to another
account is a supply-chain dependency on a third party. Drawing them identically is the same
conflation that once inflated blast radius from 202 to 209 by counting upstream repositories as
dependents.

A bill of materials has to state it, because "what is this composed of" is answered differently
depending on where the pieces come from.

## What is already true

| | |
| --- | --- |
| edge kinds | `step-uses-action` 39,594, `job-needs-job` 2,995, `job-uses-reusable-workflow` 2,273, `composite-step-uses-action` 1,402 |
| `job-needs-job` | ordering, not dependency — excluded by `store.DependencyEdgeFilter` |
| boundary classification | in the browser only, re-derived per load, unreadable by anything else |
| direct vs indirect | computed by recursive walks in `BumpImpactOf` and `BuildExposure`, never stored |

Both endpoints already carry what is needed: `abom_node` has `owner`, `repo` and
`repository_row_id`, and the edge has the containing repository. The classification was derivable
today and was derived nowhere durable.

## The design

A stored class on the edge, derived from its endpoints:

- **same repository** — source and target belong to one repository.
- **same owner** — different repositories, one account.
- **cross owner** — different accounts.

Deliberately *not* a fourth value for "owned versus not". That is a property of the operator's
declaration, changes when a declaration changes, and belongs to the bill of materials rather than
to the edge. The three above are properties of the world.

**Direct versus indirect stays out of this plan.** It is relative to a root — an action is direct
to the workflow that names it and indirect to the repository two hops up — so it is a property of
a walk, not of an edge. `api/bill-of-materials.md` computes it against whatever root the document
is for.

### The comparison is case-insensitive, and that is not a detail

GitHub treats account names case-insensitively and the corpus spells **7 accounts inconsistently**
— `1Password/check-signed-commits-action` and `1password/load-secrets-action` are one account. A
case-sensitive comparison misclassifies **211 edges**: 95 of a repository's own files reported as
third-party dependencies, and 116 siblings reported as strangers.

### Reading the target

The target repository is read from the node's **own** `owner`/`repo` first, and only then from
`repository_row_id`. A node upsert never revises `repository_row_id`, so a repository first seen
as somebody else's dependency has none — and 25,813 cross-owner edges point at exactly such nodes.
Reading the row id first would have left all of them unknown.

The two shapes coexist and the order handles both: internal nodes carry the full name in `repo`
with `owner` NULL (53,554 of them), external ones carry the two halves separately (1,280). No node
has both a non-null `owner` and a slash in `repo`, so the concatenation cannot mangle a name into
three parts — checked, not assumed.

### Backfill

The classification is derivable from rows already stored, so existing edges are filled by a pass
over them rather than by re-analysis. That is the whole reason to store it rather than join for
it: the join is three table lookups per edge on a read path the Explorer hits constantly.

Restricted to **live** edges, so a pruned corpus does not pay to classify the tombstoned two
thirds of itself.

## Phases

1. The classification as a property of two node rows, tested against a fixture holding one edge of
   every shape it has to tell apart.
2. The column, and the idempotent pass that fills it, called from `RebuildAggregates`.
3. Surfaced: the `boundary` query parameter and the field on every `GraphEdge`, the Explorer
   filter driven by the stored value rather than by a second derivation, and the field the bill of
   materials reads.

## Verification

- Counts by class on the real corpus, cross-checked against the same classification computed
  independently in SQL. Exact agreement.
- An **external action** — one nothing has analyzed, so it has no `repository_row_id` — classifies
  from its own `owner`/`repo` rather than falling into unknown.
- An edge whose target **names no repository at all** is unknown, and must not be reported as
  cross-owner. Absent must not read as a value. *(This corrects the original wording, which asked
  for cross-owner and conflated the two absences above.)*
- `job-needs-job` edges are classified but stay excluded from dependency traversals;
  `DependencyEdgeFilter` is untouched.
- The pass is idempotent: running it twice classifies nothing the second time.
- The read path the Explorer uses does not get slower — measured, since a new column on a hot
  table is the sort of thing that quietly costs a page.

## Open questions

- **Registries are a fourth kind of crossing, and the corpus has them.** The 81 edges left unknown
  are almost all `uses: docker://quay.io/cilium/...` — container images, which leave GitHub
  entirely rather than crossing an account boundary within it. Calling them cross-owner would
  report a GitHub dependency on something that is not one. They stay unknown until there is a
  value that means *left the forge*, which is the same shape as the host question below. Two of
  the 81 are an unexpanded `${{ }}` expression, which is unknown for a better reason still.
- **Enterprise and host boundaries.** `github.com` versus a GitHub Enterprise Server host is
  another kind of crossing, and `abom_account` already records a `host`. Not modelled here because
  the corpus has one host; the classification is shaped so a fourth value can be added without
  rewriting the first three.
- **Forks.** A fork of an upstream action is cross-owner by name and something else in spirit.
  ABOM has no fork relationship today and this plan does not invent one.
