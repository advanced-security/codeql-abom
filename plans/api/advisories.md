# Advisory enrichment

**Status:** the crawl cycle refreshes advisories under an age ceiling and a minimum interval, and
blocks for the duration of the refresh.

## Why this exists

Advisories are published whether or not ABOM analyzed anything today, so the answer ages on its
own. A dependency discovered now is matched against an advisory set fetched earlier, and nothing on
screen says how much earlier. "Has advisories" therefore reads as a current fact when it is a
statement about a moment nobody can see.

The refresh also sits inside the cycle. One pass over 936 packages costs 19 paced requests and 109
seconds with the cycle stopped throughout — tolerable at a fifteen-minute floor, and not tolerable
if the package list grows an order of magnitude.

## Pending

1. **Surface the fetch age** wherever an advisory-derived number appears, so a stale answer reads
   as stale rather than as none.
2. **Take the refresh off the cycle.** Pacing is fixed by GitHub's rate limit — 600 requests an
   hour authenticated, 60 unauthenticated — so the cost cannot be optimized away, only moved.
   Moving it introduces a second writer, so it needs the single-writer rule in
   `.github/instructions/store-sqlite.instructions.md` thought through rather than a goroutine.

## The constraint that must not be broken

**Advisories enrich; they never adjudicate.** A GHSA is stored as an attributed external statement,
the way a CodeQL result is attributed to a rule. Never derive "this action is vulnerable" from an
advisory alone: the observed reference may sit outside the vulnerable range. Report the advisory,
its range and the observed refs verbatim, side by side.

Declining to judge is the value, not a hedge. `actions/vulnerable-action` is exact-match only
against a curated list and finds nothing across 204 real repositories, because most usage is
floating tags no exact-match model can resolve. The advisory layer covers that gap precisely by
refusing to decide.

## Verification

- A cycle that settles new dependencies moves `advisoriesFetchedAt`, and the age it moves to is
  visible wherever the numbers are.
- Rate-limit exhaustion surfaces its remedy rather than a bare 403.
- No column anywhere records a verdict; `TestSchemaHasNoVerdictColumns` still passes.
- A retraction is carried rather than filtered, and an advisory with no EPSS stays unscored rather
  than becoming a probability of zero.
