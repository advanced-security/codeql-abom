# What no longer belongs is removed, provably

**Status:** planned. Depends on `db/discovery-boundary.md`, which decides what belongs. This plan
is the mechanism for taking out what does not, and the wide seed is what proves it works.

## Why this exists

Dependency relationships change. A `uses:` line is deleted, a repository is archived, a tag stops
being referenced, an account is un-declared — and today **nothing can ever leave**. The graph
tables are append-only and `GraphDelta` can only add, so a client is never told that something
disappeared.

That is not a hypothetical. The re-scope in `db/discovery-boundary.md` will mark most of a
56,830-node corpus as out of scope, and an interface that draws it anyway is reporting a supply
chain nobody has.

**The wide seed is the test corpus.** It is the largest orphan set this project will ever have —
204 repositories and everything they pulled in, none of it owned — and validating pruning against
it is cheaper, safer and far more convincing than waiting for the real thing to arrive one deleted
`uses:` line at a time.

## What is already true

| | |
| --- | --- |
| guard | `TestGraphTablesAreAppendOnly` |
| delta shape | `GraphDelta` returns new and updated nodes and edges; **no removals** |
| delta cursor | a row-id watermark in `abom_graph_generation` |
| prior art | the junk `/` repository was deleted by hand, once |

That hand-deletion is the most useful thing we know. Foreign keys refused the naive delete because
`abom_finding_node`, `abom_alert_link` and **edges referencing those nodes as endpoints rather
than by repository** all held references. It was verified afterwards with `PRAGMA
foreign_key_check` empty and `PRAGMA integrity_check` ok. Any automated prune has to handle the
same closure, and the endpoint case is the one that is easy to miss because the obvious query
filters by `repository_row_id`.

**Status:** shipped. Removal is a tombstone the delta reports, reads go through views that cannot
be opted out of, and a separate compaction reclaims the space behind an epoch that reloads every
client. On the reference corpus a prune marks **42,011 of 56,830 nodes and 31,365 of 46,264
edges**, leaving the graph the declared scope actually reaches.

## What reconciling and two probes corrected

**The plan priced "reads filter it" as a detail. It is 104 call sites** across 23 files —
`nodedetail.go` alone joins the graph tables 14 times, `graph_full.go` 11 — against **2** write
sites. Every missed filter is a pruned node still drawn, which fails nothing.

**The first design for fixing that cheaply does not work.** Renaming the physical tables to
`abom_node_all` and creating views under the *original* names would have made all 104 reads
correct untouched. Probed against a copy of the real corpus: SQLite's `ALTER TABLE ... RENAME TO`
**does not rewrite foreign key references in other tables**, so `abom_finding_node` still read
`REFERENCES abom_node(row_id)` — now a view — and `PRAGMA foreign_key_check` failed with
`foreign key mismatch - "abom_exposure" referencing "abom_node"`. Six tables reference
`abom_node`.

**The design that works keeps the tables and adds views beside them.** `abom_node_live` and
`abom_edge_live` are new objects, so no foreign key moves and no table is rebuilt. The 104 reads
were rewritten once, and `TestReadsGoThroughTheLiveViews` keeps them that way: it names the three
files allowed to touch the physical tables — the writer, the prune and the schema — and fails any
other `FROM`/`JOIN` that does not say `_live`. What "live" means now changes in one place.

**The views cost nothing measurable.** SQLite flattens them, so the plan is the same index search
as before:

```text
SEARCH abom_node USING INDEX sqlite_autoindex_abom_node_1 (node_key=?)
SEARCH abom_edge USING INDEX idx_edge_dst (dst_node_row_id=?)
```

The physical-table plan differs only in being *covering*, since a view selects every column.
0.02 s against 0.01 s over 46,264 edges.

## What the corpus check found

Against a copy, with seven accounts declared:

| | Before | After marking | After compaction |
| --- | ---: | ---: | ---: |
| nodes (physical) | 56,830 | 56,830 | **14,819** |
| nodes (live) | 56,830 | **14,819** | 14,819 |
| edges (physical) | 46,264 | 46,264 | **14,899** |
| edges (live) | 46,264 | **14,899** | 14,899 |
| findings | 11,370 | 11,370 | **11,370** |
| query runs | 5,472 | 5,472 | **5,472** |

**Three quarters of the graph was somebody else's supply chain.** The ledger did not move, which
is the invariant working: a run happened and a finding was really produced, so deleting them to
tidy the graph would record that work did not occur.

