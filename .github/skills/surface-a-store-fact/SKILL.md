---
name: surface-a-store-fact
description: 'Carry a new fact from SQLite to the interface in codeql-abom: adding a column, field or count to the Discovery grid, the Explorer graph, or a node page. Use when a value exists in abom.db but reaches no Go struct, no API response, or no screen; when a new field renders blank, as null, or as zero; or when adding a metric that must survive delta merges and node collapsing. Covers the store -> serve -> Models.cs -> Razor -> graph.js chain and the layer-specific traps that silently drop a field.'
---

# Surface a store fact

A fact in the database reaches the screen through five layers, and each has a way of dropping it
that no compiler and no unit test catches. This has cost a defect per layer.

## When to use

- A column exists in `abom.db` and reaches no Go struct (`analyzed_at` was 100% populated and
  invisible for months).
- A new count, ratio or identity must appear on Discovery, the Explorer, or a node page.
- A field renders blank, `null`, `0`, or vanishes once a delta arrives or nodes are collapsed.

## The chain

| Layer | File | Needed? |
| --- | --- | --- |
| 1 Query + struct | `internal/store/*.go` | Always |
| 2 Endpoint | `internal/serve/*.go` | Only for a new route or parameter |
| 3 Model | `ui/Models/Models.cs` | Always |
| 4 Render | `ui/Pages/*.razor` | Always |
| 5 Graph | `ui/wwwroot/js/graph.js`, `collapse.js` | Only for Explorer node fields |

**There is no DTO layer.** `serve` marshals store structs directly (`writeJSON(w, items, err)`),
so the `json:` tag *is* the API contract. Tags are camelCase.

## Per-layer traps

**1 Store.**
- Absent is not zero. If empty is a real state, carry a `…Known bool` beside the value, or the
  interface cannot tell "not recorded" from "none" — those are opposite statements.
- Aggregates over an empty set are NULL: `COALESCE(SUM(...), 0)`.
- Slices must be `[]T{}`, never nil. Go nil marshals to JSON `null` and breaks C# `.Select()`.
  Pinned by `TestFullGraphCollectionsAreNeverNil`.
- A scalar subquery returns an arbitrary row once the underlying key has multiplicity. Aggregate.
- New join on a read path: check `EXPLAIN QUERY PLAN`. `SCAN` on the inner side means an index,
  and it belongs in `Migrate()` because `serve` never calls `CreateIndexes()`.

**3 Model.** Property name must match the JSON tag case-insensitively; a mismatch is silent and
renders the default.

**4 Render.** Decide what *absent* looks like before writing the cell. Blank, "not recorded" and
`0` are three different claims. Columns inside `MixedStates || _status == "done"` are hidden on
the default `?status=queued` view — check there before concluding a field is missing.

**5 Graph.** Two functions silently drop a new node field, and neither fails a test that does not
name it:
- `mergeInto` in `graph.js` — a delta overwrites the node, so an uncopied field reverts.
- `collapseGroups` in `collapse.js` — a group must initialise and sum it, or folding re-hides it.

## Procedure

1. Confirm the fact is real and measure it: `sqlite3 /tmp/abom-real/abom.db "<query>"`. Check it
   is not already rendered somewhere hidden by a filter.
2. Write the store test first, run it, watch it fail to compile or assert.
3. Work the layers in order, skipping the ones the table says are not needed.
4. Add a bUnit test: `Answer("api/frontier", json)` with the field present *and* a second case
   with it absent. The absent case is the one that catches "not recorded" rendering as "none".
5. `make verify` — but never while discovery is running (`make status` first).
6. Cross-check against the corpus on a **copy**, and against the same set computed independently
   in SQL. Agreement must be exact; a plausible-looking ratio does not catch a bad join.

```bash
cp /tmp/abom-real/abom.db /tmp/abom-after.db
bin/codeql-abom serve --db /tmp/abom-after.db --addr 127.0.0.1:8100 &
curl -sS 127.0.0.1:8100/api/frontier?limit=500
```

## Done means

The field is correct on the real corpus, its absent case says so rather than reading as zero, and
`make verify` is green. A green build alone is not done — every architectural bug in this
repository passed one.
