# A commit is fetched once, not once per reference

**Status:** Built and off by default. Naming `--cache-dir` retains fetched trees at
`${CacheDir}/${owner}/${repo}/${sha}-${filter}/`, keyed on the commit and the sparse filter, so a
commit is cloned once however many references name it and however many cycles ask for it. The
directory is owner-only and claimed by one process at a time: there is no cross-process protocol
for promotion and eviction, so a second run pointed at the same cache is refused rather than left
to delete a tree the first is analyzing. Retention stays opt-in until the full closure's disk
occupancy is measured.

## Why this exists

`internal/expand/checkout.go` clones into `${WorkDir}/${owner}_${repo}${refSuffix(ref)}` — a
directory keyed by the **reference**. What it fetches is a **commit**. Everything downstream
already knows those are different things: `QueryRun.key()` hashes `db_commit` with **no reference
in it**, because two references at one commit are one evaluation, and `abom_ref_resolution` already
records which commit a tag pointed at, unique on `(owner, repo, tag, commit_sha)` precisely because
a tag moves.

So the ledger and the resolution table both treat the commit as the identity, and without
`--cache-dir` the fetch is still the one step that treats the reference as the identity.

**322 of 377 dependencies on the crawled organization are analyzed at more than one reference.** Uncached, each reference is
cloned separately even when both resolve to the same commit, and `internal/expand/expand.go`
removes the checkout after each unit unless `KeepArtifacts` is set, so the next cycle re-clones
what the previous cycle deleted for a commit that cannot have changed.

The plan originally assumed the resolved SHA was known only *after* the clone. It was not: the
recheck lane already resolves every mutable reference through `Probe.Resolve`, which is `git
ls-remote`, before any fetch — so the identity needed to key the cache was already being paid for
unconditionally. Measured on 12 real action repositories, a probe runs in a median 166 ms against
614 ms for a sparse clone, so keying on the probe costs nothing extra and saves work 3.7× its
price. `abom_ref_resolution` is *not* consulted: its rows are historical readings whose freshness
is unknown, and trusting one would defeat the drift detection the recheck lane exists for.

That live-ref rule applies to declared-ref resolution. A native workflow lock deliberately selects
a historical commit, and ABOM [queues that commit](../../docs/architecture.md#locks) as its own row, so precheck, cold
fetch and cache hits all see a commit ref rather than a tag. The cache remains reusable by repository/commit/input
set and does not need one stored checkout per consumer. Lock-aware correctness must also work with
retention disabled; enabling this cache is not permission to start honoring native locks.

This is worth having on its own terms, with nothing else built. A closure database also fetches
through it (`codeql-abom closure --cache-dir`), on misses as well as hits, because it needs many
checkouts present at once and cannot afford to fetch each of them once per consumer.

## Pending

1. **Measure the full closure, then flip the default.** Retention is opt-in because the objection
   this plan has no answer to is disk, and a ceiling is a bound rather than a measurement. Per
   checkout the corpus averages **0.18 MiB** retained (1.09 MiB of tree and 1.01 MiB of `.git`
   across 12 action repositories, so `.git` is roughly 48% of the bytes and is kept because
   stripping it would change the SCM provenance `codeql database create` records). What is not
   known is the occupancy of a crawled organization's *full* closure, which is the number that
   decides both the default ceiling and whether `--cache-dir` should be on without being asked
   for. Measure that against a real organization, not the fixture corpus, then choose.
   Depends on nothing.
2. **Eviction knows what the queue still wants.** `Trim` sorts entries by their marker's mtime and
   removes the least recently used until the total is under the ceiling. It never reads the
   frontier, so an entry whose dependency is `pending` is exactly as evictable as one nothing will
   ask for again — and evicting it guarantees a clone next round, which is the cost this plan
   exists to remove. Recency is a proxy for *will be wanted again*, and the queue is the answer.
   The filter-qualified entry name makes that proxy worse in the one case that matters most: a
   widened filter holds the old variant and the new one side by side, the ceiling is enforced
   either way, so what doubles is not the disk but the eviction rate — the same `defaultCacheMaxMB`
   of 2048, sized from 0.18 MiB per checkout under a single filter, holds half as many distinct
   commits for the duration of the transition that is also re-analyzing them. Depends on nothing.

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

**It is part of the *analysis* key too, and that half is not finished.** Keying only the cache on
the filter answers where the bytes come from and leaves the reuse decision wrong, so
`QueryRun.SourceFilter` is in `run_key` and in the `abom_query_run.source_filter` column that
`QueryRunExistsForContent` joins on. That stops a stale row answering. It does not put anything
already `done` back on the queue, and no requeue can select on the filter — so widening
`sparsePatterns` today re-measures whatever happens to be queued and silently leaves the rest.
[`analysis-ledger.md`](analysis-ledger.md) owns that half; this plan must not grow a second
invalidation mechanism beside it.

## Verification

- A crawl over the crawled organization fetches each distinct `(owner, repo, sha)` **exactly
  once**. Asserted by counting clone invocations, never by elapsed time — a timing assertion passes
  on a fast machine with the cache disabled.
- An entry the frontier still names survives a `Trim` that takes the cache under its ceiling, and
  the entry evicted instead is one nothing queued asks for.
- Node, edge, reach, bill-of-materials component and pinning totals are **unchanged**. This plan
  changes where bytes come from and nothing else; any total that moves is a defect in the cache.
- A second crawl with no upstream change performs **zero** clones and still records the same
  analyses, which is the case that is invisible today.
- Disk occupancy of the full closure is recorded before retention becomes a default, because it is
  the objection this plan has no answer to yet.
- A moved tag is still detected for declared-ref consumers: the recheck lane sees the new commit
  and the cache serves the new tree. Locked consumers retain their selected commit on cache hits,
  misses and no-cache runs; drift does not silently repin them. The resolution plan owns those
  selection tests, while cache tests continue to assert one fetch for shared commit inputs.
