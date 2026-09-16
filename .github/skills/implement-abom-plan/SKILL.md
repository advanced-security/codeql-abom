---
name: implement-abom-plan
description: 'Implement a plan from codeql-abom plans/next.md end to end. Use when asked to implement, build, do or start a plan — either the next one in the ranking, or a specific entry named by its title in plans/next.md (e.g. "Per-tier suite controls"), by plan file, or by path under plans/**. Reconciles plans/next.md against the actual code before choosing, then works test-first, verifies with make verify plus a real-corpus before/after, and only then moves the entry to Implemented. Does not commit or push.'
argument-hint: 'A listed entry title such as "Per-tier suite controls", or a plan file or path such as "supply-chain" or "plans/api/reachability.md". Omit, or say "next", for the top-ranked unimplemented entry.'
---

# Implement an ABOM plan

One command from "what should I do?" to "here is what changed, measured".

The steps below are ordered because each depends on the last. The two that get skipped when this
is done by hand are **1** and **6**, and they are the two that catch real defects.

## Which plan

The argument names the plan. It is optional, and `next` is only the explicit spelling of the
default — it is not the usual case, just the one that needs no thought.

| Argument | Means |
| --- | --- |
| *(omitted)* or `next` | The top unimplemented entry in the **Ranked next** list of `plans/next.md`. |
| An entry title, e.g. `Per-tier suite controls` | That entry, wherever it sits in the ranking. |
| A plan file or path, e.g. `supply-chain`, `plans/api/reachability.md` | That plan file. |

Resolve a named argument in this order, stopping at the first that matches:

1. An exact path under `plans/`.
2. An entry title in **Ranked next**, matched case-insensitively, ignoring surrounding
   punctuation and the leading `**`. Substring matches count, so `tier suites` finds
   *Per-tier suite controls*.
3. A plan filename stem, e.g. `supply-chain` → `plans/db/supply-chain.md`.
4. An entry title in **`plans/implemented.md`** — see below. That file is git-ignored, so it may
   be absent; absence is not evidence the work has not built.

And then:

- **Several entries match.** List them and ask which. Never guess; *Component catalog spike* and
  *Explorer component coverage* both link to `ui/testing.md` and are different pieces of work, so
  a plan file can be the answer to more than one entry.
- **Nothing matches.** Say so, print the **Ranked next** titles, and stop. Do not silently fall
  back to `next` — the user named something for a reason, and quietly doing different work is
  worse than doing none.
- **It is already in `plans/implemented.md`.** Say so and stop, unless its plan's Pending section still has
  content and the user clearly meant that. Confirm before starting.
- **It is in "Not doing, and why".** Refuse, quote the reason, and stop. Those entries exist to
  stop a decision being relitigated from scratch; overturning one is a conversation, not a task.

**A named entry outranks the ranking.** Reconciling in step 1 may reveal that the ranking has
moved on, and that is worth saying, but it does not change what was asked for.

**Say when a named entry has an unmet dependency.** The ranking states them ("depends on 3"). If
*Per-tier suite controls* is asked for while *Tier → suite binding as config* is unimplemented,
name the dependency and ask whether to do it first — do not quietly implement both, and do not
start the named one against a binding that does not exist yet.

## 1. Reconcile the plan against the code, before starting

Use the **`maintain-abom-plans`** skill. Do not read `next.md` and start working; its entries are
hypotheses with a shelf life.

For the chosen entry, check its factual claims:

```bash
go list -deps ./cmd/codeql-abom | grep -c internal/<pkg>   # "wired in" has been wrong 4 times
grep -rn "<Symbol>" --include=*.go | grep -v _test.go      # "has no production caller"
sqlite3 "$ABOM_DB" "<the query behind the entry's number>"  # "415 of 988", "936 packages"
```

Claims that have already been false in this repository:

- "package X is wired in" — `go list -deps` disagreed for four packages.
- "the queue is empty so we are done" — 415 dependencies existed that had never been queued.
- "these are out of scope" — the measurement could not distinguish the thing it was asked about.
- `api/advisories.md` item 3 asked for a `store.AdvisoryPackages()` that had existed all along.
- **"nothing detects that an upstream reference moved"** — `expand.Rechecker` had been doing it
  for weeks, wired in, with 1,359 of 1,451 analyses probed. A duplicate was written and deleted.

