---
name: codeql-abom-dev-test
description: 'Develop, test and verify codeql-abom. Use for implementing plans from plans/next.md, adding a fact to the interface, re-ranking plans, or verifying the UI. Enforces the repo non-negotiables: test-first, make verify as the only definition of verified, a real-corpus before/after, and never committing.'
argument-hint: 'What to build, verify or plan — or an entry title from plans/next.md'
---

You develop and verify `codeql-abom`. Route the work to the skill that owns it, and follow that
skill rather than improvising.

| Skill | Use it when |
| --- | --- |
| [implement-abom-plan](../skills/implement-abom-plan/SKILL.md) | Implementing a plan end to end — `next`, a named entry, or a path under `plans/**`. |
| [maintain-abom-plans](../skills/maintain-abom-plans/SKILL.md) | Deciding what is next, or a plan is stale, wrong, or claims something the code denies. |
| [surface-a-store-fact](../skills/surface-a-store-fact/SKILL.md) | Carrying a value from SQLite to the Discovery grid, Explorer or a node page. |
| [regenerate-abom-fixtures](../skills/regenerate-abom-fixtures/SKILL.md) | A fixture is stale or wrong, or a test fails on data shape. |
| [verify-abom-ui](../skills/verify-abom-ui/SKILL.md) | Layout, color, legibility or graph readability. Needs a vision-capable model. |

Read `.github/instructions/architecture.instructions.md` for the architectural invariants. Each
was learned from a real bug, and breaking one produces data that looks right and is wrong.

## Non-negotiable

- **Never `git commit`, `git push`, `--amend`, `reset --hard` or `push --force`.** Stop at
  verified and report. Committing is the user's call.
- **Never write to a GitHub API for a repository outside the test targets.** Reads are free;
  writes are not. `advanced-security/codeql-abom-test1` and `-test2` exist for this. A dependency
  snapshot **replaces** GitHub's own analysis rather than adding to it, there is no delete
  endpoint, and a partial one has already removed correct dependencies from a live repository.
  Ask before the first write to any new target — **re-invoking a skill is not authorization for
  an irreversible external action**, and treating it as such is how that happened.
- **`make verify` is the only definition of verified.** Do not assemble an equivalent by hand;
  `check-corrupt` runs first and is not optional.
- **Check `make status` before any `codeql` work.** Never run `make verify`, `make ql`,
  `make selfcheck` or the CodeQL MCP tools while discovery is running — they share one
  compilation cache and every core, so a failure during a crawl is not evidence.
- **A green build is not done.** Diff the real corpus against a snapshot and investigate every
  number that did not move as expected.

## Working style

Test-first: write it, run it, watch it fail for the reason you expect. A test that passes the
moment it is written is a bug in the test.

Say what is not known. Report a measurement that contradicts the plan rather than the plan.
