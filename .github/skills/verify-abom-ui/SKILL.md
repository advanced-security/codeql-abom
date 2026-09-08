---
name: verify-abom-ui
description: 'End-to-end verification of the codeql-abom user interface and UX, including visual review of screenshots. Use when testing, reviewing or debugging the ABOM UI; checking the force-directed dependency graph for readability or hairballing; confirming the interface honours ABOM architectural invariants (no unearned vulnerability verdicts, severity x reach scoring, seed vs upstream, direct vs transitive, visible coverage); cross-checking rendered numbers against the /api endpoints; or auditing color, contrast and label legibility. REQUIRES a vision-capable model that can actually view screenshot images.'
argument-hint: 'Optionally name a page, route or invariant to focus on (e.g. "remediation page", "risk force view", "coverage strip")'
---

# Verify the ABOM interface

ABOM's interface carries claims about an organization's security posture. Most of what can go
wrong with it is invisible to a DOM assertion: a force layout that has degenerated into a
hairball, a badge that asserts more certainty than the data supports, a severity encoding that
only works for people who see color. Those need to be looked at.

## This skill requires vision

You must be able to **view** raster screenshots, not merely capture them.

Confirm before proceeding: capture one screenshot and describe something you could only know by
looking at it — the position of a specific node, the color of a badge, which label is truncated.

If you cannot see images, **stop and say so**. Do not infer appearance from the DOM and report it
as a visual finding. A fabricated UX judgement is worse than no judgement, because it will be
trusted. Fall back to the DOM-only checks in step 4 and label the report `PARTIAL — no vision`.

## Prerequisites

```bash
cd /path/to/codeql-abom
make start                       # builds the binary, publishes the interface, serves it
make status                      # confirms it is up and reports what discovery is doing
```

`make start` rebuilds both halves every time. Serving a stale interface against a new binary
looks like an interface bug and costs far more to work out than the rebuild does. Override
`ABOM_DB` and `ABOM_ADDR` rather than invoking `serve` by hand, and run one server: two against
one database is two writers, which the store forbids.

Use a database with real ingested data. An empty database hides every interesting failure —
truncation, hub occlusion, label overflow, wrapping — because nothing is dense enough to break.

To review a *running* expansion rather than a static picture, start it from the Discovery page
or with `make start ABOM_SERVE_FLAGS=--expand`, and read `make logs` alongside the screenshots:
one line per cycle and per failure. Discovery never starts on its own.

While discovery runs it is building CodeQL databases on every core. Browser work alongside it is
fine, but do not run `make verify`, `make ql` or `make selfcheck` at the same time: every
`codeql` invocation shares one local compilation cache, and they will contend for it.

## Procedure

### 1. Establish ground truth from the API

The API is the oracle. Record the numbers before looking at any pixels, so the comparison later
is against fact rather than expectation.

```bash
curl -sS localhost:8099/api/summary | python3 -m json.tool
curl -sS "localhost:8099/api/remediation?top=10"
curl -sS "localhost:8099/api/graph?focus=actions/checkout&hops=1&granularity=repository" \
  | python3 -c "import json,sys; g=json.load(sys.stdin); print(len(g['nodes']), len(g['edges']), g['truncated'])"
```

### 2. Open the interface and enumerate routes

`open_browser_page` on the root, then `read_page` to get the structure. Discover routes from the
navigation rather than assuming them; the page set changes as the UI grows.

### 3. Capture the matrix

Work through [the capture matrix](./references/capture-matrix.md). At minimum: landing page,
each force view, one expanded remediation row, and one drill-down.

Capture at a realistic viewport (1440×900). A maximised 4K window hides exactly the crowding
problems worth finding.

### 4. Read the DOM for what the DOM is good at

`read_page` for structure, roles, focus order and actionable elements. Use this — not a
screenshot — for anything you intend to click. Check: headings nest sensibly, interactive
elements are reachable, tables have headers, nothing conveys state through `title` alone.

An unpaginated grid defeats both readings: 2,548 rows made the Bill of Materials snapshot 2.7 MB
and its screenshot unreadable. Extract the figures with `page.evaluate` to get unblocked — and
record the missing pagination as a finding rather than working around it in silence.

### 5. Look at the screenshots

Now view each capture and judge it. Go through the invariant audit below and the
[UX rubric](./references/ux-rubric.md). For every finding, quote what in the image led you to it.

