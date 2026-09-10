# A finding must be able to name the ref it came from

**Status:** The expand lane carries the requested and analyzed refs into the ledger as
`abom_query_run_ref`, on the record path and on the skip path. The seed lane records no run at all.

## Why this exists

**A finding that cannot name its ref cannot gate a pull request.** `api/verdict-gate.md` blocks a
merge on evidence, and a predicate for "a current result on the default branch" is not expressible
while a finding's provenance stops at a commit with no way back to the branch that commit was on.

It is broader than the gate. GitHub's own features are scoped to the default branch or to protected
branches: security campaigns cover code scanning alerts on the default branch only, Dependabot
alerts and the dependency graph are reported against it, and branch protection decides whether a
check can block at all. A finding reported without its ref is a measurement presented without the
qualifier that makes it meaningful.

## Pending

1. **The seed lane records no query run.** `codeql-abom build` ingests with
   `ingest.Options{QueryRunID: 0}`, so there is no run for a ref to attach to. The ref is not
   missing from a seed-lane run; the run itself is missing, and nothing seeded is attributable to
   an analysis at all.
2. **A ref reached only through content equivalence stays unrecorded.** `AttachQueryRunRef` matches
   on the full identity, commit included, so it refuses to attach a reference to a run that
   analyzed a *different* commit with byte-identical content. That is a coverage gap rather than a
   lie; closing it needs a relation that can say "this ref was settled by equivalence with that
   run" without claiming the revision was examined.
3. **The predicates that were waiting on this.** A `ref=default-branch` filter is now expressible,
   and `api/verdict-gate.md` can state whether it is able to block — with one caveat. The
   `protected` flag is set repository-wide: asked of three declared repositories, `?protected=true`
   returned **58** branches, 48 of them on one repository, including `naman_test` and twenty-odd
   `export-D…` branches. It needs the protected set intersected with the default branch.

## The constraint that must not be broken

**A recorded ref is a historical fact; the default branch is a current one.** `analyzed_ref` says
what was checked out at the time; `repository.default_branch` says what the default is *now*. A
branch rename retroactively rewrites every historical attribution that resolves the name late
instead of storing what happened. Store the ref that was analyzed, resolve the default branch at
read time, never overwrite one with the other.

**The ref is not part of the run identity.** `QueryRun.key()` hashes the commit, so two analyses of
one repository at one commit are deliberately one ledger row — and **392 repository/commit pairs
are reached by more than one ref**, so a `ref` column would have named one and dropped the other
391 silently. It would also defeat the content-equivalence skip precisely when a second ref reaches
content already analyzed.

**Absent is not unprotected**, and **an empty ref keeps meaning "as requested, default branch"**. A
migration must never back-fill `ref = 'main'` into rows that recorded a request.

## Verification

- A finding analyzed at `v4` and the same finding analyzed at the default branch are distinguishable.
- Existing `abom_query_run` rows get **no ref**, not a guessed one. The corpus holds 5,472 runs
  whose ref nobody recorded, and they read as unknown afterwards.
- `default_branch` populates from the listing already fetched with **no increase in API calls**,
  measured as a call count rather than as elapsed time.
- A repository with no protected branches and one never probed produce **different** answers.
- Renaming a branch upstream does not change what a historical finding says it was analyzed at.
