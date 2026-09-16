---
description: "Use before creating or editing any file under .github/instructions/. These files are fixed: it states why, and where a rule, a lesson, a plan or a statement about the system belongs instead."
applyTo: ".github/instructions/*.md"
---

# Instruction files are fixed

**Do not add to, reword, reorganize or extend anything under `.github/instructions/`.**

Every file here is injected into an agent's context on **every call** that touches a file its
`applyTo` matches. A line added once is paid for on every future edit, forever, by every agent —
so this is the one place where growth is invisible to the person who caused it and carried by
everyone after. Terse and unchanging is the feature.

If a rule here is wrong, out of date or contradicted by the code, **say so in one line and stop.**
Correcting it is the operator's call, not an agent's.

## Where the thing you were about to write belongs

| What it is | Where it goes |
| --- | --- |
| Work that is next, and the reasoning behind it | `plans/**/*.md` — one file per workstream, any length |
| What ABOM does now, or what it refuses to do | `docs/architecture.md` |
| Why it is worth doing, and where it is going | `docs/vision.md`, `docs/abom-value-proposition.md`, `docs/roadmap.md` |
| A word the interface puts in front of a reader | `docs/vocabulary.md` |
| A procedure worth repeating, or a lesson with a long life | `.github/skills/**/SKILL.md` |
| What a run measured, and what it refuted | `plans/implemented.md`, which is git-ignored |

A lesson earns a place in a skill only if it changes what somebody does next time. Progress,
measurements, counts and status are not lessons; they belong in the plan they came from.

Keep `docs/**` terse for the same reason a summary is worth more than a transcript: it is read by
people deciding whether to trust the thing, not by anyone looking for detail.
