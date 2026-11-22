# Ownership is declared, and discovery is gated on it

**Status:** Built. Ownership is declared rather than inferred, discovery refuses to start until
it is, several accounts are declarable, and [the vision](../../docs/vision.md) keeps ownership
declared organization by organization. Every surface says *owned* only where the declaration covers
a row's repository; *What you own* (`scope=owned`) holds exactly that, and *Yours and what it runs*
(`scope=supply-chain`) is the closure. Settings asks `gh` on arrival, offers the accounts it can
declare, and builds an include list from GitHub's own answer, or a property scope from the
organization's custom properties. Who may declare — an owner, an admin, a security manager, an App
installation, and a member for the repositories they administer or maintain — and the check beside
each declaration of the role behind it are in [`docs/architecture.md`](../../docs/architecture.md);
what is left of them to measure is [production credentials](../api/production-credentials.md).

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
2. **No browser gate draws the declaration picker.** The e2e server's `gh` is a stub that answers
   as logged out (`ui/tests/e2e/stubs/gh`), so the declarable list never appears, and the fixture
   corpus declares `acme-corp` by property scope and the other three whole, with no include list,
   so neither an include chip, the chooser nor the property scope editor is drawn by `make e2e` or
   `make e2e-corpus`; the scope lines are. bUnit holds their behavior; their layout
   and accessible names have only been measured by hand, on a copy of the crawled store. Two
   recorded answers would close it: the stub replaying `api -i user`, `user/memberships/orgs` and
   one `orgs/{o}/repos` from fixtures, and one corpus account declared with an include list. The
   stub must stay a recording, never a pass-through to the real `gh`: without it, opening Settings
   wrote whoever ran the suite into the store under test (33 identity rows).
3. **A declaration that could not be read is shown as no declaration.** `GetOwnedScopeAsync`
   answers a failed read with an empty list, so `AccountService` (and through it Settings' "Nothing
   is declared, so discovery will not start" and every `ScopeDeclared` reader), `DiscoveryControls`
   and `PruneControl` report a transport failure as an empty declaration. The Explorer reads
   through `ReadOwnedScopeAsync`, which returns null instead, and draws no stage until the read
   lands. The others need the same read and a state that says the declaration could not be read,
   which Settings does not have yet.

## The constraint that must not be broken

**A declaration is the only source of ownership, and every "yours" figure reads it through one
predicate.** `OwnedSet`, since renamed `DeclaredOwners` and kept to the account-level question,
once read the declaration table directly and walked straight past the
honored view, feeding `expand`, `pinning_coverage` and `OwnerCandidates` — three "yours" answers,
one of them ungated. It was found by writing a test that asserted the rule, not by reading the
code. A second reader is how membership quietly becomes responsibility again.

**An empty include list on an include-governed account scopes to nothing.** Removing the last
include may not silently widen an account to all of it.

**Provenance is never ownership on screen.** A row says *owned* because the declaration covers its
repository, never because it was seeded. `origin` stays the data's provenance, and *starting point*
stays the word for a seed nobody declared.

**The picker offers; it never declares.** Settings lists what `gh` reports and what GitHub says an
account holds, through the reader's own token on a guarded POST, and nothing becomes owned until a
selection is saved. Nothing is preselected, and a name the listing no longer reports stays declared
and marked rather than dropped.

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
- A member's administered repository is offered only in an organization gh reports a member role
  for, is saved as an include list there, and is read by enumeration and fetched by the crawl; an
  owned-tier count taken before and after declaring one repository in a large member organization
  moves by that repository alone. Nobody has measured this on the crawl, which declares only two
  organizations its operator administers.