# Supply-chain classification

**Status:** Exact. Classification is the seed-reachable closure, recomputed over the whole graph.

## Why this exists

A dependency reaches an organization two ways, and they are different claims:

- **runtime** — something under review executes it. A composite action's own steps run inside
  whoever calls the action; a reusable workflow's steps run for whoever invokes it.
- **build_time** — it only ever runs in an upstream project's own CI.

Both are followed, because compromising a release pipeline is how consumers receive a malicious
version. Reporting build-time work as though this organization ran it would overstate the
exposure, so they are stored and displayed apart.

## Pending

1. **Two dependencies are never queued, and that is the depth bound rather than a gap.**
   `actionsdesk/lfs-warning` and `actionutils/sticky-comment` are named by repositories sitting at
   frontier depth 5, and `Enqueue` drops anything past `MaxDepth`. Raising the bound is the only
   thing that would change it, and it stays the operator's `--max-depth`: a crawl that deepens on
   its own can surprise an operator with a bill.

## The constraints that must not be broken

**Classification comes from `abom_edge.edge_kind`, which CodeQL already emits** —
`composite-step-uses-action` for a step inside a `runs:` block, `step-uses-action` for one inside a
workflow job. Never re-derive it from file paths; that reinvents the parser and gets it wrong.

**The repository behind a container is read from `abom_edge.repository_row_id`, never from the
node.** A node upsert only ever revises `container_node_row_id`, so a container first seen as
somebody else's dependency keeps `repository_row_id = NULL` — **0 of 1,290** external nodes carry
one. Keying "is this in a repository under review" off the node classifies nothing, silently.

**`Enqueue` may only ever raise a claim; only `Reclassify` may lower one.** Runtime wins on
conflict, so the order repositories happen to be analyzed in cannot decide how a dependency is
reported. `Reclassify` is sound precisely because its input is derived from the whole graph at
once and so cannot depend on that order — which is also why a tightened rule cannot reach an
existing corpus without it.

## Verification

- `TestASeedRepositorysOwnWorkflowMakesItsDependenciesRuntime` pins the base case that a local
  "is this referenced by anything" rule gets backwards: a repository under review runs its own CI
  workflow, nothing references that workflow, so `actions/checkout` reads as build-time.
- Classification and `abom_blast_radius` are derived by different code from different tables, so
  they can be cross-examined: every build-time row must have `dependent_repos = 0` and every
  runtime row `dependent_repos > 0`. A runtime claim alongside "no repository under review reaches
  this" is the contradiction to look for.
- Check that the discriminator can distinguish the thing being asked about before trusting what it
  says. Asking "does any *seed* repository observe an edge to this?" is definitionally false for
  every transitive dependency at depth ≥ 2, and it once declared 93 genuine runtime dependencies
  out of scope.
