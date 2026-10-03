# Ownership is declared, and discovery is gated on it

**Status:** Built. Ownership is declared rather than inferred, and discovery refuses to start until
it is. Several accounts are declarable, and [the vision](../../docs/vision.md) keeps ownership
declared organization by organization now that an enterprise is the unit.

## Why this exists

ABOM's root set arrived by accident twice over. `repository.origin` is `TEXT NOT NULL DEFAULT
'seed'` and `UpsertRepository` never sets it, so every ingest path silently created a root-set
member — not because anything decided it should, but because the column has a default and nobody
passed a value. Ownership arrived the same way: `abom_account` is filled by `gh auth`, so it held
every organization the operator is a *member* of, including `github`, `codeql` and `docs`, and the
ownership predicate matched the frontier's owner against that table.

Being in an organization does not make its supply chain anyone's remediation work. The distinction
the project needs is narrower and has to be stated, not inferred: **the accounts whose repositories
the operator both has the privilege to analyze and the responsibility to remediate.**

## Pending

1. **Deferred by decision: exclude lists.** `abom_owned_include` covers "only these". There is
   deliberately no "all except these", because the failure mode of an exclude list is that a
   repository created after the list was written is silently in scope — a root set that grows
   without anyone deciding, which is the problem this plan exists to end. Add it only if the opt-in
   list proves unusable at scale.

   **A policy exclusion is a different object and is not this.** It waives a rule for something
   already in scope, and never widens or narrows what is discovered. It is refused as a pattern for
   the same reason this one is — see the waiver rules in
   [`../../.github/instructions/architecture.instructions.md`](../../.github/instructions/architecture.instructions.md).
2. **Deferred by decision: repository-level declaration across accounts.** A repository owned by an
   account you do not own — a fork you maintain — has no representation here. Deferred until
   someone needs it.
3. **The pre-flight does not check per-account permissions.** `gh` verification is recorded and
   shown beside each declaration, and the gate asks only whether a scope is declared. Checking that
   the operator holds the access each declaration claims is worth doing, and is not what stops a
   crawl from being unbounded.
4. **`OwnedSet` is owner-granular**, so an account restricted by an include list still marks the
   whole owner in the pinning split. Callers judging a *repository* use `RepositoryIsOwned`, which
   honors the list. Built as a known approximation rather than discovered as one; close it when the
   split has to be exact.

## The constraint that must not be broken

**A declaration is the only source of ownership, and every "yours" figure reads it through one
predicate.** `OwnedSet` once read the declaration table directly and walked straight past the
honored view, feeding `expand`, `pinning_coverage` and `OwnerCandidates` — three "yours" answers,
one of them ungated. It was found by writing a test that asserted the rule, not by reading the
code. A second reader is how membership quietly becomes responsibility again.

**An empty include list on an include-governed account scopes to nothing.** Removing the last
include may not silently widen an account to all of it.

## Verification

- With nothing declared, `Start` is refused with a reason and a remedy, and the Settings page says
  discovery will not start.
- The scope is refused **before** the toolchain: declaring what you own is something the operator
  can act on, and a missing CodeQL CLI on a machine that has one answers the wrong question.
- Declared-but-disabled is refused the same as nothing declared. The personal account starts that
  way, so it is the state a first run is in.
- An include naming an account that was not declared is refused rather than silently restricting
  nothing.
- A declaration `gh` cannot confirm is kept and flagged, never dropped. You can be responsible for
  something you have just lost access to.
