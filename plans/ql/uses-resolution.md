# Does CodeQL resolve a `uses:` across repositories, and at the right reference

**Status:** A measurement nobody has taken. Three plans rest on the answer and none of them is
worth starting before it is known.

## Why this exists

`codeql/actions-all` already models a `uses:` step as an interprocedural call. `Uses.getCallee()`
and `getCalleeNode()` exist, `CompositeAction.getACallerJob()` and `getACallerStep()` exist,
`ReusableWorkflow.getACaller()` exists, and there is a full `DataFlowCallable` / `ParameterNode` /
`ReturnNode` stack underneath. A composite action is a callable, its `inputs:` are parameters and
its `outputs:` are returns.

So taint crosses the `uses:` boundary **when the callee's source sits in the same database**. Today
it never does: ABOM builds one database per `(repository, reference)` from a sparse checkout, so
`actions/checkout@v4` is analyzed alone and every `uses:` leaving it resolves to nothing.

That bounds what ABOM can say. `uses: ${{ env.ACTION_REF }}` is recorded verbatim and never
resolved, which is [`db/unresolved-references.md`](../db/unresolved-references.md)'s whole subject.
The interesting case is worse than untidy: **a `uses:` built from a remote source is an attack**,
and nothing here can see one.

The obvious move is one database over every checkout. Before building that, two things have to be
true, and neither is documented.

## Pending

1. **Does `getCallee` resolve to a sibling directory at all?** Build one database over a tree
   holding two repositories, where A's workflow names B's composite action. Assert the callee
   resolves and that taint crosses from A's job into B's `run:` step. `CompositeActionImpl` and
   `ReusableWorkflowImpl` both expose `getResolvedPath`, and `Helper` exposes `getRepoRoot`,
   `normalizePath` and `isSubpath` — a set of names that reads as local `./…` resolution and may
   never have been asked to cross a repository. Depends on nothing.
2. **Can it be pointed at the right reference?** This is the half that decides the shape of
   everything downstream. A tree must hold `actions/checkout` at `v4` **and** at `v5`, because
   telling one reference from another is what ABOM is for, and CodeQL has no reason to read a
   directory named `checkout@v4` as a reference. A resolver that lands on whichever copy it finds
   first is worse than one that resolves nothing, because it answers confidently and wrongly.
   Depends on 1.
3. **If not, does a data extension do it?** `Config` and `ConfigExtensions` already carry
   `repositoryDataModel`, `immutableActionsDataModel` and `trustedActionsOwnerDataModel`, so
   teaching the resolver is a supported operation. Establish whether the callee relation is
   reachable that way, and at what cost per row. Depends on 2 concluding that it cannot.

## The constraints that must not be broken

**A fork of the Actions library is not an answer.** If the relation cannot be taught through a data
extension, that is the finding, and it is reported rather than worked around. A forked library
drifts from the one that produces every finding ABOM already stores, and nothing would say so.

**Zero results is a coverage gap until proven otherwise.** A spike that resolves nothing has two
readings — the library cannot cross a repository, or the fixture never gave it the chance. Build
the fixture so a *local* `./…` reference resolves in the same run, and the working case is the
control that tells those apart.

**The fixture comes from the CLI.** A hand-written BQRS is a trap this repository has already been
caught by: every tuple carried the same location, so every edge collapsed to one row and a correct
enqueue looked broken for several rounds. Build the tree, run `codeql database create`, run the
query, and check in what `bqrs decode` printed.

**The answer is reported even when it kills three plans.** A refuted spike is the cheapest outcome
available here, and recording it is the point. `plans/implemented.md` is where it goes.

## What the answer decides

- **Yes, and it can be pointed at a reference** — [`ql/account-database.md`](account-database.md),
  [`ql/semantic-model.md`](semantic-model.md) and
  [`ql/reachability-crosscheck.md`](reachability-crosscheck.md) all become startable.
- **Yes, but only at one reference** — the account database is still worth building for
  whole-corpus `@kind graph` projections, and every dataflow claim over it is confined to
  repositories that appear exactly once.
- **No** — all three retire, and the honest conclusion is that per-repository databases are the
  only shape available until the extractor supports overlays.

## Verification

- The test fails first, for the reason expected, before anything is built to satisfy it.
- The local-reference control resolves in the same database, so a zero is attributable.
- The two-reference case asserts *which* copy resolved, not merely that one did.
- Evaluation time is recorded for the two-repository tree, because it is the only number available
  before a real corpus and the account database's cost claim starts from it.
- Not run while discovery is crawling. Every `codeql` invocation shares one compilation cache.
