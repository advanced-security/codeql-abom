# A backup is a command, not a thing everybody is told to remember

**Status:** The correct incantation is written down in three places and implemented in none.

## Why this exists

`.github/instructions/working-style.instructions.md` tells a reader to run
`sqlite3 abom.db ".backup abom-before.db"` and warns that **`cp` does not read the WAL**, so a file
copy silently drops everything committed since the last checkpoint and the baseline is quietly
wrong. `make e2e` prints the same warning when it refuses to run against the working database.
Several plans ask for a before/after diff that depends on getting it right.

Nothing in the tool does it. Every operator re-derives it from documentation, and the failure mode
of getting it wrong is not an error — it is a diff against a baseline that is missing the most
recent work, which looks exactly like a change that did nothing.

There is a second reason, and it is the one that makes this more than a convenience. A restored
database is not the database the clients were reading. `abom_graph_generation` carries a generation
and an epoch precisely so a client holding a delta cursor can be told to start again, and a restore
moves that history backwards underneath anybody connected. Nothing currently bumps it.

## Pending

1. **A `backup` subcommand**, using SQLite's own online backup so it is safe against a running
   server, plus a `make backup` target that names the file after the moment it was taken. Depends
   on nothing.
2. **Bump the epoch on restore**, so every client reloads rather than merging a delta against a
   generation that no longer means what it did. Depends on nothing, and is the half that is a
   correctness fix rather than an ergonomic one.
3. **Refuse to restore over a running server.** The store forbids two writers, and a restore is the
   most destructive writer there is. Depends on 1.
4. **Retire the incantation from the prose it currently lives in** once the command exists, so
   there is one way to do it rather than two. Depends on 1.

## What a backup is, and is not

**It is of the evidence.** `abom_query_run`, `abom_ingest_ledger`, `abom_ref_resolution`,
`abom_finding` and the queue are what cannot be recomputed — they are measurements of a world that
has since moved. The graph is a projection of them and can be rebuilt.

**A graph export is not a backup**, and nothing in [`ui/graph-layout.md`](../ui/graph-layout.md)
should be mistaken for one. Restoring a projection produces a database that draws the same picture
and has forgotten every analysis that produced it, which is worse than having nothing because it
looks complete.

## The constraints that must not be broken

**`cp` is not a backup.** The database runs in WAL mode. Any path that copies the file without
checkpointing produces a baseline that is missing recent commits and reports no error.

**A restore is not a merge.** It replaces. There is no partial restore, no per-table restore and no
reconciliation with what is already there — each of those is a way to produce a database whose
ledger and graph disagree about what was analyzed.

**A backup command may not write to the working database.** `make corpus` and `make e2e` both
already refuse to run against `ABOM_DB`, and this refuses in the same direction: a restore names
its target explicitly and never defaults to the live one.

## Verification

- A backup taken while a crawl is writing opens cleanly and its row counts match the source at the
  moment it was taken.
- The same backup taken with `cp` is shown to differ, because a test that never sees the failure it
  exists to prevent is not protecting anything.
- After a restore, a client holding a pre-restore cursor is told to reset rather than being served
  a delta.
- Restoring a database and re-running the aggregate pass reproduces reach, exposure and blast
  radius identically, which is what "the graph is a projection" has to mean in practice.