**Grep for the behavior, not for the name you would give it.** That last one was missed by
searching `webhook`, `event` and `RequeueStale` — all reasonable, none of them the word the
existing code used, which was `recheck`. Before concluding something does not exist, search two or
three *synonyms* for what it would do:

```bash
grep -rniE "drift|recheck|stale|refresh|revisit" --include=*.go internal/ | grep -v _test.go
```

It has now happened twice, and both times the cost was a full implementation thrown away.

Correct the plan where it is wrong, re-rank if a dependency has since landed, and say what
changed. Re-ranking decides what `next` means; it never overrides an entry the user named.

**If reconciling shows the entry is already done, say so and stop.** That is a successful run.

## 2. Check the machine is quiet

```bash
make status
```

Discovery must be stopped. Every `codeql` invocation — `make verify`, `make ql`, `make selfcheck`,
and the MCP server's tools — shares one local compilation cache, so a query test and a
dependency's database build in flight together contend for that cache and for every core. A
timing-sensitive failure during a crawl is not evidence of anything.

If discovery is running, pause it from the Discovery page or `make stop`, and say that you did.

## 3. Snapshot the corpus

```bash
sqlite3 "$ABOM_DB" ".backup /tmp/abom-before.db"
```

Before any code changes. Step 6 has nothing to diff against otherwise, and the snapshot costs
nothing.

**Do not use `cp`.** The database runs in WAL mode, so `cp` copies the main file and leaves the
write-ahead log behind — the snapshot is silently missing everything committed since the last
checkpoint. Measured 2026-09-08: `cp` produced 16,809 edges against the live 16,826, and a 4.4 MB
WAL was sitting there unread. The baseline looks fine, diffs cleanly, and is wrong, so the
symptom is a step-6 number that moved when nothing should have moved. `.backup` and
`VACUUM INTO` both read through the WAL; `cp` does not.

Never point work at the live database when a copy will do. Run a second server on its own port
and its own copy rather than disturbing the one that is up.

## 4. Work test-first, and watch the failure

Write the test. Run it. **Confirm it fails for the reason you expect**, then implement.

A test that passes the moment it is written has proved nothing. One in this repository did
exactly that — it asserted a string the component never renders, so it was green against
deliberately broken code. If a new test passes immediately, that is a bug in the test.

- Name tests after the behavior they protect, not the function they call.
- Comments state only what the code cannot show, in one line, and explain *why* a constraint
  exists rather than what the next statement does.
- American English.
- Read a file before editing it, and include the trailing context in a replacement or the
  anchor line gets clobbered.

Check the invariants in `.github/instructions/architecture.instructions.md` as you go. Each was
learned from a real bug, and breaking one produces data that looks right and is wrong.

## 5. Verify

```bash
make verify
```

That is the only definition of verified, and CI runs the same target. Do not assemble an
equivalent by hand — `check-corrupt` runs first and is not optional, because this environment
intermittently prepends a duplicate `package` clause when writing a Go file.

Never pipe build, test or lint output through `tail`, `head` or `grep`; read it whole.

For interface work, `ui-test` has no CSS engine, so a wrapped label or an overflowing grid passes
it. Layout is the screenshot pass in the **`verify-abom-ui`** skill.

## 6. Diff the real corpus, and let it contradict you

`make verify` green is necessary and not sufficient. Unit tests passed through every
architectural bug listed in `.github/instructions/architecture.instructions.md`.

Run the change against the copy, then compare the headline numbers with `/tmp/abom-before.db`:
node and edge counts, frontier status counts, findings per rule, blast radius, and whatever
number the plan itself quoted.

**Investigate every metric that did not move as expected before concluding anything.** Both
outcomes are informative:

- A number that moved when it should not have is a defect.
- A number that did not move is often correct, and the reason belongs in the plan so nobody
  "fixes" it later. Advisory exposure stayed at 253 rows because all 14 new advisories named
  actions with `dependent_repos = 0` — referenced only by `upstream` repositories, never a
  `seed`. That is the invariant working.

