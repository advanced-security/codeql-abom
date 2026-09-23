# A verifiable analysis ledger

**Status:** Every run records the pack, its content digest, the CodeQL that evaluated it and the
checkout filter the database was built from, and work already on record is not done twice. Making a
change *undo* that is the unfinished half: a dimension of the identity can refuse a skip without
being able to select the work it made stale.

## Why this exists

Every unit of analysis work is **(database identity x query identity)**, and both are
content-addressed: if the pair is already on record, CodeQL is deterministic and nothing needs to
run. The ledger is worth exactly what its columns are worth — a row that omits what evaluated it
cannot be told from one produced by a different CodeQL, and the skip that reads it trusts it.

Refusing a skip is the half that is finished, and it only moves work that is **queued**. A `done`
frontier row is never re-examined, so a change reaches it only if something puts it back.
`store.RequeueStale` is that something, and it selects on `cli_version` and `pack_digest` — two of
what `QueryRun.key()` hashes. Nothing can select on `source_filter`, which arrived with
[`checkout-cache.md`](checkout-cache.md); nothing can select on `query_version` or `pack_version`
either, and those are now **written on every run**, so the dimension exists in the data and only
the selection is missing.

So widening `sparsePatterns` half-measures the corpus. Everything queued is fetched and analyzed
under the new filter, because the reuse decision now reads the filter; everything already `done`
keeps rows measured under the old one, because no requeue can name it. Both sit in the same tables,
and no count says the two are mixed.

The dimension that has already done the damage is the one the ledger does not carry at all. A
Go-side change to how results are *read* invalidates nothing: after the `$/` parser fix,
`astral-sh/setup-uv` at `e105c8fb…` was requeued, resolved to the commit already on record and
never re-ran, leaving its 17 pre-fix rows in place.
[`unresolved-references.md`](unresolved-references.md) is waiting on this for the 63 edges it cannot
heal.

**Item 2 is a fork before it is work**, which is why this plan sits among the unranked refinements
rather than in the ranking: the three ways to version the Go side invalidate different amounts, and
picking one is the decision. Everything else here follows from it.

## Pending

1. **A requeue can name any dimension of the identity.** `store.RequeueStale(cliVersion,
   packDigests)` takes exactly the two it can select on, so each new dimension is a new argument or
   a new function. Selection must include the recorded
   [analysis mode](../../docs/architecture.md#analysis-modes) and the `query_version` and
   `pack_version` columns every run now writes — a library edit changes a query version without
   changing its pack digest, so that is a dimension nothing can currently invalidate on. Note what
   the existing selection is already coarser than
   anyone reasons about: `requeueDone` correlates the frontier row to `abom_query_run` **by
   repository alone**, so one stale run requeues every reference of that repository rather than the
   analysis that is actually out of date. Depends on nothing.
2. **The Go side of interpretation has a version.** `expand.pendingWork` reads repository, commit,
   query id, pack, CLI and filter — nothing about the code in `internal/ingest` and
   `internal/normalize` that decides what the same BQRS tuples *mean*. The seed lane already solves
   this: `pipeline.Config.ExtractorVersion` reaches `store.Unit.ExtractorVersion`, which
   participates in `Unit.key()`, so changing how results are interpreted re-runs the work. **The
   decision is a flag, a build-stamped constant, or a hash of the interpreting code**, and it is a
   decision rather than a preference because each prices a different thing: a flag invalidates what
   a person names and forgets what they did not, a constant invalidates everything on a release
   whether or not interpretation moved, and a hash invalidates precisely while re-running the
   corpus whenever an unrelated refactor touches the package. Depends on nothing.
3. **A commit's content is measured once per filter, not once.** `abom_commit_content` is
   `PRIMARY KEY (repository, commit_sha)` and `RecordCommitContent` overwrites `indexed_digest`, so
   re-fetching a commit under a different filter destroys the measurement the earlier runs were made
   against. `source_filter` on the run is what stops `QueryRunExistsForContent` crediting a
   narrow-tree run with the wide tree's digest — a guard against a measurement that is gone, not a
   use of one. Two things follow: switching a filter back cannot recognize content it has already
   measured, and no reader can answer *what did this commit hold under the filter that run saw*. The
   key is `(repository, commit_sha, source_filter)`. Depends on nothing.
4. **Price an invalidation before making it.** *How many repositories does this re-run* has no
   answer until the requeue has already happened and reported its row count, which is the wrong
   order for the one decision the ledger exists to make affordable. A dry run over the same
   selection as 1, reported per tier and per depth, is what turns widening a filter or bumping a
   pack into a priced decision rather than a discovered one. Depends on 1.
5. **Tighten the identity columns to `NOT NULL`.** Blocked by data: **1,555 of 5,472** ledger rows
   predate the identity columns, and a pre-identity row never matches a current key, so each of
   those pairs has to be analyzed once more before the constraint can hold. `source_filter` pushed
   that gate out rather than moving it — every row written before it is NULL there too. A one-line
   change after a full re-crawl, and wrong before one.
6. **The database digest and overlay databases**, together: a digest's only job is asserting that
   a stored base is still the one we analyzed, and nothing stores a base until overlays do.
   Blocked by the extractor — `actions` declares no `overlay_support_version`, so `--overlay-base`
   is accepted, exits 0 and is a silent no-op.
   `TestTheActionsExtractorStillCannotBuildAnOverlayBase` fails when that changes, and the
   database build is **20.3%** of a repository's cost.

## The constraint that must not be broken

**A content hash of a database directory must never enter `run_key`.** A CodeQL database is not
byte-reproducible, so the run key hashes *inputs* — `H(repo, commit, cli_version, flags)`. Put a
content hash in it and every rebuild invalidates every row it touches, the skip silently stops
firing, and nothing looks broken: the crawl quietly costs what it did before the ledger existed. A
result hash is repository-independent for the same reason, so no absolute path, timestamp, run id
or `automationDetails` may enter one.

**A dimension that can refuse a skip must be able to select the work it made stale.** Adding one to
the run key is half a feature: it stops the wrong row answering, and does nothing about the rows
already recorded. Shipping the halves apart leaves a corpus measured two ways with nothing in the
schema or on screen saying which row is which — the same failure `analysis_mode` exists to prevent
one level up, arriving through the back door.

**Invalidation is selective or it is a re-crawl.** The blunt instrument is always available and
always wrong: requeueing a tier re-clones and re-analyzes everything in it, which is the cost the
ledger was built to remove. A change that cannot name what it touched has not been described well
enough to ship.

## Verification

- Two consecutive full rebuilds produce identical `run_key` sets and identical result hashes.
- An unchanged corpus evaluates zero queries and changes no counts.
- Changing one dimension requeues the repositories that dimension touched and **no others**,
  asserted by counting the rows the requeue moved against a set computed independently — never by
  observing that the crawl got shorter.
- The priced estimate from 4 and the row count the requeue actually moves agree.
- A re-analysis after an interpretation change actually re-runs, which is the count of surviving
  `cross-owner` `$/` edges reaching zero.
- Deferred with the overlays: two builds of the *same commit* produce **different** database
  digests and the **same** run keys, which fails loudly in the one case that would otherwise
  retire the skip in silence.
