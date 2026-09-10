# A declaration nobody made is not a declaration

**Status:** A declaration records how it arrived — `cli`, `interface` or `config` — *contradicted by
`gh`* is its own state, and a contradicted declaration roots nothing. What is left is telling the
reader.

## Why this exists

`abom_owned_scope` is the gateway to everything ABOM presents as yours — discovery, pruning, the
bill of materials, every "yours" figure. A row written by `codeql-abom own`, by the editor, by
`curl`, or by anyone with write access to the file is otherwise indistinguishable from any other.
Measured once on this corpus: two declared accounts the operator does not belong to took the
operator's repositories from 1 to **45**, and the bill of materials named `microsoft/TypeScript`
as theirs to remediate.

This is the first architectural invariant applied to ownership instead of vulnerability: ABOM
refuses a verdict it did not earn from a rule id, and must equally refuse one from a table row.

## Pending

1. **The bill of materials states what it is rooted in, and how that was established**, beside
   where it already states coverage. `AccountService.AnyVerified` goes with it: computed from the
   declared set, it makes "figures are reported split" turn on declaring, never on `gh` agreeing.
2. **The first run asks.** A first-run sequence owns the flow; this plan owns what the answer
   means. Affirming is one action and it is the operator's — not a dialog to dismiss, but the
   Settings page refusing to draw an ownership mark it cannot account for.
3. **`discover --org` is not constrained to the declared scope.** `cmd/codeql-abom/discover.go`
   never reads `OwnedScope`; the supervisor's preflight asks only whether *something* is declared,
   not whether the organization being enumerated is that something. Cataloguing an undeclared
   account asserts no ownership, so this is narrower than the bug that built a corpus of somebody
   else's repositories — but it spends quota on an account nobody claimed, and the three ways to
   name an organization should agree. **The decision is whether `--org` is refused outside the
   declaration, defaults to it, or stays a free argument.**
4. **Deferred by decision: finer roles, and a scoped token.** `owner`/`admin` is a stand-in for
   "can act on this account". The honest version asks the credential what it may do rather than
   reading a role name, and would also cover a machine account that is neither owner nor admin.

## The constraint that must not be broken

**Contradicted and unproven are different states, and only one of them may hold a declaration
back.** `gh api user/memberships/orgs` lists what you belong to and never states that you do not
belong to something, so *absent from the list* covers three cases: you are genuinely not a member,
the token lacks `read:org`, or the account lives on a host this identity did not enumerate. Only
the first is evidence against the operator. A declaration is held **only** when a recorded scope
says the list could have been complete; a gate built on the other two locks someone out of their
own bill of materials.

**Ownership is stated, never inferred.** An account `gh` does not report at all stays declarable
by name. An account `gh` does report, with a role that can change nothing, is refused.

**A local file is a declaration channel, never a verification bypass.** `abom.local.json` exists so
that rebuilding a database does not mean retyping the declaration, and it fills a vacuum only: it
never replaces a declaration somebody made, and what it writes is verified exactly like what was
typed. A file that could skip verification would be a back door wearing a convenience's clothes.

## Verification

- A row written by `codeql-abom own` says `cli`; one written from the page says `interface`; one
  read from the local file says `config`; one already in the file says nothing and is not attributed
  to any of them.
- An account pre-claimed by the local file and contradicted by `gh` roots nothing, exactly as if it
  had been typed. If the file can skip verification, it is a back door rather than a convenience.
- An account `gh` contradicts can still be declared, is shown so the operator can correct it, and
  roots nothing while it stands.
- Losing network access moves an account to *unknown*, never to *contradicted*, and holds nothing.
- A stored verification that predates the scopes column cannot contradict anything, so the gate is
  inert until something refreshes, and the page must say which state it is in.

## Open questions

- **Whether `enabled` already means this.** An unaffirmed declaration could simply be disabled,
  which discovery already ignores. Against it: `enabled` is the operator's own switch for a
  temporarily out-of-scope account, and overloading it would make "I turned this off" and "nobody
  has vouched for this" the same state.
- **What affirmation costs when the declaration is large.** Forty-four repositories is one
  sentence; four thousand is not, and an affirmation nobody reads is theatre.
