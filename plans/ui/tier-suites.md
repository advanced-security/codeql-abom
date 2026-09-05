# Per-tier suite controls

**Status:** re-analysis shipped; choosing *what* runs is pending.

## Why this exists

The set of queries ABOM runs is meant to keep growing, and not every tier deserves the same set.
A repository under review is worth a broad pass; something reached through three intermediaries
is worth the minimum needed to place it in the graph. Today that choice is a single
`--expand-suite` flag applied to everything, so the trade-off is made once at the command line
and is invisible to whoever reads the result.

The interface has to state the cost, because pressing one of these buttons spends minutes of
cloning and CodeQL database building that nothing can take back.

## Done

Re-analysis by tier: **Re-analyze direct (522)** and **Re-analyze transitive (691)**, each
carrying its count on the control and its consequence in a tooltip. `POST
/api/discovery/requeue/{tier}` is guarded exactly like Start, for the same reason.

Tier is derived from depth (`direct` = depth ≤ 1) rather than stored, because depth is already
maintained as the shortest route from the repositories under review and a second copy could
disagree with it.

## Pending

1. **Per-tier suite selection.** A control binding each tier to a set of suites, read from
   `codeql resolve queries` at runtime so a new suite appears without a release. Depends on the
   configuration store in `plans/api/query-suites.md`.
2. **Cost guidance that is measured, not guessed.** "This tier, this suite, ~N repositories at
   ~M seconds each." The numbers exist — cycle timings are already logged — but nothing surfaces
   them at the point of decision.
3. **Re-analyze a selection**, not only a whole tier: the Discovery table already supports
   filtering, so acting on the current filter is the natural extension.
4. **Staleness per suite.** Once `analyzed_suite` is recorded, a tier can show *which* of its
   suites are behind, rather than an undifferentiated "re-analyze everything".

## Constraints worth restating

- Naming: suites are named for the query set they contain, never for the tier they default to.
  A tier binds to a *set* of suites, so no combined per-tier suite is ever needed.
- Requeueing must keep what is already known. Clearing it would blank the Explorer for as long
  as the re-run takes, which is the opposite of what re-processing is for.
- Requeueing charges no attempt: nothing failed.

## Verification

- bUnit: the control states its count; an unknown tier is refused.
- Real corpus: requeue a tier, confirm the queue fills and the counts move as predicted.
