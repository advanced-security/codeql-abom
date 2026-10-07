# Ownership is declared, and discovery is gated on it

**Status:** Built. Ownership is declared rather than inferred, discovery refuses to start until
it is, several accounts are declarable, and [the vision](../../docs/vision.md) keeps ownership
declared organization by organization. Every surface says *owned* only where the declaration covers
a row's repository; *What you own* (`scope=owned`) holds exactly that, and *Yours and what it runs*
(`scope=supply-chain`) is the closure. Settings asks `gh` on arrival, offers the accounts it can
declare, and builds an include list from GitHub's own answer, or a property scope from the
organization's custom properties. A repository administered inside an organization the operator
does not own still cannot be declared, deliberately: see item 2.

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
2. **Deferred: a member declaring the repositories they administer.** In an organization where
   the operator is only a member, the account as a whole is not declarable, and a repository they
   administer there has no representation. Deferred because nothing could read what such a
   declaration names, not for want of a picker. Measured with the operator's own token on
   2026-10-06:
   - **Every one checked is private**: `ps-resources/es-offerings-ops`, `ps-resources/github-msft`,
     `TaxPilotAI/TaxLing`, `els-ai/ghas-guzzlers`. ABOM reads and fetches through its GitHub App,
     and only an organization owner can install an App that asks for organization permissions,
     which ABOM's does. A repository administrator may install only an App limited to repository
     permissions, and only where the organization allows it. A declaration made today would be
     listed and then never read.
   - **Finding them costs a route choice, and the cheap one misses most.**
     `GET /user/repos?affiliation=collaborator` is one call in 1.3 s and finds 11 outside owned
     organizations (`github` 9, `TaxPilotAI` 1, `els-ai` 1), only direct grants. Team grants find
     208 more (206 in `github`, 2 in `ps-resources`) at 31 teams and 172 s. Listing a whole
     organization and keeping `permissions.admin` is complete and costs its size: `dsp-testing` is
     4,454 repositories, 45 pages, 84 s.
   - **`dsp-testing`'s 14 are all temporary GHSA advisory forks** (`…-ghsa-…`), granted through
     advisory collaboration, which neither the collaborator nor the team route reports. They are
     not repositories anyone should declare, so they are not the fixture they were taken for.

   Resumes when one of these holds: the App is installed on the repositories in question (an owner
   approves a request), or reading a member's declaration with the operator's own token is decided
   as a credential the crawl may use. The owned tier and the pinning split already decide per
   repository, so 11 declared repositories in `github` would be the owned tier and no other
   `github` repository would join it; only Settings' owner-candidate mark stays account-level, and
   it says *declared in part*. Enumeration still lists an include-only account whole before
   filtering it, which for `github` is about 192 pages every full listing.

   **Constraints this must keep:** membership still decides nothing on its own; listing is
   read-only against GitHub and paid from the reader's own token, never the App's; every name
   offered comes from GitHub's answer, never from the store.
3. **The pre-flight does not check per-account permissions.** `gh` verification is recorded and
   shown beside each declaration, and the gate asks only whether a scope is declared. Checking that
   the operator holds the access each declaration claims is worth doing, and is not what stops a
   crawl from being unbounded.
4. **No browser gate draws the declaration picker.** The e2e server's `gh` is a stub that answers
   as logged out (`ui/tests/e2e/stubs/gh`), so the declarable list never appears, and the fixture
   corpus declares `acme-corp` by property scope and the other three whole, with no include list,
   so neither an include chip, the chooser nor the property scope editor is drawn by `make e2e` or
   `make e2e-corpus`; the scope lines are. bUnit holds their behavior; their layout
   and accessible names have only been measured by hand, on a copy of the crawled store. Two
   recorded answers would close it: the stub replaying `api -i user`, `user/memberships/orgs` and
   one `orgs/{o}/repos` from fixtures, and one corpus account declared with an include list. The
   stub must stay a recording, never a pass-through to the real `gh`: without it, opening Settings
   wrote whoever ran the suite into the store under test (33 identity rows).
5. **A declaration that could not be read is shown as no declaration.** `GetOwnedScopeAsync`
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
- **Item 2, when it resumes:** a member's administered repository is offered only in an
  organization gh reports a member role for, is saved as an include list there, and is read by
  enumeration and fetched by the crawl; an owned-tier count taken before and after declaring one
  repository in a large member organization moves by that repository alone.
