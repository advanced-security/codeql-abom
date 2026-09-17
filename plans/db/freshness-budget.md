# Complete first, then current, under budget

**Status:** The claim order takes nothing at depth 2 while depth 1 is outstanding. Freshness
correctly still does not run: the closure is incomplete, and waiting is what the third stage means.

## Why this exists

The crawl has no objective it can state. `Queue.Claim` orders by `score DESC, depth ASC,
enqueued_at ASC` with `expedited_at` ahead of all of it, which encodes neither completeness nor
freshness — `score` is a cost estimate, so ordering by it means the crawl does whatever looks
cheapest next. The point of an objective is that each stage is *finishable*: "complete at depth 1"
is provable, and "worked on the cheapest thing available" never is.

And nothing accounts for what a cycle cost. `expand.Stats` is seven counters and no clock, and the
only duration recorded in the loop is `Status.LastRebuildMillis`. The per-repository split — fetch
3.68 s, build 3.11 s, graph lane 3.95 s, findings lane 4.56 s, **15.30 s** total — was measured by
hand once and cannot be reproduced by asking the system.

## Pending

1. **The claim ordering as an explicit objective, with the stages named in code.** Three, in order:
   unanalyzed inside the closure at the current depth; the shallowest unexplored depth; the stalest
   analyzed row inside the closure. `expedited_at` stays ahead of all three, because an operator
   asking for one repository states an objective the crawl cannot infer. The stages exist only as a
   comparison today, not as a declaration.
2. **Per-cycle budget accounting.** What a cycle spent — fetches, database builds, query-server
   evaluations, findings analyses, remote ref probes — and what each bought. It needs its own
   table: `abom_observation` has `query_run_id NOT NULL` and is keyed by node, edge or repository,
   so it is for CodeQL output about subjects, never for what a cycle cost.
3. **The recheck gated on completeness rather than on a momentarily empty queue**, and the
   Settings stating the objective and the spend. A crawl whose priorities are invisible is
   one nobody can steer.
4. **Deferred by decision: iterative deepening.** `MaxDepth` is a static flag read once and changed
   by nothing, and a crawl that deepens on its own can surprise an operator with a bill.
   `Status.MaxDepth` is reported beside completeness so "complete" can be told from "complete as
   far as we are allowed to look".

## The constraint that must not be broken

**`checked_at` means probed, and nothing else may write it.** While `Complete` set `checked_at =
analyzed_at` in one statement, `checked_at > analyzed_at` was not merely 0 but structurally
impossible, so the signal that says whether freshness works read as though it always did. A clock
that can only agree with itself is not evidence.

## Verification

- The claim order takes nothing at depth 2 until depth 1 is exhausted, across batch sizes.
- `checked_at > analyzed_at` stays 0 until the closure completes, which is the objective working.
- A row nothing has ever probed is offered before one that has, whatever their scores.
- Budget accounting records what each cycle spent and what it bought, and a database that has never
  run one reports no cycles rather than zero spend.
- A row analyzed moments ago is not probed. Without a second clock, **800 of 1,997** mutable rows
  sit on a 24-hour treadmill indistinguishable from real drift.

## Open questions

- **What "budget" is denominated in** — wall clock, GitHub API calls, CodeQL invocations, or all
  three. Today only rate-per-hour exists; record all three before anything is asked to respect one.
- **Near-real-time.** Nothing here watches GitHub for changes; it re-probes on its own schedule.
  Webhooks or the events API would be a different plan, and polling should be honest first.
