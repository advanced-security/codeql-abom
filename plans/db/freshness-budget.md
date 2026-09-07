# Complete first, then current, under budget

**Status:** planned. Depends on `db/discovery-boundary.md`, because "complete" is meaningless
until there is a boundary to be complete within.

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
| claim order | `expedited_at`, then `score DESC, depth ASC, enqueued_at ASC` |
| recheck | drain-gated, 24-hour TTL, `DefaultRecheckLimit = 50` |
| times the recheck has fired | **0 of 2,800** |
| rate limiting | `work.Limiter`, `--rate-limit-per-hour`, off by default |
| per-cycle accounting | `Stats{Processed, Skipped, Failed, Unavailable, Items, Discovered, Edges}` |
| what a cycle spent | **not recorded** |

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
2. The claim ordering, as an explicit objective with the three stages named in code.
3. Per-cycle budget accounting, recorded and served.
4. The recheck folded into the ordering, replacing the TTL gate.
5. The Discovery page states the objective and the spend, since a crawl whose priorities are
   invisible is one nobody can steer.

## Verification

- From a declared scope on a copy of the real corpus: the crawl reaches closure at depth 1 before
  spending anything at depth 2, observable in the cycle log and in the claim order.
- `checked_at > analyzed_at` becomes non-zero, and the count of rows probed per cycle is bounded
  and reported. It has been 0 for the life of the corpus.
- A fully current, fully complete closure produces a cycle that spends a bounded number of probes
  and no fetches at all.
- Budget accounting sums to the wall clock: a cycle's recorded spend and its measured duration
  agree within noise, or the accounting is measuring the wrong thing.
- The 800-of-1,997 treadmill does not reappear: a corpus of unmoved references does not re-analyze
  itself.

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
