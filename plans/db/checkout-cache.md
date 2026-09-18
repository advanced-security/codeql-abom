# A commit is fetched once, not once per reference

**Status:** Every reference is cloned separately, including the ones that resolve to a commit
already on disk, and the tree is deleted before the next cycle can reuse it.

## Why this exists

`internal/expand/checkout.go` clones into `${WorkDir}/${owner}_${repo}${refSuffix(ref)}` — a
directory keyed by the **reference**. What it fetches is a **commit**. Everything downstream
already knows those are different things: `QueryRun.key()` hashes `db_commit` with **no reference
in it**, because two references at one commit are one evaluation, and `abom_ref_resolution` already
records which commit a tag pointed at, unique on `(owner, repo, tag, commit_sha)` precisely because
a tag moves.

So the ledger and the resolution table both treat the commit as the identity, and the fetch is the
one step that still treats the reference as the identity.

**132 of 225 dependencies are analyzed at more than one reference.** Each reference is cloned
separately even when both resolve to the same commit. Worse, `internal/expand/expand.go` removes
the checkout and the database after each unit unless `KeepArtifacts` is set, so the next cycle
re-clones what the previous cycle deleted, for a commit that cannot have changed.

This is worth having on its own terms, with nothing else built. It is also the precondition for
[`ql/closure-database.md`](../ql/closure-database.md), which needs many checkouts present at once
and cannot afford to fetch each of them once per consumer.

## Pending

1. **Resolve before fetching, and key the cache on the commit.** The resolved SHA is currently
   known only *after* the clone, from `git rev-parse HEAD`. Two orders are available and the choice
   is this plan's: ask `abom_ref_resolution` first and fall back to `git ls-remote` on a miss, or
   fetch into a staging directory and move it into the cache once the SHA is known. The first
   spends a network round trip to save a clone; the second spends nothing extra and caches one
   clone later. Measure both against the crawled organization rather than reasoning about it.
   Depends on nothing.
2. **A cache that outlives the unit.** A directory outside `WorkDir`, laid out
   `${CacheDir}/${owner}/${repo}/${sha}/`, so a path can be read back to its identity — unlike
   today's underscore join, where `foo_bar_baz` is both `foo/bar_baz` and `foo_bar/baz` and
   repository names contain underscores. `KeepArtifacts` stops being the only way to retain
   anything, and becomes what it says: keep the *database*, which is a different decision.
   Depends on 1.
3. **Eviction, with a stated disk cost.** A cache with no ceiling is a disk leak that presents as a
   crawl failure weeks later. Least-recently-used against a configured ceiling is the obvious
   choice and is not the only one — a commit no frontier row still names is evictable regardless of
   recency, and that is cheaper to compute than it sounds. **The disk cost of the crawled
   organization's full closure is measured before any retention default changes.** Depends on 2.

## The constraints that must not be broken

**The cache holds a fetch, never an extraction.** A CodeQL database is not byte-reproducible, which
is why [`db/analysis-ledger.md`](analysis-ledger.md) already refuses to put a content hash of a
database directory into `run_key`. The same fact bars caching databases by content here: the cache
key is a commit, and a commit is a fact about somebody else's repository that cannot drift.

**A cache hit is not a skip.** Reuse of a checkout says nothing about whether the analysis needs to
run again — that decision stays in `expand.pendingWork`, over `QueryRunExists` and
`QueryRunExistsForContent`. Conflating them would make a cache eviction silently re-run analysis
and a cache hit silently suppress it, and neither would appear anywhere.

**A reference is not a cache key, ever.** A tag moves. The whole reason `abom_ref_resolution` is
unique on `(owner, repo, tag, commit_sha)` rather than on the tag is that one tag legitimately has
several commits over time, and a cache keyed on the tag would serve the old one forever with
nothing to say it had.

**The sparse pattern set is part of the key, or the cache lies.** `sparsePatterns` is currently
`/.github/`, `action.yml`, `action.yaml`. A cached tree fetched under one pattern set is not a
valid hit for a wider one — it is a tree that is *missing files*, which reads downstream as a
repository that does not contain them. [`ql/semantic-model.md`](../ql/semantic-model.md) plans
adapters that widen this set, so the trap is not hypothetical.

## Verification

- A crawl over the crawled organization fetches each distinct `(owner, repo, sha)` **exactly
  once**. Asserted by counting clone invocations, never by elapsed time — a timing assertion passes
  on a fast machine with the cache disabled.
- Node, edge, reach, bill-of-materials component and pinning totals are **unchanged**. This plan
  changes where bytes come from and nothing else; any total that moves is a defect in the cache.
- A second crawl with no upstream change performs **zero** clones and still records the same
  analyses, which is the case that is invisible today.
- Disk occupancy of the full closure is recorded before retention becomes a default, because it is
  the objection this plan has no answer to yet.
- A moved tag is still detected: the recheck lane sees the new commit and the cache serves the new
  tree, not the one it already held for that tag.
