---
name: maintain-abom-plans
description: 'Read, write and re-rank the codeql-abom planning documents under plans/**. Use when asked what to work on next; when starting or finishing a workstream that plans/ describes; when a plan is out of date, contradicts the code, or records something that turned out wrong; when adding a new plan for pending work; or when re-prioritising plans/next.md. Also use before claiming a plan is "done", because done means verified against the real corpus, not merely written.'
argument-hint: 'Optionally name a plan or area (e.g. "advisories", "plans/db", "what is next")'
---

# Maintain the ABOM plans

`plans/**` is where reasoning outlives a conversation. Each file explains why a piece of work
exists, what is already true, what is pending, and the constraint that must not be broken.
`plans/next.md` indexes them and holds the order.

The point is not to have a plan. The point is that someone six months from now can tell a
deliberate decision from an accident, and does not re-derive a constraint by breaking it.

## Layout

```
plans/next.md          index, ranking, and what we are deliberately not doing
plans/api/*.md         server behavior, query execution, HTTP surface
plans/db/*.md          schema, storage semantics, derivation
plans/ui/*.md          interface and its testing
```

One file per workstream. If two plans keep referring to each other for the same reason, they are
one workstream described twice.

## Answering "what next"

Read `plans/next.md` first. It is ranked, and each entry says why in one line. Follow the link
before acting: the ranking is a judgement and the plan holds the evidence behind it.

Check the ranking still holds before starting. Dependencies between entries are stated
("depends on 4"); a lower entry is sometimes unblocked by work that has since landed.

## Writing a plan

Structure, loosely:

- **Status** in one line, at the top. "Shipped with a known approximation" is a status; "in
  progress" is not.
- **Why this exists** — the problem, not the solution. Prefer the observed failure over the
  abstract concern.
- **What is already true** — so a reader can tell what to trust.
- **Pending**, numbered, with dependencies named.
- **The constraint that must not be broken**, stated as a rule with its reason.
- **Verification** — what would show it works, against real data.

Rules that keep these documents worth reading:

- **Numbers, not adjectives.** "415 of 988 never queued" beats "many were missing". Every figure
  should be one someone could reproduce.
- **Record what turned out wrong.** A plan that only records successes teaches nothing. Two of
  the current plans lead with a mistake, and those are the most useful paragraphs in them.
- **Name the invariant, not just the behavior.** "Advisories enrich, they never adjudicate" is
  reusable; "don't set vulnerable=true here" is not.
- **No status theatre.** No percentages, no burndown, no dates. This is not a tracker.

## Moving something to Implemented

Only when it is verified against the real corpus, not when the code compiles. `make verify`
green is necessary and not sufficient — the entry should carry the measured before/after that
shows it did what it claimed.

Move the one-line summary into the Implemented list in `next.md`, keep the plan file (its
pending section usually still has content), and update the plan's Status line.

## Adding to "Not doing"

Deliberate exclusions belong in `next.md` with their reason. Most are refusals to invent a
verdict the data does not support — evaluating an `if:`, deriving vulnerability from an advisory,
a `pin_status` column. Someone will propose each of these again; the entry is what stops it being
relitigated from scratch.

## Keeping plans honest

Before trusting a plan's claim about the code, check it. Claims that have been wrong here:

- "package X is wired in" — `go list -deps ./cmd/codeql-abom` said otherwise for four packages.
- "the queue is empty so we are done" — 415 dependencies existed that were never queued.
- "these are out of scope" — the measurement could not distinguish the thing it was asked about.

A plan asserting something about the code is a hypothesis with a shelf life. Verify, then edit
the plan rather than working around it.

## Related

- `.github/copilot-instructions.md` — architectural invariants; a plan must not contradict them.
- `.github/skills/verify-abom-ui/` — how the interface layers are verified.
