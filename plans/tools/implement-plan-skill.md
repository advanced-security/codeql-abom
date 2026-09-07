# A skill that implements the next plan

**Status:** shipped 2026-09-05 as `.github/skills/implement-abom-plan/SKILL.md`. Unproven until
it has driven a plan end to end without hand-holding.

## Why this exists

Implementing a plan is a fixed sequence that is currently re-derived from scratch each time, and
the two steps that get skipped are always the same two:

- **Checking the plan's claims are still true before acting on them.** Three claims in `plans/**`
  have already been falsified by measurement: "package X is wired in" (`go list -deps` disagreed
  for four packages), "the queue is empty so we are done" (415 dependencies had never been
  queued), and `api/advisories.md` item 3, which asked for a `store.AdvisoryPackages()` that had
  existed the whole time.
- **The real-corpus before/after.** `make verify` green is necessary and not sufficient. Unit
  tests passed through every architectural bug in `architecture.instructions.md`.

The operator should be able to type one thing and get the whole loop, including the parts that
are easy to skip precisely because nothing fails when they are.

## What is already true

`.github/skills/maintain-abom-plans/SKILL.md` covers reading, writing, re-ranking and the bar for
moving an entry to Implemented. What it does not cover is *executing* a plan — it is a
documentation skill, and deliberately stops before the code.

`make status`, `make verify` and `make start/stop` are the canonical lifecycle, so the skill has
commands to call rather than shell history to reconstruct.

## Pending

1. ~~`.github/skills/implement-abom-plan/SKILL.md`~~ — written, eight steps. The argument is a
   listed entry title (`Per-tier suite controls`), a plan file or path, or omitted for the
   top-ranked entry; `next` is only the explicit spelling of that default. A named entry outranks
   the ranking, an unmet dependency is named rather than silently implemented, and an unmatched
   argument stops rather than falling back to `next`.
2. **Prove it on a real entry.** The skill is untested until it has picked, implemented and
   measured something without being told the answer. The obvious candidate is *Exact
   supply-chain classification*, whose plan is large enough that a skipped step would show.
   Prove the named form too, not only `next` \u2014 they take different paths through step 1.
3. **Decide whether step 6 can be made harder to skip.** It is currently a written instruction,
   which is the same protection the four "tested but wired to nothing" packages had.

## The constraint that must not be broken

**A green build is not "done".** The skill must not be able to move an entry to Implemented on
`make verify` alone — the Implemented line has to carry a measured before/after, or the entry
stays where it is. Everything else in the skill is convenience; this is the part that stops the
planning documents becoming status theatre.

## Verification

- Run it against a genuinely pending entry and confirm, in order: it reconciled `next.md` before
  choosing, it refused or waited when discovery was running, a test failed before any
  implementation existed, and the Implemented entry it wrote carries real numbers.
- Run it against an entry whose plan is already stale and confirm it corrects the plan rather
  than implementing what the plan says.
