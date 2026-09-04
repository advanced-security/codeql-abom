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
cd /path/to/codeql-abom && go build -o /tmp/codeql-abom ./cmd/codeql-abom
/tmp/codeql-abom serve --db /tmp/abom-real/abom.db --addr 127.0.0.1:8099 --ui-dir <published-wwwroot>
```

Use a database with real ingested data. An empty database hides every interesting failure —
truncation, hub occlusion, label overflow, wrapping — because nothing is dense enough to break.

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
| 3 | Seed ≠ upstream | Repositories under review and dependency-only repositories styled identically | Implies the organization owns repositories it does not |
| 4 | Direct ≠ transitive | One "affected: N" figure with no split | They are different work: one is a version bump here, the other waits on someone else |
| 5 | Provenance is reachable | A finding with no rule ID, or an advisory with no GHSA, within one interaction | Every claim must trace to the query or advisory that made it |
| 6 | Say what is not known | No coverage indicator on screen | Silence reads as completeness. Coverage belongs in a persistent strip |
| 7 | References are verbatim | A ref rewritten, normalized or truncated without a way to see the original | The exact ref is the evidence a reader judges with |

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
