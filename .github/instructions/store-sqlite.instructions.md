---
description: "Use when writing SQL, schema changes, migrations, upserts or queries in the codeql-abom store package. Covers SQLite NULL semantics in UNIQUE constraints, why LastInsertId is unsafe after ON CONFLICT, the single-writer constraint, and additive column migrations."
applyTo: "internal/store/**/*.go"
---

# Store and SQLite rules

Only this package writes SQL. Derivation lives in `internal/graph`, delivery in `internal/serve`.

## Never use LastInsertId after an upsert

`ON CONFLICT DO UPDATE` reports one row affected even when it updated, and leaves `LastInsertId`
pointing at an unrelated row. This silently corrupted node identity once. Always read the id
back:

```go
if _, err := insert.Exec(...); err != nil { return err }
var id int64
if err := lookup.QueryRow(n.Key).Scan(&id); err != nil {  // never LastInsertId()
    return fmt.Errorf("resolving node %q: %w", n.Key, err)
}
```

## NULLs are distinct in a UNIQUE constraint

SQLite treats every NULL as different, so a nullable column in a UNIQUE constraint lets
duplicates through. Use `NOT NULL DEFAULT ''` for any column participating in one.

```sql
-- Empty rather than null: SQLite treats nulls as distinct in a UNIQUE constraint, so a
-- nullable column here would let duplicates through.
vulnerable_version_range TEXT NOT NULL DEFAULT '',
UNIQUE(advisory_row_id, package_name, vulnerable_version_range)
```

Elsewhere the opposite applies: `nullable()` stores empty strings as NULL so "absent" and
"empty" stay distinguishable.

## Distinguish absent from zero

A null score is not a score of zero. `cvss_v3_score` of `0.0` means *unscored* while severity is
still `high`, so rendering "CVSS 0.0" is a lie. Carry a `Has…` flag alongside the value.

## Schema changes must be additive

`Migrate()` applies `addedColumns` via `PRAGMA table_info` + `ALTER TABLE ADD COLUMN`. A
expansion runs for weeks and a graph takes hours to rebuild, so gaining a column must never
require a rebuild. Add to `addedColumns` as well as the `CREATE TABLE`, and verify in place:

```bash
sqlite3 abom.db "PRAGMA table_info(abom_frontier);"   # before and after
```

## Indexes are created after bulk load

`Migrate()` creates tables only; `CreateIndexes()` runs once at the end. Maintaining indexes
during a multi-million-row insert costs far more than building them once.
`TestIndexesAreNotCreatedByMigrate` guards this.

## One writer

The pool opens with `MaxOpenConns(1)`. Anything parallel belongs in `Prepare`; persistence is
serialised through `Consume`. Do not add a second writer or a nested transaction.

## Reads for the interface

Query surface functions are consumed by `internal/serve` and must:

- carry camelCase `json:` tags, matching the sibling `mrva-reports` convention
- be scoped, not whole-graph — `Subgraph` walks bounded by hops and node count so cost follows
  the scope rather than the corpus
- return evidence rather than judgement: observed refs verbatim, `Truncated` when a budget bit

## Check `rows.Err()`

Every `Next()` loop needs a final `rows.Err()`, and `errors.Is(err, sql.ErrNoRows)` rather than
`==`. `go vet` catches the first; nothing catches the second.
