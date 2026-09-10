# Code quality, measured by our own tool

**Status:** `make quality` analyzes actions, go, csharp and javascript against pinned packs, names
every rule that fires, and fails above zero findings. `self-audit.yml` enforces the same threshold.

## Why this exists

A supply-chain analysis tool that does not analyze itself is making a claim it has not tested on
the one codebase it can always reach. That gate is on, and the count it enforces is zero.

What the gate cannot see is complexity: all 190 queries in those suites are problems, and the
metric queries are left out. **50 production functions exceed cyclomatic complexity 10, total
excess 204** — worst `runServe` at 25, `Supervisor.cycle` at 24, `scopePreview` and `runExpandAll`
at 20. Three complexity linters run as a ratchet and report zero at their defaults.

## Pending

1. **Reduce Go complexity**, led by excess rather than by the worst single function.
   **`Supervisor.cycle` is blocked:** it is the crawl loop, its ordering is load-bearing —
   reclassification runs before the sweep — and extracting phases reorders error paths no unit test
   exercises, so it needs a session that can run a real crawl. The ~40 two-line serve handlers are
   **not** part of it: a wrapper would trade forty readable handlers for one indirection.
2. **Decomposition, which depends on 1.** One store seam for the graph read models; the Explorer's
   filter panel and Discovery's three concerns as components, so the counts sentence and the empty
   state stop being written out four and five times. `DiscoveryStatus` also reaches the interface
   carrying five fields no page renders — unsurfaced facts rather than dead code.
3. **Complexity as a query we own**, over `cyclomaticComplexity` at the documented > 10 threshold.
   Blocked on 1: fifty findings against a threshold of zero forces either a red build or a
   threshold set where the code happens to be.

## The constraint that must not be broken

**Total excess over the threshold is the metric that moves, not the count of functions above it.**
The count is insensitive to depth: taking one function from 22 to 11 leaves 50 over 10 and scores
nothing, which is how a ratchet stops being believed. Quote both; only one is evidence.

**The JavaScript side has no complexity guard at all**, so its zero is unmeasured rather than
clean — as is the dead-export case, since CodeQL treats an `export` nothing imports as public API.

## Verification

- `make verify` green at every step, unfiltered, and `make quality` at zero.
- Total excess re-measured either side of every change; the count over 10 may legitimately not move.
- Byte-identical API responses for each behavior-preserving change, on a database **copy**.
