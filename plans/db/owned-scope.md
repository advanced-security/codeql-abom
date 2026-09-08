# Ownership is declared, and discovery is gated on it

**Status:** shipped. Ownership is declared rather than inferred, and discovery refuses to start
until it is. The corpus check found the declaration is not merely narrower than membership — on
this corpus it is **empty**, which is recorded below because it is the strongest possible argument
for the two plans that follow.

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

## What the corpus check found

The plan predicted the difference between membership and declaration "should be large". It is
total.

| Tier | From the 33 `gh` accounts | Declared, nothing stated | Declared: 5 accounts |
| --- | ---: | ---: | ---: |
| owned | **238** (221 analyzed) | 0 | **2** (0 analyzed) |
| direct | 1,533 (1,324) | 1,600 (1,386) | 1,600 (1,386) |
| transitive | 1,891 (1,255) | 2,062 (1,414) | 2,060 (1,414) |

The 238 redistribute exactly — 67 to direct and 171 to transitive — so nothing is lost or
double-counted when the owned tier empties.

The declaration used was the five accounts `gh` reports the operator as **owner or admin** of:
`data-douser`, `advanced-security`, `ActionsDesk`, `Pii-Hole-Engineering`, `ql-mcp-playground`.
Against that:

**0 of the 204 repositories in scope belong to a declared account.** They are `facebook` (18),
`microsoft` (16), `google` (13), `spring-projects` (10), `aws` and `alibaba` (9 each),
`tauri-apps` and `github` (8), `cloudflare` and `chef` (7) — the `gh mrva` bootstrap, and nobody's
responsibility here.

The only trace of a declared account anywhere in 56,830 nodes is
**`advanced-security/dismiss-alerts`, reached as an upstream dependency at depth 2, pending, never
analyzed**. The one thing the operator actually owns is queued behind somebody else's supply
chain. That single row is the whole argument for `db/discovery-boundary.md`, and it was not
predicted — the plan assumed a smaller owned set, not an absent one.

## What reconciling corrected, before any code changed

**The gate already exists; only its predicate is missing.** The plan described refusing to start
as something to build. `Supervisor.Start` already consults a `Preflight func() *Problem` hook,
records the problem on its status, logs `cannot start: <reason> (<remedy>)`, and
`POST /api/discovery/start` already serves it — which the Discovery page already renders. The hook
is wired from `cmd/codeql-abom/discovery.go`, where `preflight` today checks for the CodeQL CLI,
for git, for a readable query and for a writable work directory. Declared scope is **one more
clause in that function**, not new machinery.

**The schema already names the ownership notions and says none of them means what we need.**
`abom_account`'s comment reads: *"It is kept apart from the other two ownership notions.
`repository.origin` records how a repository entered the corpus; an account holding a seed
repository is 'in scope'. Neither means the operator can change anything: the `actions` account
owns a seed repository and nobody running this tool controls it."* That is this plan's argument,
written before the plan was. The declared scope is the fourth notion and the only one that means
responsibility, and that comment should say so rather than be contradicted.

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

- With nothing declared, `Start` is refused with a reason and a remedy, and the Settings page says
  discovery will not start.
- The scope is refused **before** the toolchain, because declaring what you own is something the
  operator can act on and a missing CodeQL CLI on a machine that has one answers the wrong
  question.
- Declared-but-disabled is refused the same as nothing declared. The personal account starts that
  way, so it is the state a first run is in.
- An include list admits only what it names, and an *empty* include list on an include-governed
  account scopes to nothing — removing the last include cannot silently widen an account to all
  of it.
- An include naming an account that was not declared is refused rather than silently restricting
  nothing.
- A declaration `gh` cannot confirm is kept and flagged, never dropped.
- Every ownership test that read `gh` was **narrowed rather than deleted**: eight of them, each
  still protecting what it always did — ownership beats depth, the tiers are exclusive, a requeue
  takes what it counted, the split halves sum to the whole — with only the source of ownership
  moved. `verifies` became `declares`, and the two tests genuinely about `ReplaceAccounts` now
  assert against what was stored rather than against `OwnedSet`.
- The seven bUnit tests passed the moment they were written, so `AdoptScope` was deliberately
  broken and four of the seven watched to fail before it was restored.

## Open questions

- **Exclude-lists.** `includes` covers "only these". There is deliberately no "all except these";
  add it only if the opt-in list proves unusable at scale.
- **Repository-level declaration across accounts.** A repository owned by an account you do not
  own — a fork you maintain — has no representation here. Deferred until someone needs it.
- **`OwnedSet` is owner-granular.** An account restricted by an include list is still the
  operator's account, so the pinning split marks the whole owner. Callers judging a *repository*
  use `RepositoryIsOwned`, which honors the list. Shipped as a known approximation rather than
  discovered as one.
- **The pre-flight does not yet check per-account permissions.** `gh` verification is recorded and
  shown beside each declaration, and the gate asks only whether a scope is declared. Checking that
  the operator holds the access each declaration claims is worth doing and is not what stops a
  crawl from being unbounded.
- **What happens to the 204 existing seeds** is `db/discovery-boundary.md` and `db/pruning.md`.
  This plan only makes the correct answer expressible — and, as the corpus check shows, the
  correct answer is that none of them belong.