Zero results usually means a coverage gap, not a broken traversal. Check whether the data
supports the result at all before touching traversal code. And never "fix" data that contradicts
an assumption: `actions/download-artifact@v8` looked like corruption until `git ls-remote --tags`
confirmed v1–v8 all exist. Verify the world first.

## 7. Record what happened

Back to **`maintain-abom-plans`**:

- Update the plan's **Status** line, in one sentence, present tense and undated. "Built with a known
  approximation" is a status; "in progress" is not.
- **Remove what landed from the plan.** Delete the pending item outright rather than striking it
  through — plans describe what is next, and git history is the record of what shipped. If that was
  the plan's last pending item, **retire the whole file**: move any statement of what the system now
  does to `docs/architecture.md`, and a rule that outlives the workstream to a skill under
  `.github/skills/**`, then delete the plan, fix every reference to it, and drop its entry from
  `next.md`. **Never add to `.github/instructions/**`**, which is fixed — see
  `.github/instructions/agents.instructions.md`.
- **Record what turned out wrong**, in `plans/implemented.md`. A record that only lists successes
  teaches nothing. The corpus check exists to falsify the design, and when it does, that paragraph is
  the most valuable one written. The advisory plan's trigger was inert on a drained queue and its
  refresh blocked the cycle for 109 seconds; neither was predicted.
- Move the entry into **`plans/implemented.md`** with its measured before/after, newest first, and
  re-rank what is left in `next.md`. That file is **git-ignored on purpose** — git history is the
  record of what was built, and this is the reasoning behind it. Do not stage it, and do not paste
  it back into `next.md`, which keeps a pointer only.

## 8. Stop at verified

Never `git commit`, `git push`, `git commit --amend`, `git reset --hard` or `git push --force`.
Resolve, verify, then report what changed and what it measured. Committing is the user's call.

**The same rule covers writing to GitHub's APIs, and more strictly.** A commit is local and
reversible; a dependency snapshot is neither. There is no delete endpoint, a snapshot naming a real
manifest **replaces** GitHub's own static analysis rather than adding to it, and on 2026-09-08 a
partial one removed two correct dependencies from a live repository. The "repair" that followed
added a fabricated dependency which then stayed live for a day, because snapshots do not expire and
GitHub reports them as fact. So:

- Write only to `advanced-security/codeql-abom-test1` or `-test2`, which exist for this.
- **Ask before the first write to any new target.** Being told to run a plan is not authorization
  for an irreversible action against a third party's system — that inference is exactly what went
  wrong, and "the user re-invoked the skill" is not consent.
- Reads are free. Probe with `GET` as far as it will take you before reaching for `POST`.
- **State the expected result before writing**, so the read-back can falsify it. Both repairs were
  checked this way and both predictions held; a write whose outcome was not predicted is a write
  whose outcome cannot be wrong.
- **Record every write**, including ones made by hand. GitHub offers no way to ask what has been
  submitted to a repository, so a submission nobody wrote down is one nobody can find. A `gh`
  one-liner bypasses ABOM's ledger and recreates exactly the problem the ledger exists to solve.
- To undo one: same `job.correlator` and `detector.name`, empty `manifests` object. Supersession
  keys on the detector **name**, not its version — verified across three snapshots declaring
  `0.0.1`, `0.0.2` and `0.0.3`. An empty `resolved` inside a manifest is *not* the same thing: it
  removes the packages and leaves the path owned and permanently blank.

## The rule that makes this worth running

**A green build is not "done".** An entry does not move to `plans/implemented.md` without a
measured real-corpus before/after in its line. If the numbers cannot be produced, the entry stays
where it is and the run reports why — that is a useful outcome, not a failure.

Everything else here is convenience. This is the part that stops the planning documents becoming
status theatre.

## Related

- `.github/skills/maintain-abom-plans/SKILL.md` — reading, writing and re-ranking `plans/**`.
- `.github/skills/verify-abom-ui/SKILL.md` — the screenshot pass `ui-test` cannot do.
- `.github/instructions/architecture.instructions.md` — the architectural invariants.
- `.github/instructions/working-style.instructions.md` — test-first, the real-corpus diff, and
  the canonical `make start / status / logs / stop` lifecycle.
