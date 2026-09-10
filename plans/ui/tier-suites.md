# Per-tier suite controls

**Status:** tiers are `owned`, `direct` and `transitive` by precedence; each binds a set of suites
chosen in a grid on the Discovery page, and each can be re-analyzed on its own.

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
2. **Re-analyze a selection**, not only a whole tier. The Discovery table already filters; the
   layout this lands in is `ui/discovery-config.md`.
3. **Staleness per suite.** A row says *when* it was last analyzed, not *with what*. Once the
   run's pack is joined to the row, a tier can show which of its suites are behind instead of
   offering an undifferentiated re-analysis of everything. Depends on the run's pack version being
   recorded at all; see `ql/packs.md`.
4. **The suite grid is the largest thing between a reader and the queue.** Folding it into the
   configuration box is a page-layout problem rather than a control problem, and belongs with
   `ui/discovery-config.md`.

## The constraint that must not be broken

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
