# A finding has to reach somewhere a person can fix it

**Status:** deferred by decision until a review-and-confirm gate exists. Nothing in ABOM writes a
code scanning alert, and nothing calls `/code-scanning/sarifs`.

## Why this exists

The point of the tool is remediation, not detection. The dependency path is built:
`api/dependency-submission.md` puts ABOM's transitive edges into GitHub's dependency graph, and
Dependabot raises an alert against the consuming repository. That path fixes **versions**.

Findings have no such path. A snapshot has no field for a finding — `metadata` is eight scalar
keys, matched against nothing — so findings reach remediation through **code scanning alerts** or
not at all. Campaigns, assignment and Copilot are all downstream of an alert, so ABOM stops at the
first arrow of `finding → SARIF → alert → campaign → assignment → pull request`.

Two things stand between here and there, and they are the work rather than the POST.
**Re-anchoring:** a code scanning location is a path relative to the repository root, matched
against a file committed to that repository, and **6,327 findings across 309 repositories sit in
upstream repositories** — `actions/checkout`'s own `action.yml` is not committed to the repository
that calls it. `store.ActionsReachedThroughComposites` reports **5,724 call sites** and has never
had a caller. **Commits:** the endpoint requires `commit_sha`, and of the **5,043 findings in seed
repositories** that need no re-anchoring, **512** name the commit they describe.

## Pending

1. **The review-and-confirm gate.** A selection over specific findings with nothing pre-selected;
   the repositories it would write to; per repository the count of alerts that would be **created**
   and the count that would be **withdrawn**; and an acknowledgement naming the consequence. Blocks
   everything below it, and is most of the work.
2. **A finding names the commit it was found at.** Depends on a re-crawl, which
   `db/finding-lifecycle.md` is also waiting for.
3. **A code scanning client** — gzip then base64, an allow-list refusing any repository not
   explicitly approved, a ledger row per upload, and its own SARIF rather than CodeQL's forwarded.
   Blocked on 1 by rule, not by technique.
4. **Re-anchoring a transitive finding to the `uses:` line.** Also what makes step 2 of the gate
   answerable, because the count of alerts is a count of `uses:` lines, not of findings.
5. **Fingerprints that are ours and stable**, surviving the re-anchoring being recomputed.
   Depends on 4.
6. **Whether ABOM drives the campaign API or leaves campaigns to the operator.** Depends on 1–5,
   and on whether the chain terminates in a generated fix: Autofix documents a language list that
   **does not include Actions**, and agent assignment is documented for campaign alerts where an
   autofix was generated. Settle that by observation on `codeql-abom-test1`, downstream of item 1.
   Assignment to a person is the fallback and is always available.

## The constraint that must not be broken

**A person chooses every alert, or there is no alert.** An alert appears in somebody else's
repository, in the Security tab their developers read and in the numbers their leadership is
measured on, indistinguishable from one their own analysis found. No crawl, schedule, sweep,
"submit all" or test run may produce one — including on `codeql-abom-test1` and `-test2`, where
the habit is what would ship. **Building the client before the gate is the one sequence this plan
forbids**, because a client that works is a client somebody will call. An allow-list is the floor
beneath the gate, never a substitute for it.

**ABOM uploads under its own tool name and its own stable category, never the source SARIF's.**
GitHub's replace key is the pair *(tool name, category)*, and both halves are constants — not read
from the input, not derived from the provider, not formatted from a repository, selection or date.
A category that moved with the provider set would orphan the old analysis and raise its alerts
again as new ones, losing resolution state, dismissals and campaign membership. The driver name is
ABOM's too, because a re-anchored result is not one CodeQL ever emitted. `runAutomationDetails.id`
splits at the **last** `/`, so an id with no slash lands in the empty category, the bucket a plain
single-analysis upload uses. Getting this wrong does not error; it deletes somebody's alerts.

**An upload replaces; it does not add.** A second upload under the same category for the same
commit supersedes the first, so uploading selection B silently withdraws selection A. That is a
requirement on the gate's numbers rather than a caveat.

**A path that is not in the repository is not a location** — re-anchor, or do not upload — and
**the alert says what reaches what, never what is vulnerable**. Adjudication stays in
`api/verdict-gate.md`, against a policy someone declared.

## Verification

- **The gate is verified before the client is.** A test that fails if any code path reaches the
  upload without a recorded human confirmation, and one that fails if a count shown to the operator
  disagrees with the alerts the upload would create or withdraw.
- **The category and the driver name are constants, proved by mutation**, each mutated separately,
  because a wrong tool name and a wrong category mask each other.
- **Nothing automated ever uploads.** A test that finds a way to make one is a defect report.
- **The first real upload is made by hand, on `codeql-abom-test1` only**, with the prediction
  written before the write, and the repository's own analyses unchanged outside ABOM's category.
  Reversal is checked at the same time: re-uploading under the same tool and category with no
  results is how a run stops reporting what a previous run reported.
