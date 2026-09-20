# Submit the enriched Actions graph to GitHub

**Status:** guarded manual submission is built; publication expansion is deferred pending native evidence.

## Why this exists

ABOM already constructs a transitive projection through
`store.SubmissionClosure` and `submit.Build`, normalizes package identity with
`normalize.PackageURL`, and sends it through the guarded `submit.Client`.

GitHub's native Actions lockfile now supplies transitive pin evidence, and Dependabot's source
already includes lock updates. Automatic lock-backed DG/SBOM reporting and advisory outcomes are
separate capabilities to measure in [dependency-graph-crosscheck.md](dependency-graph-crosscheck.md).
An open feature request is neither a delivery commitment nor proof that no server-side support
exists. Expanding ABOM's publisher before that comparison risks duplicating native behavior.

**The planned `abom-check` release is verdict-only.** Workflow preview/submit modes and their new
write grants are deferred by decision. Existing operator-controlled submission, its ledger and
its refusal guards remain; deleting them would neither withdraw historical snapshots nor prove
that a native replacement covers their use cases.

## Pending

1. **Establish a residual need before expanding.** Consume the native capability comparison from
   [dependency-graph-crosscheck.md](dependency-graph-crosscheck.md): pin enforcement, update support,
   DG/SBOM inventory, review, alerts and security-update PRs separately. If the native path covers
   the required case, do not build another publisher. If a gap remains, name its target population,
   evidence and benefit before asking to resume workflow submission or other expansion.
2. **Measure any retained fallback on approved targets.** After 1 and explicit operator consent,
   compare native-only results with ABOM's synthetic-path supplement. Distinguish an alert from an
   update PR and version updates from security updates; test configuration differences rather than
   promising a PR because a package was accepted. Existing test1/test2 submissions must be accounted
   for, not mistaken for native data. Do not execute vulnerable code to observe an advisory.
3. **Keep the current path honest at the new resolution boundary.** Once
   [actions-lockfile.md](../db/actions-lockfile.md) supplies consumer-context selection, existing
   submission readers must use it or explicitly refuse claims they cannot support. The current
   handler resolves head when called and labels snapshots `refs/heads/main`; it cannot assert an
   arbitrary workflow commit. Before exposing broader use, bind preview and write to exact
   repository/commit/ref/context, scope coverage to that target, and refuse stale or incomplete
   evidence. This is not a new `abom-check` operation or a reason to bypass today's target guard.
4. **Prepare retirement only if native coverage warrants it.** Inventory ledger targets, stable
   detector/correlator pairs and claimed source paths. Under explicit consent, use a superseding
   snapshot to release ABOM's contribution where appropriate, then verify native package coverage
   and record the result. An empty `manifests` object and an empty `resolved` have different effects.
   Keep a repair procedure and historical ledger; retiring local code is the final step, not the
   mechanism that removes remote state. Depends on 1 and verified target-specific coverage.

## The constraint that must not be broken

**Never claim a path GitHub analyzes.** Precedence keys on `file.source_location`, not on the
manifest name, so a snapshot naming a real workflow owns that file's whole dependency list and
replaces GitHub's reading of it. ABOM keeps its synthetic source path and stable detector/correlator
pair. That protects the native workflow reading; it does not prove coexistence with another producer
claiming the same synthetic path, or make an incomplete renewal safe for ABOM's previous snapshot.

**A submission cannot be withdrawn.** There is no delete endpoint, snapshots never expire, and a
new commit neither restores static analysis nor invalidates stale data. The only repair is a
snapshot with the same correlator and detector *name* and an **empty `manifests` object**; an empty
`resolved` removes the packages and leaves the path owned and permanently blank. Every write goes
through the ledger, refusals included, because GitHub offers no way to ask what was submitted and a
repair made by hand is invisible.

**No new publisher is implied by native-lock adoption.** No timer, automatic first submission,
workflow grant or client submission mode is enabled here. Existing renewal stays explicitly
requested and bounded to approved, previously submitted targets. Read and verdict operations never
write; an incomplete read must not become an empty snapshot releasing prior data.

**Relationship and coverage are facts, not alerting switches.** Do not label transitive dependencies
direct merely to influence an external product. Report incomplete coverage and keep the declared
relationship even when a particular consumer cannot act on it.

## Verification

- The native comparison attributes each package to its actual producer before any fallback is
   called necessary. DG/SBOM, review, alerts and update PRs each have their own observed result.
- Existing approved-target, synthetic-path, unchanged-renewal and ledger guards remain tested.
   Wrong context, unknown analysis and a failed verification cannot cause a write or release.
- Any approved fallback or retirement experiment captures before/after packages and source paths,
   accounts for other producers, and verifies repair. Different correlators or a `201` response
   alone do not prove coexistence or removal.
- Known success, refusal and indeterminate network outcomes remain distinguishable. Do not retry
   an uncertain write as if it were known to have done nothing.

## Prior art

Jesse Houwing's
[actions-dependency-submission](https://github.com/jessehouwing/actions-dependency-submission),
introduced through [issue #18](https://github.com/advanced-security/codeql-abom/issues/18), shows a
per-repository workflow integration over
[DependencySubmitter](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/src/dependency-submitter.ts)
with [payload tests](https://github.com/jessehouwing/actions-dependency-submission/blob/aa358ec7aee0a7f2dbaef9702915b28e90879842/__tests__/dependency-submitter.test.ts).
Learn from its integration and explicit relationship handling without assuming its manifest paths,
replacement keys or SHA/version duplication are appropriate for ABOM. Compatibility is measured
on approved test targets, not inferred from either implementation's unit tests.

GitHub's [native locking effort](https://github.com/github/gh-actions-lock/blob/v0.1.6/README.md)
and [Dependabot update integration](https://github.com/dependabot/dependabot-core/blob/main/github_actions/lib/dependabot/github_actions/lockfile/cli_engine.rb)
now set the preferred integration direction. Jesse Houwing's
[upstream request #122](https://github.com/github/gh-actions-lock/issues/122) asks for lock-backed
DG reporting; it is a reason to measure and coordinate, not a public delivery promise.
