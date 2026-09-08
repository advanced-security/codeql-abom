# Complete first, then current, under budget

**Status:** implemented, with freshness correctly still not running — the closure is incomplete,
and waiting is what stage 3 means. Iterative deepening is explicitly out and stays unbuilt.

## What the corpus showed

- **The claim order was the whole of it.** At every batch size the old order sent **4 items to
  depth 2 while depth 1 still had 205 outstanding**, and at a 400-item batch it reached **depth 3
  (12 items) while depth 2 was incomplete**. The new order sends **0** beyond the shallowest
  incomplete depth until it is exhausted. That is the objective, and it is visible in one query.
- **`checked_at` was the diagnosis and not the cause.** Making it mean *probed* turns
  `checked_at > analyzed_at` from structurally impossible into a real signal, but the reason the
  recheck has never fired is the gate plus **793 pending rows**: the queue has never drained. The
  due population is **1,997 under both the old and the new predicate**, because everything in this
  corpus was analyzed more than a day ago.
- **So freshness still does not run here, and that is correct.** Completeness is not satisfied —
  depth 1 holds 205 unanalyzed rows — and freshness is the third stage. The plan's own verification
  ("`checked_at > analyzed_at` becomes non-zero") **cannot be met on this corpus without first
  completing the closure**, which is the objective working rather than failing. What is now true is
  that the population is ordered never-probed-first and bounded at 50 a cycle, so when it does run
  it works off a backlog instead of re-probing the same valuable few.
- **The second clock earns its place only on a fresh corpus.** Adding `analyzed_at` to the due
  condition changes nothing here (1,997 either way) and is what stops a corpus analyzed minutes
  ago from immediately re-probing itself — the 800-of-1,997 treadmill.

## What reconciling against the code corrected

- **Iterative deepening never landed.** This plan says stage 2 "is where iterative deepening from
  `db/discovery-boundary.md` advances the cap". That plan shipped the closure and the queue
  boundary; it did not ship a cap that advances. `MaxDepth` is a static flag — `--max-depth`,
  `DefaultMaxDepth = 5` — read once by `Queue.maxDepth()` and never changed by anything. So stage
  2 has no cap to advance and has to bring its own.
- **`checked_at` is worse than "does not mean probed" — it is written to equal `analyzed_at`
  exactly.** `Complete` sets `analyzed_at = now, checked_at = now` in one statement, so
  `checked_at > analyzed_at` is not merely 0, it is **structurally impossible** until something
  calls `Touch`. Confirmed on the corpus: **2,818 rows have `checked_at`, all 2,818 equal to
  `analyzed_at`, 0 greater.**
- **The recheck's "immutable refs are excluded" is done by a comparison that reads as a mistake.**
  `DueForRecheck` filters `analyzed_ref <> resolved_sha` — a ref *name* against a *commit* — which
  is true for every tag and branch and false only when the ref is itself a SHA. It works, and
  nothing says so. **1,997 of 2,800 done rows are eligible**, matching the plan's figure.
- **No cycle records what it spent, and `abom_observation` is not the place to put it.** That
  table exists and looks like a generic fact store, but `query_run_id` is `NOT NULL` and it is
  keyed by node, edge or repository: it is for CodeQL output about subjects, not for what a crawl
  cycle cost. Budget accounting needs its own table.
- **The per-repository timings the plan quotes are not instrumented anywhere.** `expand.Stats` is
  seven counters and no clock; the only duration recorded in the whole loop is
  `Status.LastRebuildMillis`. The 15.30 s split was measured by hand once and cannot be
  reproduced by asking the system.

## Why this exists

The crawl has no objective. `Queue.Claim` orders by `score DESC, depth ASC, enqueued_at ASC` with
`expedited_at` ahead of all of it, which encodes neither completeness nor freshness — `score` is a
cost estimate, and ordering by it means the crawl does whatever looks cheapest next.

Freshness is worse than unordered; it has never happened. The drift recheck is wired, gated on a
drained queue plus a 24-hour TTL, and the honest signal for whether it has ever run —
`checked_at > analyzed_at` — is **0 rows across all 2,800**. A mechanism that has never fired is
not a mechanism, and its first firing would have put **800 of the 1,997 mutable rows** on a
permanent 24-hour treadmill, which is why it needs an objective rather than a timer.

And nothing accounts for what a cycle cost. The per-repository split is known — fetch 3.68 s,
build 3.11 s, graph lane 3.95 s, findings lane 4.56 s, **15.30 s** total — but no cycle records
what it spent or what that bought, so "under budget" is a phrase rather than a constraint.

## What is already true