A second dry run reports **0 nodes and 0 edges** — the operation settles, which is what makes it
safe to run on a schedule. `PRAGMA foreign_key_check` is empty and `PRAGMA integrity_check` is
`ok` both after marking and after compaction.

## The design

### Tombstone, then compact

Removal is two mechanisms, because they answer different questions.

**`removed_at` on `abom_node` and `abom_edge`.** Every read filters it. Row ids stay monotonic, so
every delta cursor a client holds remains valid, and `GraphDelta` gains a **removals list** so a
client learns what disappeared instead of holding it for ever. This is what makes deletion
expressible at all.

**Compaction hard-deletes tombstoned rows** and bumps an epoch in `abom_graph_generation`. A
client whose cursor predates the epoch is told to reload rather than being handed a delta that
silently skips row ids. Compaction is separate and deliberate, so a prune is reversible right up
until someone decides it is not.

### The guard was narrowed, not deleted

`TestGraphTablesAreAppendOnly` failed, exactly as predicted. Its **stated reason** was that a
delta cursor is a row-id watermark, so a `DELETE` would hand clients an update that skips or
repeats work. That reason is narrower than the assertion: a removals list plus an epoch is
precisely what makes deletion safe for a cursor.

It is now `TestGraphTablesAreDeletedOnlyByTheCompaction`, allowing exactly one file —
`internal/store/prune.go`, the one that bumps the epoch — and still failing anywhere else. Its
comment says what changed and why. Fixing it also caught a defect in the old version: it matched
`DELETE FROM abom_finding` as a substring of `abom_finding_node`, which is a link table and not
one of the three it protects. The new one uses a word boundary.

### The closure

A prune of a node or edge must also take:

`abom_finding_node`, `abom_alert_link`, edges referencing the node as **either endpoint**,
`abom_exposure`, `abom_blast_radius`, `abom_frontier`, `abom_observation`, `abom_surface`.

`abom_query_run` and `abom_finding` are **kept**. A run happened; a finding was really produced.
Deleting the ledger to tidy the graph would be recording that work did not occur, and it would
mean a re-declared account is re-analyzed from scratch rather than recognized as already on
record. The ledger is evidence, and the graph is a projection of what is currently in scope.

### Orphan causes, named

Pruning is not only for the re-scope. The same mechanism answers:

- a `uses:` line removed upstream, so an edge is no longer produced by any current analysis;
- a repository archived or deleted, so nothing can be fetched for it again;
- an account un-declared, so its closure is no longer anyone's;
- a reference no longer named anywhere, so a per-reference node has no reason to exist.

Each is a different predicate over the same removal. Writing them as one interface is what keeps
this from being a one-off cleanup script.

### Dry run first

Every prune explains before it acts: what would go, how much, and under which predicate. A
destructive operation whose scope is only visible afterwards is one nobody will run twice.

## Phases

All done.

1. `removed_at`, the removal predicate and a dry run that shares the removal's implementation.
2. `abom_node_live` / `abom_edge_live`, 104 reads rewritten, and
   `TestReadsGoThroughTheLiveViews` naming the three files allowed to bypass them.
3. `GraphDelta` carries removals; `mergeInto` in `graph.js` honors them.
4. `Compact` and the epoch, with the client reload path.
5. The guard narrowed to `TestGraphTablesAreDeletedOnlyByTheCompaction`.
6. `codeql-abom prune`, explaining by default, `--apply` to mark and `--compact` to delete.

## Verification

Against a **copy** of the real corpus, never the live one:

- Declare a small owned scope, prune, and check the survivors equal the closure computed
  independently in SQL. **Exact agreement.**
- `PRAGMA foreign_key_check` empty and `PRAGMA integrity_check` ok afterwards.
- A second prune removes nothing — the operation settles.
- An Explorer client holding a pre-prune cursor is told what disappeared, and its node count after
  the delta matches a fresh load exactly.
- After compaction, a client with a pre-epoch cursor reloads rather than receiving a delta.
- `abom_query_run` and `abom_finding` counts are **unchanged** by any prune. The ledger is not
  collateral.
- The wide seed: prune it, and the remaining graph is exactly what the declared scope reaches.
  This is the headline measurement and the reason the wide seed is kept until then.

## Open questions

- **Whether tombstones ever expire on their own.** A `removed_at` that nothing compacts is the
  ever-growing graph under another name. The plan's assumption is that compaction is operator-run;
  a retention policy may follow once there is evidence about how often prunes happen.
- **Whether a pruned node can come back.** It can — the boundary is recomputed, not remembered —
  but the row id will be new, so a client's history of it is not continuous. Acceptable, and worth
  stating rather than discovering.
