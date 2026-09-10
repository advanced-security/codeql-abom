---
description: "Use when writing SQL, schema changes, migrations, upserts or queries in the codeql-abom store package. Covers LastInsertId after ON CONFLICT, NULL semantics in UNIQUE constraints, absent versus zero, empty-set aggregates, scalar subqueries under multiplicity, empty join keys, additive migrations, index placement and the single-writer rule."
applyTo: "internal/store/**/*.go"
---

# Store and SQLite rules

Only this package writes SQL. Derivation lives in `internal/graph`, delivery in `internal/serve`.
`serve` marshals these structs directly, so `json:` tags are the API contract and are camelCase.

## Never use LastInsertId after an upsert

`ON CONFLICT DO UPDATE` reports one row affected even when it updated, and leaves `LastInsertId`
on an unrelated row. This corrupted node identity once. Read the id back with a lookup.

## NULLs are distinct in a UNIQUE constraint

A nullable column in a UNIQUE constraint lets duplicates through. Use `NOT NULL DEFAULT ''` for
any column in one. Elsewhere `nullable()` stores `""` as NULL, keeping absent and empty apart.

## Distinguish absent from zero

A null score is not zero: `cvss_v3_score` of `0.0` means *unscored*. Carry a `Has…` flag.
`GROUP_CONCAT` skips nulls, which is how *not recorded* stays distinct from *none*.

A `NOT NULL` boolean cannot say *not asked*. `has_database INTEGER NOT NULL` forces `0` for a
repository nobody probed, asserting code scanning is off there. A column holding a measurement
someone may not have taken is nullable — which outranks the rule above, and the rule above applies
only to columns inside a UNIQUE constraint.

## Aggregates over an empty set are NULL

`COUNT` returns 0; `SUM`, `MIN` and `MAX` return NULL. Use `COALESCE(SUM(...), 0)`.

## A scalar subquery returns an arbitrary row, not an error

`(SELECT status ... WHERE node_key = ?)` was correct until the queue was re-keyed
`(node_key, ref)`. Aggregate instead. When a key gains a column, every `WHERE <old key> = ?` is a
candidate defect — make the tuple the only way to address a row so the compiler finds the rest.

## A join on a key that can be empty joins everything

Most queue rows have no `resolved_sha` and many runs have no `db_commit`:

```sql
AND COALESCE(abom_frontier.resolved_sha, '') <> ''
AND COALESCE(r.db_commit, '') <> ''
```

## Schema changes must be additive

`Migrate()` applies `addedColumns` via `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`. An
expansion runs for weeks, so a new column must never require a rebuild. Add it to `addedColumns`
as well as the `CREATE TABLE`.

## Indexes after bulk load, except the ones a page reads

`CreateIndexes()` runs after loading, and
`TestIndexesAreNotCreatedByMigrateOnTheTablesItLoadsInBulk` guards the tables that rationale
protects — not indexes as such. `serve` never calls it, so an index a page needs on a small table
belongs in `Migrate()`. One missing index cost 198 ms a page against 9.7 ms.

An index added only to `indexDDL` therefore **never reaches an established database**. Nothing calls
`CreateIndexes()` again after the first bulk load.

```bash
sqlite3 abom.db "EXPLAIN QUERY PLAN <the query>;"   # SCAN on the inner side is the warning
```

## A function on an indexed column cannot use the index

An `OR LOWER(x) = SUBSTR(y, 6)` arm added to a join forced a full scan per returned row — 17.7 s
against 0.045 s for the same answer. Where two key shapes must meet, emit both forms on the build
side and join on equality. Assert the cost as a query plan in a test, never as a stopwatch.

## A rebuild that drops rows must VACUUM, outside the transaction

A table rewritten to shed duplicates leaves its pages on the free list, so the file *grows* after a
migration that removed most of the rows. `VACUUM` cannot run inside a transaction — commit first.

## Observations are keyed by the statement, not by the run

UNIQUE is `(subject_kind, subject_key, name, value)` and deliberately excludes `query_run_id`, so
re-running a query refreshes its answers rather than adding to them. `value` is `NOT NULL DEFAULT ''`
because NULL never equals NULL in a UNIQUE constraint and a nullable column cannot deduplicate.
Subjects are named by key rather than row id, so an observation survives being recorded before its
subject exists. Two queries may disagree about one subject, and both rows are kept.

## A tag is not a stable key

`abom_ref_resolution` is unique on `(owner, repo, tag, commit_sha)`, not on the tag, because a
major-version tag points somewhere new after every release. The ref that was analyzed is a
historical fact; the default branch is a current one. Store the first, resolve the second at read
time, and never overwrite one with the other. An empty ref keeps meaning "as requested, the default
branch" and gains no second meaning.

## Identity is hashed from inputs, never from the database file

A CodeQL database is not byte-reproducible — timestamps and parallel extraction vary. A run's
identity is the resolved commit, the pack digest and the CLI version. Result hashes are per result
and deliberately repository-independent, so they exclude commit, line and message.

## Staleness is only observable across a change in run identity

A re-run with an unchanged pack is the *same* ledger row, so no single query can tell whether a
finding is still reported. `abom_finding_sighting` records which run saw which finding, and
**absence in a run that could not have reported it is not absence.** A rule that ignores this
condemned 2,420 findings other references still report.

## Record an epoch honestly, and carry it forward

A generation whose epoch was never written is not a generation at epoch zero. A column defaulting to
`0` collapses those two, and the result is a cursor that can never match again — indistinguishable
from one legitimately refused after a compaction, except that it never recovers.

`RecordGraphGeneration` therefore reads `currentEpoch()` and writes it; only the compaction
advances it. **A refused cursor is correct behavior; a cursor that can never be honored is not.**
The epoch check exists because row-id watermarks stop meaning anything once rows are deleted, so the
fix for a stuck delta is always to record the epoch, never to weaken the comparison.

**Two writers of one column drift.** The compaction named `epoch` in its insert and the ingestion
path left it defaulted, which is exactly how this broke; both now go through one reader.
`TestAGenerationRecordedAfterACompactionIsStillHonored` compacts first on purpose, because a delta
test that does not passes on any corpus that has never been compacted.

## Removal is a tombstone, then a compaction

`removed_at` is set on the row and honored on every read; only `Compact` deletes, and it bumps the
epoch that forces clients to reload. That is what keeps row ids monotonic, and a delta cursor valid.
Pruning a node also takes `abom_finding_node`, `abom_alert_link` and every edge naming it as
*either* endpoint — but never the ledger. `abom_query_run` and `abom_finding` are evidence; the
graph is a projection, and a pruned node may legitimately come back.

## One writer

The pool opens with `MaxOpenConns(1)`. Parallelism belongs in `Prepare`; persistence is
serialized through `Consume`. No second writer, no nested transaction.

## Reads for the interface

Scoped, not whole-graph. Return evidence rather than judgement: observed refs verbatim,
`Truncated` when a budget bit. Slices must be `[]T{}` — Go nil marshals to `null` and breaks the
C# client. Every `Next()` loop needs `rows.Err()`, and `errors.Is(err, sql.ErrNoRows)`.

Carrying a new field all the way to a screen: use the `surface-a-store-fact` skill.
