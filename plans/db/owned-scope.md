# Ownership is declared, and discovery is gated on it

**Status:** planned. Nothing here is built. This is the foundation the four plans after it stand
on, and until it lands every "yours" figure in the interface is answering a question nobody asked.

## Why this exists

ABOM's root set arrived by accident twice over.

`repository.origin` is declared `TEXT NOT NULL DEFAULT 'seed'` in `internal/store/schema.go`, and
`UpsertRepository` never sets it. So **every ingest path silently creates a root-set member** —
not because anything decided it should, but because the column has a default and nobody passes a
value. The 204 repositories currently `origin='seed'` came from `discover --org` runs against
whatever had a `gh mrva` database, which is a fine way to bootstrap a project and a poor
description of what anyone is responsible for.

Ownership arrived the same way. `abom_account` is filled by `gh auth`, so it holds **33 accounts**
— every organization the operator is a *member* of, including `github`, `codeql`, `docs` and
`els-ai`. `store.isOwned` matches the frontier's owner against that table:

```sql
EXISTS (SELECT 1 FROM abom_account a WHERE LOWER(a.owner) = LOWER(abom_frontier.owner))
```

which means the Owned tier, the "yours" split on Version Pinning, the `UsedByOwned` count on
Discovery and the ownership marks on every node page all inherit a definition that is *membership*
rather than *responsibility*. Being in the `docs` organization does not make `docs`'s supply chain
anyone's remediation work.

The distinction the project needs is narrower and has to be stated, not inferred: **the accounts
whose repositories the operator both has the privilege to analyze and the responsibility to
remediate.** Nothing can infer that. It has to be declared.

## What is already true

Measured on the reference corpus:

| | |
| --- | --- |
| repositories `origin='seed'` | **204** |
| accounts in `abom_account` from `gh` | **33** |
| of those, organizations the operator merely belongs to | most of them |
| ways to declare an account yours | **none** — `Settings.razor` is read-only |
| ways to seed | one: `discover --org <name>`, one org per invocation |
| discovery preconditions | **none**; Start works against an empty configuration |

`internal/ghauth` already reads `gh auth status` and produces `Account{Owner, Kind, Role}` with
`role` in `owner` / `admin` / `member`. That machinery is worth keeping — as *evidence about a
declaration*, which is a different job from being the declaration.

## The design

### A configuration, not an inference

Two tables, because a declaration and its exceptions are different shapes:

- `abom_owned_scope(kind, owner, enabled, includes_only, declared_at, declared_by)` — one row per
  declared account. `kind` is `organization` or `user`.
- `abom_owned_include(owner, repo)` — the optional per-account allow-list.

**A non-empty include list means only those repositories.** Empty means the whole account. This is
opt-in semantics deliberately: the failure mode of an exclude-list is that a repository created
after the list was written is silently in scope, and a root set that grows without anyone deciding
is the problem this plan exists to end.

**The operator's own user account is a row like any other, with `enabled = 0`.** It is inherently
scoped to one person and is not what most operators mean by "what we own", so it is opted into
rather than out of.

### Configured before a crawl, or during one

- `codeql-abom own --org acme --org acme-labs --include acme-labs/tooling --user` writes the
  configuration ahead of any discovery. This is the pre-seeding path, and it is what a scripted or
  CI-driven run uses.
- `PUT /api/owned-scope` writes the same rows at run time, which is what the interface uses.

One representation, two ways in, so the command line and the page cannot drift into two answers
about what is owned — the same argument that moved requeue selection into `expand.RequeueStale`.

### `gh` becomes a pre-flight check

Verification stops being the source of ownership and becomes a check *on what was declared*: for
each declared account, does the authenticated identity hold the access a crawl needs? The answer
is recorded with its timestamp as evidence, exactly as `abom_account` records one now.

Declaring an account `gh` cannot confirm is allowed and flagged. You can be responsible for
something you have just lost access to, and the interface saying so is more useful than refusing
to hold the fact.

### The gate

Discovery `Start`, and `expand-all` on the command line, **refuse with a reason** when:

- no account is declared, or
- pre-flight has never run, or
- pre-flight last failed for every declared account.

This is the user's requirement stated as code: no discovery until the information and the
verification are in hand. The reason travels to the interface, because a disabled Start button
that does not say why is the control that "did nothing" — the same failure the queue-state rule
already names.

### Everything downstream re-points

`store.isOwned` and `tierOfRow` read the declared scope instead of `abom_account`. `discover`
enumerates the declared set rather than taking one `--org` at a time. `Settings.razor` stops being
a read-only report and becomes the editor, with the `gh` result shown beside each declaration as
evidence rather than as the source.

## Phases

1. Schema, the two tables, and the store reads. Written first because every later phase asks them
   questions.
2. `store.isOwned` / `tierOfRow` re-pointed. This alone moves the tier counts, and the move is the
   first measurement.
3. `codeql-abom own`, and the pre-flight check against the declared set.
4. The gate on `Start` and `expand-all`, with its reason.
5. `PUT /api/owned-scope` and the Settings page as editor.

## Verification

- With nothing declared, `Start` is disabled, `expand-all` exits non-zero, and both say why.
- With `acme` declared, the root set is exactly `acme`'s repositories; with
  `acme-labs includes tooling`, it is exactly that one repository from that account.
- The operator's personal account contributes nothing until it is enabled.
- Tier counts before and after re-pointing `isOwned`, on the real corpus: the owned tier is
  currently keyed on 33 accounts and will be keyed on the declared few. The difference is the
  measurement, and it should be large.
- A declared account that `gh` cannot confirm is kept, flagged, and still gates nothing but its
  own pre-flight line.

## Open questions

- **Exclude-lists.** `includes` covers "only these". There is deliberately no "all except these";
  add it only if the opt-in list proves unusable at scale.
- **Repository-level declaration across accounts.** A repository owned by an account you do not
  own — a fork you maintain — has no representation here. Deferred until someone needs it.
- **What happens to the 204 existing seeds** is `db/discovery-boundary.md` and `db/pruning.md`,
  not this plan. This plan only makes the correct answer expressible.
