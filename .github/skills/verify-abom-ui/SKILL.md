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

### The shape of the interface

Four surfaces, and everything else is a unit, a tab or an action on one of them:

| Surface | Answers |
| --- | --- |
| **Dashboard** (`/`) | How complete the picture is, and what discovery is configured to do |
| **Inventory** (`/inventory`) | Which things match — in tabs that are named views, reprojected by a unit switch, with actions on the scope |
| **Explorer** (`/explore`) | What those things touch |
| **Settings** (`/settings`) | What the operator claims and how ABOM is configured |

A fifth nav entry is a finding: a question grew its own page again, and a page has to invent its
own scope. `/repositories`, `/pinning`, `/remediation`, `/bom` and `/discovery` still **answer** at
their old addresses so no link breaks, and none of them is in the navigation.

Inventory's units are `dependencies`, `repositories` and `advisories`, chosen with `?unit=`.
Relationships is missing on purpose until an edge carries its own identity.

## 1. Establish ground truth from the API

The API is the oracle. Record the numbers before looking at any pixels, so the comparison later
is against fact rather than expectation.

```bash
curl -sS localhost:8099/api/summary | python3 -m json.tool
curl -sS "localhost:8099/api/actions?top=1" | python3 -c "import json,sys; print(json.load(sys.stdin)['total'], 'matching')"
curl -sS "localhost:8099/api/remediation?top=1" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['total'], 'reaching you,', d['unreached'], 'reaching nothing')"
curl -sS localhost:8099/api/views | python3 -m json.tool
curl -sS "localhost:8099/api/graph?focus=actions/checkout&hops=1&granularity=repository" \
  | python3 -c "import json,sys; g=json.load(sys.stdin); print(len(g['nodes']), len(g['edges']), g['truncated'])"
```

**A listing answers with `{items, total}`.** `total` is the size of the match, `items` is the page.
Every figure a listing renders is checked against `total`, never against the row count — that
distinction is the whole reason the envelope exists.

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

**Re-read the Explorer key with hue removed.** Desaturate the capture, or describe each row by
its glyph alone, and check every node kind is still tellable apart. Colour is the channel that
fails first and silently: `action` red against `workflow` green is the commonest pair in the
corpus, and `job` against `step` is two greys a shade apart. The second channel exists — the
legend draws a per-kind glyph and the canvas a per-kind geometry — so this is checking it still
survives a theme change, not checking whether it is there.

### 6. Cross-check rendered numbers against the API

Read the figures off the screenshot and compare to step 1. Mismatches here are real bugs:
truncated counts, rounding that changes meaning, a total that silently excludes upstream repos.

**Check the listing figures three ways**, because they have failed all three:

- the *Showing N of M* line against `total` from the endpoint;
- the pager's own "1-30 of M" against the same `total`;
- the first row under a sorted column against `?sort=<col>&desc=true` from the endpoint.

For the advisories unit, `total` and `unreached` are two populations and both belong on screen. A
strip counting 11 over a table holding 3 is what this check exists to catch.

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
| 12 | A page says what it is not showing | A grid with a pager and no statement of the match size, or a pager counting a fetch cap rather than the match | It held 500 of 1,585 with nothing saying so. A pager makes truncation look like completeness |
| 13 | A column sorts the match, not the page | The first row under a sorted column disagrees with `?sort=&desc=true` from the API | Sorting a page answers "the worst row on this page". It named a row with 39 findings while the real answer had 50 |
| 14 | A unit's columns are its own | A repository, an advisory or a relationship rendered in node columns — reach, refs, findings — reading 0 | A `repo:` node carries none of them, so the screen asserts 193 repositories depend on nothing. This is invariant 8 wearing a different hat |
| 15 | A term is applied or refused, never dropped | A scope in the address that narrows nothing, with no refusal on screen | The reader believes they scoped when they did not. The refusal names the term, and the name is the only thing that teaches them which surface answers it |
| 16 | An action carries the scope on screen | An export or a hand-off that widens to the whole organization | A bill of materials named after one part, describing all of it, is the worst document this project can emit |
| 17 | A categorical encoding has a channel besides hue | Kinds, states or boundaries told apart by colour alone, or by colour plus a shape they all share | Hue alone excludes a reader who cannot judge it, and at this density it is hard to judge against neighbouring hues anyway. Severity pairs its dot with a word; kind pairs its colour with a glyph and a geometry |

For each: **PASS**, **FAIL** with the screenshot region, or **N/A** if that surface is absent.

## Force-directed graph checks

Only a viewer can make these calls.

**Three of them are no longer yours.** `ui/tests/e2e/specs/forcegraph.spec.mjs` measures whether
the simulation advances at all, whether a reload reproduces the layout, and whether a force switch
re-orients rather than reshuffles — in a foreground browser, stated as numbers. Run it and quote
what it measured instead of judging those three by eye; a screenshot cannot tell a settled layout
from a paused one, which is how an earlier audit reported two pixel-identical captures 90 seconds
apart as determinism. What remains below is what only a viewer can answer.

- **Hairball test.** Pick any node and try to follow one edge to its other end. If edges are
  visually inseparable, the scope is too large — the fix is repository granularity or fewer
  hops, not a prettier layout.
- **Hub occlusion.** In a high-fan-in view, is the focus node still findable, or buried under
  its neighbors?
- **Semantic force legibility.** In the Risk view, name the highest-priority node from position
  alone. If you cannot, the semantic force is not doing its job.
- **One semantic force at a time.** If position appears to encode two things at once, that is a
  design regression: position must have exactly one meaning.
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
