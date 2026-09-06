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

```bash
sqlite3 abom.db "EXPLAIN QUERY PLAN <the query>;"   # SCAN on the inner side is the warning
```

## One writer

The pool opens with `MaxOpenConns(1)`. Parallelism belongs in `Prepare`; persistence is
serialized through `Consume`. No second writer, no nested transaction.

## Reads for the interface

Scoped, not whole-graph. Return evidence rather than judgement: observed refs verbatim,
`Truncated` when a budget bit. Slices must be `[]T{}` — Go nil marshals to `null` and breaks the
C# client. Every `Next()` loop needs `rows.Err()`, and `errors.Is(err, sql.ErrNoRows)`.

Carrying a new field all the way to a screen: use the `surface-a-store-fact` skill.
