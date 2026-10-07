# A coverage gap names its cause

**Status:** deferred. The contract this consumes is
[`gh-ghas-audit` v2](https://github.com/advanced-security/gh-ghas-audit/pull/11), which is open and
unreleased. Nothing here starts before there is a tag.

## Why this exists

`abom_database_catalog` records which repositories have a CodeQL database. A repository with none
may have scanning switched off, a failing workflow, a language nobody selected, or no supported code
at all. ABOM reports the absence honestly and cannot say which it is.

That is the absent-measurement invariant working correctly and still leaving the operator with
nowhere to go. **Coverage is whether ABOM looked; enablement is whether the feature is on and
working.** They are different questions, and the second one has an owner already: `gh-ghas-audit`
measures configuration, execution, freshness and language coverage per repository.

The exchange runs both ways, which is what makes this worth doing rather than merely convenient.
GitHub's repository language statistics do not report `actions`, so an audit built on them
*structurally cannot* tell an operator that a repository is missing Actions analysis. ABOM knows
which repositories hold `.github/workflows/**`. That is the one language gap the audit is blind to.

## Pending

1. **Ingest the report.** JSON is the canonical form and `schema_version` versions the data contract
   independently of the CLI release, so `schema_version` is what ABOM pins. NDJSON exists for the
   sizes where the whole document is not worth holding.
2. **Attach enablement beside coverage**, as an attributed external statement with the audit named
   as its source and the scan depth recorded — `config` reports execution and freshness as
   `not-evaluated` by design, and rendering that as a failure would invent a problem. Depends on 1.
3. **Carry incompleteness through.** The audit is explicit that a zero exit does not mean a complete
   report; `stats.incomplete`, per-repository `status.incomplete`, `errors` and `warnings` are what
   say otherwise. ABOM restates them rather than flattening them into a clean answer. Depends on 1.
4. **Enablement as something a policy may reference**, on the same footing as an advisory. The rule
   stays the operator's. Depends on 2, and on a predicate term the policy language can carry.

ABOM closes the `actions` gap this plan once meant to contribute back itself, by the operator's
decision: it reads each owned repository's default setup, because it must classify setup before it
may add `actions` ([`docs/architecture.md`](../../docs/architecture.md) says how). The audit still
owns execution, freshness and every other language.

## The constraints that must not be broken

**Consume the contract; never import the package.** Every package in that module sits under
`internal/` — `internal/collector`, `internal/model`, `internal/ghapi`, `internal/output` — so Go's
visibility rule forbids importing them at all. This is not a preference to be re-litigated later by
someone who notices the types would be convenient. Even if it were possible, the dependency tree is
four direct and roughly twenty indirect modules, much of it terminal rendering, against a project
with three direct dependencies that analyzes its own supply chain.

**Pin `schema_version`, never a CLI version and never a branch.** The contract is versioned
separately from the tool precisely so a consumer can depend on one without the other.

**An enablement status enriches; it never adjudicates.** Identical to the rule for a GHSA, for the
same reason: it is somebody else's measurement, stored beside ABOM's own and attributed to them. An
audit verdict of `healthy` is not ABOM saying a repository is safe.

**ABOM does not re-implement what the audit measures.** If the answer is wrong, that is an upstream
bug with an upstream owner, not a reason to grow a second implementation here. The one exception is
the default setup reading ABOM needs before it may
write, and it reads that for `actions` alone.

## Verification

- A repository the audit reports as `not-configured` and one it cannot inspect at all are
  distinguishable in ABOM, because only the first is work somebody can do.
- A `config`-depth report does not produce any freshness or execution claim anywhere in ABOM.
- A report whose `stats.incomplete` is set never renders as full coverage, and what was unread is
  readable.
- Every enablement figure names the audit, its scan depth and when it ran. A figure that cannot say
  where it came from is indistinguishable from one ABOM measured.
