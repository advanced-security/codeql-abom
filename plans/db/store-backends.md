# One store, two backends: SQLite for one binary, PostgreSQL for an enterprise

**Status:** one embedded SQLite store with one writer; no store interface exists, and no PostgreSQL
backend.

## Why this exists

ABOM's target is now an enterprise: many organizations, up to 100,000 repositories each, read on
events all day. One embedded database owned by one process is then a single point of failure, and it
bounds ingestion, derivation and every read to one machine. The operator's decision: **PostgreSQL
for the enterprise deployment, so the coordinator can be replicated and readers scaled out; SQLite
kept for the single-binary install; one store interface with one shared test suite.**

This reverses the database decision in [`tools/deployment.md`](../tools/deployment.md), which kept
SQLite in exactly one coordinator and excluded network SQL and a replicated coordinator. Workers
still never open the store: that half of the deployment design stands.

## Decisions

- **Both backends are first-class.** Every store test runs against both. A capability that exists on
  one backend only does not exist.
- **No data is carried from SQLite into PostgreSQL.** An enterprise deployment starts from its own
  crawl; databases are rebuilt from the crawl, not migrated.
- **SQL stays in `internal/store`.** The interface is the store's own method set; a second package
  writing SQL for the other backend would be a second store.

## Where the dialects differ in today's SQL

| Construct | SQLite today | PostgreSQL |
| --- | --- | --- |
| Parameters | `?`, and numbered `?1`–`?3` in the submission closure | `$1`… — one rebinding layer, not two query texts |
| JSON passed as data | `json_each`, `json_extract` (lock choices, live commits) | `jsonb_array_elements`, `->>` |
| Additive migrations | `PRAGMA table_info` + `ALTER TABLE ADD COLUMN` | `ADD COLUMN IF NOT EXISTS` |
| Row identity | `INTEGER PRIMARY KEY` rowids, read back after upserts | identity columns, read back the same way |
| Ignore on conflict | `INSERT OR IGNORE` | `ON CONFLICT DO NOTHING`, which SQLite also accepts — use it everywhere |
| String aggregation | `GROUP_CONCAT` | `string_agg` |
| Booleans and times | `INTEGER` flags, RFC 3339 `TEXT` | either the same representation or a mapping, decided once |
| Single writer | `MaxOpenConns(1)`, serialized `Consume` | concurrent sessions — see below |

**RFC 3339 text orders correctly only at a fixed width.** `time.RFC3339Nano` trims trailing zeros
from the fraction, so `…00.12Z` sorts after `…00.1234Z`. `ReclaimStale` compares
`claimed_at <= cutoff` that way, and `TestReclaimOrphanedTakesBackEvenAClaimMadeMomentsAgo`, whose
claim is microseconds old, failed 5 of 300 runs on 2026-10-07 with `internal/frontier` unchanged.
A reclaim at process start compares claims seconds old, which order correctly, so the cost is a
flaky `make verify` rather than a stuck queue. A fixed-width fraction everywhere, or comparing
through `julianday()`, closes it; whichever this plan decides for times decides it.

## Pending

1. **Extract the interface without changing an answer.** SQLite only, every endpoint captured on the
   crawled organization before and after and compared byte for byte. Nothing else can start until
   the interface is the only way in.
2. **A shared suite that runs on both.** `make verify` is the only definition of verified, so it has
   to run the PostgreSQL half too. **The fork is how a PostgreSQL appears there:** a container,
   which adds a Docker dependency to every machine that verifies; or a PostgreSQL binary the test
   harness downloads and starts, which adds none but pins a server build in the harness. Decide
   before item 3. Depends on 1.
3. **The PostgreSQL backend**, seam by seam, each seam proven by the shared suite rather than by
   inspection. Depends on 2.
4. **A change feed ordered by commit, not by row id.** Every delta cursor today is a row-id watermark
   with an epoch. With concurrent writers a transaction can commit a lower id after a cursor has
   passed it, and the watermark skips that row for good. Both backends get a sequence assigned in
   commit order; the epoch semantics stay as they are. Depends on 3.
5. **Correct under concurrent writers.** The one-writer rule becomes: transactional writes whose
   conflicts are decided by constraints, ingestion made effectively-once by the per-attempt receipts
   [`tools/deployment.md`](../tools/deployment.md) already requires, and one scheduler at a time
   holding a lease, so claiming is never raced. Concurrency tests ingest the same results from
   several writers and must end where serial ingestion ends. Depends on 3.
6. **Replicas for readers.** The API reads replicas; a control action that must see its own write
   reads the primary. Depends on 5.

## The constraints that must not be broken

**A cursor ordered by row id cannot be honored under concurrent writers.** It does not fail: it
silently skips. A refused cursor is correct behavior; a cursor that misses rows forever is not.

**The graph tables stay append-only on both backends.** Only the compaction deletes, and it bumps
the epoch that forces every client to reload, exactly as on SQLite.

**Absent is not zero on either backend.** Both treat NULLs as distinct in a UNIQUE constraint by
default, so the `NOT NULL DEFAULT ''` rule for keyed columns carries over unchanged; an empty-set
aggregate is NULL on both and is coalesced on both.

**Workers never open the store.** Moving to a network database makes that easier to break, not less
important: a worker with a connection string is a second writer outside every fence.

## Verification

- The shared suite is green on both backends, and the corpus builder can build into either; the
  same replay answers every endpoint identically on both.
- Several writers ingesting the same results end in the state one writer reaches.
- A delta cursor taken during interleaved commits receives every row, on both backends.
- A replica serving reads is never more than its reported lag behind, and a control action's own
  write is visible to the reader that made it.
