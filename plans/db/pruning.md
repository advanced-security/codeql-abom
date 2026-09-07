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

### The guard is narrowed, not deleted

`TestGraphTablesAreAppendOnly` will fail. Its **stated reason** is that a delta cursor is a row-id
watermark, so a `DELETE` would hand clients an update that skips or repeats work. That reason is
narrower than the assertion: a removals list plus an epoch is precisely what makes deletion safe
for a cursor.

So the test is narrowed to what its reasoning actually protects — no unannounced deletion — and
says so in its own comment. It is not deleted, and the rule that a blocking guard gets narrowed
rather than removed is exactly what stops this from being how the invariant quietly dies.

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

1. `removed_at`, and every read filtered. Nothing removes anything yet; the column is inert and
   the reads are proven not to change.
2. `GraphDelta` carries removals; the browser module honors them. `mergeInto` in `graph.js` is the
   place a removal is dropped silently, the same way it drops an uncopied metric field.
3. The removal predicates and the FK closure, with dry-run output.
4. Compaction and the epoch, with the client reload path.
5. `TestGraphTablesAreAppendOnly` narrowed, with its reasoning rewritten rather than removed.

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