| | |
| --- | --- |
| claim order | `expedited_at IS NULL`, then `score DESC, depth ASC, enqueued_at ASC` |
| recheck | drain-gated, 24-hour TTL, `DefaultRecheckLimit = 50` |
| times the recheck has fired | **0 of 2,800**, and `Complete` makes it unmeasurable |
| depth cap | static `--max-depth`, default 5; nothing advances it |
| rate limiting | `work.Limiter`, `--rate-limit-per-hour`, off by default |
| per-cycle accounting | `Stats{Processed, Skipped, Failed, Unavailable, Items, Discovered, Edges}` |
| what a cycle spent | **not recorded**, and no clock anywhere in the loop |

The skip machinery is done and measured, which is what makes an objective affordable: a
re-analysis that is already on record costs a remote ref resolution, and content equivalence means
a moved tag over unchanged content costs a fetch rather than a rebuild. Freshness is cheap now in
a way it was not when the recheck was written.

## The design

### The objective, stated in order

1. **Unanalyzed inside the closure at the current depth.** Completeness at the depth already being
   worked, before anything deeper or anything already known.
2. **The shallowest unexplored depth.** This is where iterative deepening from
   `db/discovery-boundary.md` advances the cap.
3. **The stalest analyzed row inside the closure.** Freshness, once completeness is satisfied.

`expedited_at` stays ahead of all three, because an operator asking for one repository is stating
an objective the crawl cannot infer.

The point of the ordering is that each stage is *finishable*. "Complete at depth 1" is a claim the
queue can prove; "worked on the cheapest thing available" never is.

### Freshness that actually runs

The recheck stops being a TTL and becomes the third stage of the ordering, which means it competes
with everything else on the same terms rather than firing on a clock. A row is due when the
reference it names could have moved and nothing has probed it since — and `checked_at` has to
start meaning *probed*, which today it does not, since `Complete` sets it at analysis time.

That correction is small and load-bearing: without it the signal that says whether freshness is
working reads as if it always is.

The cost of a probe is already known to be a fraction of a clone, and 803 of 2,800 rows are
SHA-pinned and cannot move at all, so the population is smaller than it looks.

### Budget accounting

A cycle records what it spent — fetches, database builds, query-server evaluations, findings
analyses, remote ref probes — and what each bought: repositories analyzed, skipped, settled by
content equivalence, discovered.

This is what makes "under budget" checkable. It also makes the iterative-deepening decision
observable: advancing the cap is a choice with a price, and the price should be visible before it
is paid.

## Phases

1. `checked_at` means probed. Small, and the measurement everything else is judged by.
2. The claim ordering, as an explicit objective with the stages named in code.
3. Per-cycle budget accounting, recorded and served.
4. The recheck gated on completeness rather than on a momentarily empty queue.
5. The Discovery page states the objective and the spend, since a crawl whose priorities are
   invisible is one nobody can steer.

**Iterative deepening is not here.** The plan assumed `db/discovery-boundary.md` had shipped a cap
that advances; it had not, and building one is a separate decision about whether a crawl may
deepen on its own and surprise an operator with a bill. `Status.MaxDepth` is now reported beside
completeness so "complete" can be told from "complete as far as we are allowed to look", which is
the precondition for that decision rather than the decision itself.

## Verification

- From the real corpus: the claim order takes nothing at depth 2 until depth 1 is exhausted.
  **Measured at batches of 20, 50, 100, 205 and 400.**
- `checked_at` is written only by a probe, so `checked_at > analyzed_at` is a signal rather than
  an impossibility. **It stays 0 until the closure completes, which is the objective working.**
- A row nothing has ever probed is offered before one that has, whatever their scores.
- Budget accounting records what each cycle spent and what it bought, and a database that has
  never run one reports no cycles rather than zero spend.
- The 800-of-1,997 treadmill does not reappear: a row analyzed moments ago is not probed.

## Open questions

- **Whether the depth cap rises automatically.** "As up to date as possible under budget" implies
  it should. A crawl that deepens on its own is also one that can surprise an operator with a
  bill. The plan's assumption is automatic with the spend visible and a cap that can be pinned;
  say so if that is wrong.
- **What "budget" is denominated in.** Wall clock, GitHub API calls, CodeQL invocations, or all
  three. Today only rate-per-hour exists. The accounting phase should record all three before
  anything is asked to respect one.
- **Near-real-time.** The original framing asks for adaptation to dependency changes in near-real
  time. Nothing here watches GitHub for changes; it re-probes on its own schedule. Webhooks or the
  events API would be a different plan, and this one should make the polling honest first.
