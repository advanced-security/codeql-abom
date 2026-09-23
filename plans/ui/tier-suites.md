# Per-tier suite controls

**Status:** tiers are `owned`, `direct` and `transitive` by precedence; each binds a set of suites
chosen in a grid on the CodeQL page's Suites tab, and each can be re-analyzed on its own from
Settings.

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
   how much work there is, never how long a reader will be waiting for it. **This plan owns the
   number for every caller of it**, not only for a tier: the database resolution knows how many
   databases exist and how many would have to be built, and
   [`api/on-demand-analysis.md`](../api/on-demand-analysis.md) item 5 states it before spending it.
   One measurement, three consumers.
2. **Staleness per suite.** A row says *when* it was last analyzed, not *with what*. The pack
   version is now recorded on every run, so joining it to the row is the remaining work: a tier can
   then show which of its suites are behind instead of offering an undifferentiated re-analysis of
   everything. A query version is recorded too and is the finer signal — a library edit moves it
   without moving the pack version. See [`db/query-catalog.md`](../db/query-catalog.md).
3. **Decide what a section whose read failed does.** A panel that could not reach
   `/api/discovery/suites` has no setting to summarize. It states the failure rather than folding
   over it; what it does not yet do is offer a way to try again.
4. **Binding and requeue are on two pages.** The grid is on the CodeQL page and the tier requeue it
   calls for is in Settings, so a reader who binds a suite has to go elsewhere to apply it to what
   is already analyzed. The alternatives are: a requeue beside each tier column; or a link from the
   grid's caption to the requeue. Depends on nothing but the decision.

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
