# Per-tier suite controls

**Status:** tiers are `owned`, `direct` and `transitive` by precedence; each binds a set of suites
chosen in a grid in Settings, and each can be re-analyzed on its own.

## Why this exists

The set of queries ABOM runs is meant to keep growing, and not every tier deserves the same set. A
repository under review is worth a broad pass; something reached through three intermediaries is
worth the minimum needed to place it in the graph. An owned action is a third case again: it is
the one a reader can change today, which is where custom or additional queries actually pay.

The interface has to state the cost, because pressing one of these buttons spends minutes of
cloning and CodeQL database building that nothing can take back. Today it states a count. On the
reference corpus the tiers hold **16 owned / 439 direct / 682 transitive**, and a count is not a
duration.

## Pending

1. **Cost guidance measured, not guessed.** "This tier, this suite, ~N repositories at ~M seconds
   each" needs the cycle timings that are already logged and that nothing surfaces. A count says
   how much work there is, never how long a reader will be waiting for it.
2. **Re-analyze a selection**, not only a whole tier. The mechanism belongs to
   [`api/on-demand-analysis.md`](../api/on-demand-analysis.md), which resolves a scope to a database
   set and runs a chosen plan over it; what is owed here is the control, and the rule that it states
   the size of what it is about to re-run before it runs it.
3. **Staleness per suite.** A row says *when* it was last analyzed, not *with what*. Once the
   run's pack version is joined to the row, a tier can show which of its suites are behind instead
   of offering an undifferentiated re-analysis of everything. Depends on the pack version being
   recorded at all; see [`db/query-catalog.md`](../db/query-catalog.md).
4. **Decide what a section whose read failed does.** A panel that could not reach
   `/api/discovery/suites` has no setting to summarize. It states the failure rather than folding
   over it; what it does not yet do is offer a way to try again.

## The constraint that must not be broken

- **A collapsed section still states what it is set to.** The rule against printing a number nobody
  measured applies to controls as much as to data: a fold that hides the binding turns "direct runs
  findings" into something a reader has to go looking for, and a reader who does not know to look
  assumes the default. The header carries the current setting; only the means of *changing* it
  folds away. Collapsed is a sensible default only because the summary is not.
- **The tiers are exclusive, and precedence is what makes them so**: `owned` first, then `direct`
  at depth ≤ 1, then `transitive`. A dependency counted twice makes the three counts exceed the
  queue. `tierWhere` feeds both the counts and the requeue, or the two drift.
- **With no verified account there is no Owned column.** A column reading 0 dependencies asserts
  the operator owns none, which nothing established.
- **A suite already bound is still offered even when it is not on disk**, because a binding may
  name a suite from an installed pack and dropping it would make the binding look invented.
- **Suites are named for the query set they contain, never for the tier they default to.** A tier
  binds a *set*, so no combined per-tier suite is ever needed.
- **Requeueing keeps what is already known and charges no attempt.** Clearing first would blank
  the Explorer for as long as the re-run takes, and nothing failed.

## Verification

- bUnit: a control states its count, and an unknown tier is refused.
- Real corpus: requeue a tier, confirm the queue fills, the counts move as predicted, and the
  three tier counts still sum to the queue.
- A change takes effect on the next round and never the current one — a round resolves its queries
  once, before any repository is claimed.
