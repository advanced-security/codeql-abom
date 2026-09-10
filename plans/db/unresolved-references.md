# `$/` is a self reference, not an expression ABOM could not resolve

**Status:** The parser is fixed and verified. The corpus cannot re-heal itself, because the expand
lane has no way to say that interpretation changed.

## Why this exists

`$/` is [GitHub's self repository
reference](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#jobsjob_idstepsuses):
it names an action stored in the same repository as the workflow or action currently running, and
resolves to that repository at the running commit. It carries no `@{ref}` — a ref suffix is invalid
— and it always resolves against the repository of the file it appears in, not the repository that
called it.

Splitting it on the first slash yields something shaped like `owner/repo`, and everything
downstream then treats it as one. That is how **73 relationships were classified `cross-owner`**,
so ABOM stated that the organization depended on a third-party account named `$` — the fabricated
verdict the first architectural invariant refuses.

A self reference is repository-relative, exactly like `./`. `$/x` and `./x` are one node, `$/` alone
collapses onto the repository node, and nothing is reported as unresolved because nothing is: a
resolved reference called a coverage gap is the same error as an unresolved one called a dependency.

## Pending

1. **The expand lane cannot be told that interpretation changed.** `expand.pendingWork` decides
   from `store.QueryRunExists` — repository, commit, query id, query version, pack — so a Go-side
   parser fix invalidates nothing and re-analysis of the same commit is *skipped*. Measured:
   `astral-sh/setup-uv` at `e105c8fb…` was requeued, resolved to the commit already on record and
   never re-ran, leaving its 17 pre-fix rows in place. The seed lane already solves this — `build
   --extractor-version` exists and `store.LedgerUnit.ExtractorVersion` participates in the unit key
   so that changing how results are interpreted re-runs the work — and the expand lane has no
   equivalent. **This decides how every future interpretation change reaches the corpus, not only
   this one.**
2. **The remaining `$`-owned nodes.** **63** edges across `Homebrew/actions`,
   `mongodb-labs/drivers-github-tools` and `zizmorcore/zizmor-action` still point at them, and will
   until those repositories are re-analyzed, which needs 1. The orphaned nodes are then ordinary
   prune candidates.

## The constraint that must not be broken

**A reference ABOM does not understand is not a dependency on an account.** A new syntax has to be
refused explicitly rather than fall through to the owner/repo split — that fall-through is what
turned a documented self reference into 73 third-party dependencies on an account named `$`.

**A genuine unevaluated expression is still refused a package.** `normalize.PackageURL` returns
`""` for an owner containing `.`, `/` or `$`, and `frontier.namesARepository` keeps it out of the
queue. CodeQL emits `${{ }}` verbatim through `bqrs decode`, so an expression that reaches the
graph reaches it intact and must stay unroutable.

## Verification

- Verify the world before touching the data. Three measurements settled this one: `gh api` returned
  the `uses:` line literally, a scratch actions database decoded `${{ }}` with the braces intact,
  and all 27 `$`-containing nodes in the corpus were `$/…`.
- The `AbomEdges` query test covers `$/.github/actions/greet` and a `$/…` reusable workflow call.
- The edge upsert clears `boundary` when re-analysis moves an edge's target, so a reclassification
  is visible rather than sticky.
- A re-analysis after an interpretation change actually re-runs. Today it does not, and the count
  of surviving `cross-owner` `$/` edges is the number that has to reach zero.
