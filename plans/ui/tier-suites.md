# Per-tier suite controls

**Status:** re-analysis and per-tier suite selection both shipped; cost guidance is still a count
rather than a duration.

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

**Per-tier suite selection.** A grid on the Discovery page, one row per suite and one column per
tier, each column headed by the number of analyzed dependencies a change would apply to. The
suites offered are read from disk at request time — `runner.AvailableSuites` over
`ql/actions/abom/src/codeql-suites` — so a suite appears by being dropped in, with no release.
Anything already bound stays offered even when it is not on disk, because a binding may name a
suite from an installed pack and dropping it from the list would make it look invented.

A change takes effect on the next round, never the current one: a round resolves its queries once
before any repository is claimed. It reaches work already analyzed only when that tier is
re-analyzed, which is what the buttons beside it are for.

## What offering every suite on disk turned up

The corpus check showed three suites offered, and the third is `abom-selfcheck.qls` — the gate
this repository runs against its *own* workflows. Filtering it out by name was the obvious fix
and is the wrong one: a compiled-in exception list is exactly the release-coupled hard-coding
this control exists to remove, and the next suite someone adds would face the same question.

It does expose a sharper edge, though. `abom-selfcheck.qls` is findings-shaped, and the BQRS lane
drops findings-shaped queries silently because the SARIF lane does not exist yet. So a tier bound
to it would look configured and analyze nothing. That is not a fault of this control — the
`direct` default already binds `abom-findings.qls` and has the same problem — but it is the
second place the missing lane now shows through, which is why it moved up the ranking.

## Pending

1. **Cost guidance that is measured, not guessed.** The control states *how many* dependencies a
   change applies to; it does not state how long. "This tier, this suite, ~N repositories at ~M
   seconds each" needs the cycle timings that are already logged and nothing surfaces.
2. **Re-analyze a selection**, not only a whole tier: the Discovery table already supports
   filtering, so acting on the current filter is the natural extension.
3. **Staleness per suite.** Once `analyzed_suite` is recorded, a tier can show *which* of its
   suites are behind, rather than an undifferentiated "re-analyze everything".
4. **The grid costs 304px and never folds**, which is now the largest thing between a reader and
   the queue. That is a page-layout problem rather than a control problem, so it lives in
   `ui/discovery-config.md`.

## Constraints worth restating

- Naming: suites are named for the query set they contain, never for the tier they default to.
  A tier binds to a *set* of suites, so no combined per-tier suite is ever needed.
- Requeueing must keep what is already known. Clearing it would blank the Explorer for as long
  as the re-run takes, which is the opposite of what re-processing is for.
- Requeueing charges no attempt: nothing failed.

## Verification

- bUnit: the control states its count; an unknown tier is refused.
- Real corpus: requeue a tier, confirm the queue fills and the counts move as predicted.
