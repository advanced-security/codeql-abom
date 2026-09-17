---
name: maintain-abom-plans
description: 'Read, write and re-rank the codeql-abom planning documents under plans/**. Use when asked what to work on next; when starting or finishing a workstream that plans/ describes; when a plan is out of date, contradicts the code, or records something that turned out wrong; when adding a new plan for pending work; or when re-prioritising plans/next.md. Also use before claiming a plan is "done", because done means verified against the real corpus, not merely written.'
argument-hint: 'Optionally name a plan or area (e.g. "advisories", "plans/db", "what is next")'
---

# Maintain the ABOM plans

`plans/**` says what is **next**, and nothing else. Each file explains why a piece of work exists,
what is pending, and the constraint that must not be broken. `plans/next.md` indexes them and holds
the order.

**Git history is the record of what was implemented.** A plan is not a logbook, a changelog, or a
chronicle of how the work went. Where things go:

| Goes to | |
| --- | --- |
| `plans/**` | Pending work, open decisions, the constraints around them, and whatever else it takes to plan and then implement the thing |
| `docs/*.md` | The big picture, kept terse — what ABOM does, why it is worth doing, where it is going, and the words the interface uses |
| `.github/skills/**` | A procedure worth repeating, or a lesson with a life beyond one workstream |
| `plans/implemented.md` | The reasoning: what the corpus refuted, what did not move. Git-ignored |

**`.github/instructions/**` is fixed and is not one of the destinations.** Those files are injected
on every agent call in their `applyTo` scope, so anything added there is paid for forever. If a rule
there is wrong, say so and stop — see `.github/instructions/agents.instructions.md`.

The point is that someone six months from now can tell a deliberate decision from an accident, and
does not re-derive a constraint by breaking it — without reading a year of arrival states to do it.

## Layout

```
plans/next.md          index, ranking, and what we are deliberately not doing
plans/implemented.md   reasoning worth keeping — git-ignored, see below
plans/api/*.md         server behavior, query execution, HTTP surface
plans/db/*.md          schema, storage semantics, derivation
plans/tools/*.md       how the work itself gets done
plans/ui/*.md          interface and its testing
```

One file per workstream. If two plans keep referring to each other for the same reason, they are
one workstream described twice.

## Answering "what next"

Read `plans/next.md` first. It is ranked, and each entry says why in one line. Follow the link
before acting: the ranking is a judgement and the plan holds the evidence behind it.

Check the ranking still holds before starting. Dependencies between entries are stated
("depends on 4"); a lower entry is sometimes unblocked by work that has since landed.

**One entry is one piece of work, never a phase of one.** A plan file may legitimately answer more
than one entry when they are genuinely different work — *Component catalog spike* and *Explorer
component coverage* both point at `ui/testing.md` and share nothing else. What is not allowed is
splitting a single plan into "the big part" and "the cheap parts": that is sequencing, it belongs
in the plan's own Sequencing section, and putting it in the ranking spends two slots on one plan
and pushes everything beneath it down twice. The test is whether the two entries could be done by
different people in either order. If one is just the leftovers of the other, it is one entry.

**Adding an entry is a decision about everything below it.** A plan that was nearly next can end
up mid-table without anyone ever deciding to demote it, purely through accumulated insertions
above. When ranking something above long-standing work, say so and say why, rather than letting
the number drift.

## Writing a plan

Structure, loosely. **There is no length limit.** A plan carries whatever it takes to plan the work
and then implement it from the file alone — the measurement behind a claim, the alternative that was
rejected, the query that produced a number. What a plan may not carry is narration of work already
finished, and that is the thing to cut, not length.

- **Status** in one line, at the top, present tense, no date. "Built with a known approximation" is
  a status; "in progress" is not.
- **Why this exists** — the problem, not the solution. Prefer the observed failure over the
  abstract concern.
- **Pending**, numbered, with dependencies named.
- **The constraint that must not be broken**, stated as a rule with its reason.
- **Verification** — what would show it works, against real data.

Rules that keep these documents worth reading:

- **Numbers that size the work, not numbers that narrate it.** "523 pins the queue cannot reach"
  earns its place; "reduced 140,421 rows to 43,379" is a result, and results live in git.
- **No arrival states.** No "built 2026-09-05", no struck-through completed items, no phase lists
  with most of the phases done, no before/after table for work that is finished. If it is done, it
  leaves the file.
- **Name the invariant, not just the behavior.** "Advisories enrich, they never adjudicate" is
  reusable; "don't set vulnerable=true here" is not. State it in the plan, and if it outlives the
  plan, carry it into a skill when the plan retires.
- **An open question is a fork with named alternatives.** If the answer already follows from an
  architectural invariant, it is a decision to record, not a question to ask.
- **Answer a question in place.** Write the decision as a decision. Do not keep the question and the
  answer side by side once it is settled — state what was decided and why, in one place.
- **No status theatre.** No percentages, no burndown, no dates. This is not a tracker.

## Retiring a plan

When the last pending item lands, **the plan file goes away.** Before deleting it, move anything
durable out:

1. A rule or procedure that outlives this workstream → a skill under `.github/skills/**`, but only
   if it changes what somebody does next time. Most constraints die with their plan, and should.
2. A fact about what the system now does → `docs/architecture.md`, or `docs/vocabulary.md` if it is
   a word the interface uses. Terse: these are read by people deciding whether to trust ABOM.
3. A theme somebody should know is coming → `docs/roadmap.md`.
4. The reasoning — what was refuted, what did not move and why that was right →
   `plans/implemented.md`.

Then delete the file, fix every reference to it, and remove its entry from `next.md`. A plan kept
alive only to hold history is the thing this layout exists to prevent.

**Never stage or commit.** Leave the deletion unstaged; the operator commits.

## `plans/implemented.md`

**Git-ignored on purpose.** Git history already records what changed and when, so a tracked markdown
summary is a second copy that drifts from the log it duplicates. What history does not carry is the
reasoning — the measurement that refuted a design, the number that did not move and why that was
right — and that is what the file is for. Write it for an agent picking the work up cold.

Two consequences worth holding on to:

- **It may not exist.** A fresh clone has no copy. Absence means nothing has been *recorded here*,
  never that nothing has been built — the same absent-is-not-zero rule the store follows. Check each
  plan's Status line, and the code, before concluding a workstream has not landed.
- **Never add it to a commit**, and never restore its content into a tracked plan. `next.md` keeps a
  pointer and nothing more.

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
- "`discover --org` enumerates and records which repos have a database" — accurate, and it hid
  that the seconds-long enumeration is only reachable behind a per-repository probe. A package
  doc comment states intent; read the calls it makes.

A plan asserting something about the code is a hypothesis with a shelf life. Verify, then edit
the plan rather than working around it.

## Related

- `docs/architecture.md` — what the system does now; a plan must not restate it.
- `.github/instructions/architecture.instructions.md` — architectural invariants a plan must not
  contradict. Read-only: see `.github/instructions/agents.instructions.md`.
- `.github/skills/verify-abom-ui/` — how the interface layers are verified.
