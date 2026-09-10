# What no longer belongs is removed, provably

**Status:** Built. Removal is a tombstone the delta reports, reads go through views that cannot be
opted out of, and a separate compaction reclaims the space behind an epoch that reloads clients.

## Why this exists

Dependency relationships change. A `uses:` line is deleted, a repository is archived, a tag stops
being referenced, an account is un-declared. The graph tables are append-only and a delta cursor is
a row-id watermark, so without a removal mechanism a client is never told that something
disappeared and holds it for ever.

Each cause is a different predicate over the same removal — a `uses:` line no current analysis
produces, a repository nothing can be fetched for again, an account no longer anyone's, a reference
no longer named anywhere. Writing them as one interface is what keeps this from being a one-off
cleanup script.

## Pending

1. **Whether tombstones ever expire on their own.** A `removed_at` that nothing compacts is the
   ever-growing graph under another name. Compaction is operator-run today; a retention policy
   needs evidence about how often prunes happen before it can name a number.
2. **Say that a pruned node may come back.** It can — the boundary is recomputed, not remembered —
   but the row id will be new, so a client's history of it is not continuous. Acceptable, and worth
   stating in the delta contract rather than being discovered by a client that kept a row id.

## The constraints that must not be broken

**The ledger is not collateral.** `abom_query_run` and `abom_finding` survive every prune. A run
happened and a finding was really produced; deleting them to tidy the graph would record that work
did not occur, and it would mean a re-declared account is re-analyzed from scratch rather than
recognized as already on record. The ledger is evidence; the graph is a projection of what is
currently in scope.

**Only the compaction may delete, and it bumps the epoch in the same breath.** A `DELETE` anywhere
else hands a client a delta that skips row ids.
`TestGraphTablesAreDeletedOnlyByTheCompaction` allows exactly one file and fails anywhere else.

**A prune explains before it acts** — what would go, how much, and under which predicate. A
destructive operation whose scope is only visible afterwards is one nobody will run twice.

**The removal closure includes edges referencing a node as *either endpoint*.** The obvious query
filters by `repository_row_id` and misses them, and foreign keys then refuse the delete.

## Verification

Against a **copy** of the real corpus, never the live one:

- Declare a small owned scope, prune, and check the survivors equal the closure computed
  independently in SQL.
- `PRAGMA foreign_key_check` empty and `PRAGMA integrity_check` ok, after marking and after
  compaction.
- A second prune removes nothing, with nothing else changing in between. Settling is about a fixed
  declaration: expect a non-zero queued count whenever discovery has run since the last prune.
- A client holding a pre-prune cursor is told what disappeared, and its node count after the delta
  matches a fresh load exactly. A client with a pre-epoch cursor reloads instead.
- `abom_query_run` and `abom_finding` counts are unchanged by any prune.
- A count of what a prune costs names repositories that actually lose something. A count that
  ignores tombstones reports work that no longer exists, and one that counts every repository
  holding an out-of-closure row includes those kept alive entirely from inside it.
