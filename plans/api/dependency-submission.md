# Submit the enriched Actions graph to GitHub

**Status:** ABOM submits the transitive Actions dependencies GitHub cannot derive, under a manifest
path of its own, and GitHub reports them. Nothing submits on a timer.

## Why this exists

GitHub's dependency graph does not resolve transitive dependencies for GitHub Actions, and the
reason is structural rather than an oversight: **there is no lock file.** Static analysis reads
`jobs[*].steps[*].uses` out of `.github/workflows/`, yields the direct set, and stops. ABOM
resolves the rest by recursion, so it holds something GitHub wants and cannot compute.

The payoff is not the submission but what GitHub does afterwards — Dependabot alerts, transitively
aware dependency review, an SBOM export carrying our edges. One link in that chain has never been
measured.

## Pending

1. **Measure whether a transitive Actions alert produces a pull request.** GitHub says it opens
   pull requests against the parent dependency; the ecosystems page says Dependabot cannot find a
   vulnerable transitive dependency in the repository and **will not create a security update**,
   and that *version* updates open pull requests when the parent is explicitly declared in the
   manifest. For Actions the parent is explicitly declared — it is the `uses:` line — so the
   promise probably holds, which is an inference rather than a measurement. The difference is a
   product: version updates are **opt-in per repository** and need a `dependabot.yml` naming
   `package-ecosystem: "github-actions"`, while security updates are automatic. Settle it in
   `advanced-security/codeql-abom-test1`, which already carries a submitted transitive dependency:
   introduce one whose version has a known advisory, then record whether an alert appears, whether
   a pull request appears, and whether `dependabot.yml` changes the answer. **Do not soften the
   claim in place — measure it.**
2. **Deferred by decision: nothing submits on a timer.** `POST /api/submissions/renew` re-sends
   approved targets whose content digest moved and never makes a first submission, so adding a name
   to `--submit-targets` cannot write to it as a side effect of editing a flag. Writing to other
   people's repositories unasked is a different thing from doing it when asked; the endpoint exists
   so that decision can be taken later without rebuilding anything.

## The constraint that must not be broken

**Never claim a path GitHub analyzes.** Precedence keys on `file.source_location`, not on the
manifest name, so a snapshot naming a real workflow owns that file's whole dependency list and
replaces GitHub's reading of it. Claiming a path GitHub does not analyze is purely additive, which
is what makes partial data safe: a shallow crawl reports fewer transitive dependencies rather than
removing correct ones.

**A submission cannot be withdrawn.** There is no delete endpoint, snapshots never expire, and a
new commit neither restores static analysis nor invalidates stale data. The only repair is a
snapshot with the same correlator and detector *name* and an **empty `manifests` object**; an empty
`resolved` removes the packages and leaves the path owned and permanently blank. Every write goes
through the ledger, refusals included, because GitHub offers no way to ask what was submitted and a
repair made by hand is invisible.

## Verification

- In test1, a submitted transitive dependency with a known advisory raises an alert, and whether a
  pull request appears is recorded with and without `dependabot.yml`.
- Dependency review on a test pull request reports a transitive Actions dependency.
- A renewal of unchanged content is a no-op to GitHub's graph.
