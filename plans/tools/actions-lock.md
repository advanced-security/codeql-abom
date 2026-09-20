# Use GitHub's tooling to keep workflow locks current

**Status:** native onboarding has been run locally; repeatable checks and platform verification are pending.

## Why this exists

GitHub's Workflow Dependency Pinning effort supplies a native lockfile, verifier and update path.
ABOM should use those rather than build a competing pinning tool. An action can keep a readable
version reference while the workflow lock selects an immutable commit. A full-version tag is still
mutable; the locked commit, not the spelling of the tag, supplies that execution identity.

The generated [actions.lock](../../.github/workflows/actions.lock) records schema `v0.0.2`, and
ABOM's workflows now use tags with their selected commits in that file. A run-details Lockfile link
describes a particular run revision, not proof that every platform enforces the lock or that a local
uncommitted lock affects earlier runs. This plan owns ABOM's workflow/toolchain adoption; consuming
locks during analysis belongs to [actions-lockfile.md](../db/actions-lockfile.md).

## Compatibility baseline

The inspected combination is CLI `v0.1.6`, its Go parser dependency `v0.0.4`, and emitted lock schema
`v0.0.2`. Dependabot's source recognizes that schema and invokes the CLI to update tracked locks.
The shared parser's development branch also describes `v0.0.3`; that does not make the newer schema
supported by the installed CLI or deployed consumers. Pin tested releases and record a compatibility
matrix rather than following `main` or treating CLI and schema versions as the same thing.

The CLI is a technical preview. Its README describes runner enforcement; deployment availability,
host support and the actual execution of this repository's workflows still need verification.

## Pending

1. **Review native onboarding as one change.** Compare workflow refs, local `./` to `$/` migrations
   and lock entries together. Account for every selected commit that differs from the former inline
   pin, including version narrowing. Never hand-edit the generated lock or rerun fix mode over
   unreviewed user changes. Confirm workflow coverage, not just a count of deduplicated packages.
2. **Pin and verify the toolchain.** Select an exact released CLI with verified distribution
   provenance, and record the parser/schema/platform combinations it supports. Explicitly install
   required tools in the relevant job; absence is a setup failure, not a skipped check. Coordinate
   upgrades with Dependabot compatibility rather than automatically accepting newer schema output.
3. **Check without rewriting.** Add offline coverage validation for workflow, action-metadata and
   lock-only changes. Prefer a suitable existing always-run check before creating another workflow;
   required-check path filters must not leave pull requests waiting for an unreported status.
   Make authenticated network verification separately named and budgeted. Depends on 2.
4. **Measure native execution semantics.** On an explicitly approved target with benign actions,
   test onboarded and unonboarded workflows, missing entries, a moved tag with an older locked SHA,
   malformed locks, `$/` references, nested composites and reusable workflows. Record the run
   commit, lock blob, host/runner and observed selected commits. Establish caller/callee lock
   precedence with the resolution plan before ABOM claims to reproduce it. Depends on 1 and 2;
   local fixtures can begin without a live target, but cannot establish deployed enforcement.
5. **Exercise the native update path.** Inspect a Dependabot update of an onboarded workflow and
   its lock together, including transitive changes. Document what updates automatically and what
   still requires explicit onboarding or operator review. Existing Dependabot configuration is the
   starting point, not a reason to add another updater. Depends on 2 and an approved test target.
6. **Document the supported workflow.** Explain onboarding, review, no-fix checking, verification
   and upgrades, including native coverage gaps and the role of explicit SHA pins on unsupported
   platforms. Update the old SHA/comment-only explanation in the Dependabot configuration when
   adoption is implemented. Never automate acceptance of moved or unreachable pins. Depends on
   the preceding evidence, not only a successful local generation.

## Read-only commands

These are the proposed check invocations, to verify in isolated source trees before wiring CI:

```sh
gh actions-lock --verify-local --no-fix --no-interactive --json
gh actions-lock --verify --no-fix --no-interactive --json
```

In `v0.1.6`, `runVerifyLocal` can migrate local references unless `--no-fix` is set, even though
the mode is offline. `--json` selects output only. Always specify no-fix; full verification uses
the network, whereas offline coverage cannot prove identity or commit reachability.

## The constraints that must not be broken

**GitHub owns lock generation and verification.** ABOM neither writes a competing format nor
implicitly relocks, accepts moved refs or rewrites workflows. A read path cannot call default fix
mode. Updating a lock is an operator or native-updater action, visible in review.

**Coverage is not enforcement or code safety.** Parsed, covered, network-verified and observed at
runtime are distinct evidence. A successful exit with inconclusive diagnostics is not proof that
every pin was verified. No native-lock result silently suppresses a CodeQL finding.

**Updater support is not Dependency Graph ingestion.** Dependabot lock updates, DG/SBOM contents,
Dependency Review, advisory alerts and update pull requests need separate evidence. Their native
comparison belongs to [dependency-graph-crosscheck.md](../api/dependency-graph-crosscheck.md).

## Verification

- In an isolated copy, compare source and lock bytes before and after both no-fix commands,
  including a `./` reference eligible for migration. Neither command rewrites inputs.
- Missing entries, unsupported schemas, missing tools, unknown JSON categories and network failures
  produce explicit outcomes. Offline coverage never claims fresh reachability evidence.
- A lock-only pull request triggers the intended check. Rejection fixtures are benign and cannot
  execute untrusted code merely to demonstrate a rejection.
- Confirm selected commits in actual approved test runs, and inspect a real Dependabot update of
  both workflow and lock. Record unsupported environments rather than extrapolating from one host.
- Repository code changes use the required project gates. Live writes or workflow triggers require
  an explicitly approved target; this plan alone is not authorization to run them.

## Sources and attribution

GitHub provides the [native CLI](https://github.com/github/gh-actions-lock/blob/v0.1.6/README.md),
its [no-fix verification behavior](https://github.com/github/gh-actions-lock/blob/v0.1.6/cmd/gh-actions-lock/verify.go)
and [shared parser](https://github.com/github/actions-lockfile/blob/go/v0.0.4/README.md).
The [release guidance](https://github.com/github/gh-actions-lock/blob/main/RELEASING.md) identifies
Dependabot coordination; its [CLI engine](https://github.com/dependabot/dependabot-core/blob/main/github_actions/lib/dependabot/github_actions/lockfile/cli_engine.rb)
is source evidence for updates, not a hosted-rollout guarantee. Resolve moving source links to
tested revisions when implementing. Jesse Houwing's
[request for native DG reporting](https://github.com/github/gh-actions-lock/issues/122) connects this
work to the earlier [actions-dependency-submission investigation](https://github.com/advanced-security/codeql-abom/issues/18).