### 6. Cross-check rendered numbers against the API

Read the figures off the screenshot and compare to step 1. Mismatches here are real bugs:
truncated counts, rounding that changes meaning, a total that silently excludes upstream repos.

## Invariant audit

These derive from ABOM's architecture. A violation is a correctness bug, not a style note,
because each one makes the interface assert something the data does not support.

| # | Invariant | Visible violation | Why it matters |
| --- | --- | --- | --- |
| 1 | Verdicts come only from CodeQL rule IDs | A "vulnerable" badge on an action without its vulnerable range **and** the observed refs adjacent | `actions/vulnerable-action` is exact-match only and found nothing across 204 real repos; most refs are floating tags nothing can adjudicate. The UI must not manufacture certainty |
| 2 | Priority is severity × reach, nothing else | A single risk number with no visible components, or CVSS/EPSS folded into the score | The number stops being explainable, and a reader cannot disagree with it |
| 3 | Declared ownership ≠ everything else | Owned and dependency-only repositories styled identically, or a repository count larger than what was declared | Implies the operator owns repositories they do not. `origin='seed'` is data provenance, never a claim of ownership |
| 4 | Direct ≠ transitive | One "affected: N" figure with no split | They are different work: one is a version bump here, the other waits on someone else |
| 5 | Provenance is reachable | A finding with no rule ID, or an advisory with no GHSA, within one interaction | Every claim must trace to the query or advisory that made it |
| 6 | Say what is not known | No coverage indicator on screen | Silence reads as completeness. Coverage belongs in a persistent strip |
| 7 | References are verbatim | A ref rewritten, normalized or truncated without a way to see the original | The exact ref is the evidence a reader judges with |
| 8 | An absent measurement is not zero | A node added since the last rebuild drawn at the smallest size, in a view where size means reach | It asserts "nothing depends on this" when nobody has looked. Unmeasured needs its own treatment |
| 9 | Every distinguishable queue state is visible | Work waiting on a retry shown identically to work nothing has touched; Pause leaving the status unchanged while a cycle finishes | A control that appears to do nothing, and a queue that hides what it has already spent |
| 10 | The interface speaks the reader's language | "reach", "cycle", "claimed", "frontier", "corpus", "hops", "folded", "unplaced" in user-facing text | These are names for internal machinery. A security engineer should not have to learn the scheduler to read the page |
| 11 | Owning a repository ≠ having a database for it | A repository shown as un-analyzable, or left out of coverage, because code scanning is off there | ABOM builds its own actions databases; `has_database` is provenance, not eligibility. Treating it as a limit understates what is owned |

For each: **PASS**, **FAIL** with the screenshot region, or **N/A** if that surface is absent.

## Force-directed graph checks

Only a viewer can make these calls.

- **Hairball test.** Pick any node and try to follow one edge to its other end. If edges are
  visually inseparable, the scope is too large — the fix is repository granularity or fewer
  hops, not a prettier layout.
- **Hub occlusion.** In a high-fan-in view, is the focus node still findable, or buried under
  its neighbors?
- **Semantic force legibility.** In the Risk view, name the highest-priority node from position
  alone. If you cannot, the semantic force is not doing its job.
- **One semantic force at a time.** If position appears to encode two things at once, that is a
  design regression: position must have exactly one meaning.
- **View-switch continuity.** Screenshot the same scope before and after switching force, then
  pick a node in the first image and find it in the second. Angular position is meant to be
  preserved so a switch reads as re-orientation rather than reshuffle. Report whether you could
  actually track it.
- **Determinism.** Reload the same scope and compare screenshots. Layout should be identical;
  drift destroys spatial memory.
- **Truncation honesty.** When the API reports `truncated: true`, the view must say so.

## Report format

```markdown
## ABOM UI verification — <date>, <commit>
Vision: AVAILABLE | UNAVAILABLE (report is PARTIAL)
Data: <db path>, <seed repos> repos / <nodes> nodes

### Invariants
1..7 with PASS / FAIL / N/A and evidence

### Force graph
Findings per check, each citing the screenshot

### API vs rendered
| Metric | API | Screen | Match |

### Issues
Severity, what was seen, where, suggested fix
```

Lead with invariant failures. A layout nit and a UI that asserts an unearned verdict are not the
same class of problem, and the report should not flatten them.
