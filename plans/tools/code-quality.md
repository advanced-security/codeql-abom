# Code quality, measured by our own tool

**Status:** `make quality` analyzes actions, go, csharp and javascript against pinned packs, names
every rule that fires, and fails above zero findings. `self-audit.yml` enforces the same threshold.

## Why this exists

A supply-chain analysis tool that does not analyze itself is making a claim it has not tested on
the one codebase it can always reach. That gate is on, and the count it enforces is zero.

What the gate cannot see is complexity: all 190 queries in those suites are problems, and the
metric queries are left out. **112 production functions exceed cyclomatic complexity 10, total
excess 310** (`gocyclo -over 10`, non-test Go) — worst `Store.Migrate` at 29, `Supervisor.cycle` at
24, `exposureOf` at 21. Three complexity linters run as a ratchet and report zero at their
defaults.

## Pending

1. **Reduce Go complexity**, led by excess rather than by the worst single function. The next
   tranche is the ~100 functions between 11 and 16. **`Supervisor.cycle` is blocked:** its ordering
   is load-bearing — reclassification runs before the sweep — and extracting phases reorders error
   paths no unit test exercises, so it needs a session that can run a real crawl. **`Store.Migrate`
   and the migration helpers** run against real older databases and move only with a migration
   test per branch. The lock-binding walks (`exposureOf`, `admits`, `reachOf`) move only with the
   agreement tests. The ~40 two-line serve handlers are **not** part of it: a wrapper would trade
   forty readable handlers for one indirection.
2. **Decomposition, which depends on 1.** One store seam for the graph read models; the Explorer's
   filter panel and Discovery's three concerns as components, so the counts sentence and the empty
   state stop being written out four and five times. `DiscoveryStatus` also reaches the interface
   carrying five fields no page renders — unsurfaced facts rather than dead code.
3. **Complexity as a query we own**, over `cyclomaticComplexity` at the documented > 10 threshold.
   Blocked on 1: over a hundred findings against a threshold of zero forces either a red build or a
   threshold set where the code happens to be.

## The constraint that must not be broken

**Our own analysis evaluates what Code Scanning evaluates, or it is weaker than the thing that
reports.** Default Setup for this repository scans with **remote and local sources**, which is the
right choice for a tool whose inputs are the filesystem, `argv` and the environment. `selfcheck`
took CodeQL's default of remote sources alone, and the gap was not theoretical: two `go/path-injection`
alerts sat on a pull request while `make quality` reported **0 findings**, and the same suite run
with `--threat-model=local` reproduced one of them at the exact line. `Runner.ThreatModel` is set
to `local` for selfcheck and left empty for the crawl, because what ABOM records about somebody
else's action is a product decision rather than a CI setting.

**Total excess over the threshold is the metric that moves, not the count of functions above it.**
The count is insensitive to depth: taking one function from 22 to 11 leaves the count unchanged and scores
nothing, which is how a ratchet stops being believed. Quote both; only one is evidence.

**The JavaScript side has no complexity guard at all**, so its zero is unmeasured rather than
clean — as is the dead-export case, since CodeQL treats an `export` nothing imports as public API.

## Verification

- `make verify` green at every step, unfiltered, and `make quality` at zero.
- Total excess re-measured either side of every change; the count over 10 may legitimately not move.
- Byte-identical API responses for each behavior-preserving change, on a database **copy**.
