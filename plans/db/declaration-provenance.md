# A declaration nobody made is not a declaration

**Status:** planned, and blocking. Found on 2026-09-07 by the operator reading their own Settings
page and saying: *"I own neither facebook nor microsoft and never have. You never actually made me
choose ownership for myself."*

## Why this exists

On the reference corpus, right now:

| | |
| --- | --- |
| accounts `gh` reports the operator belongs to | **33** |
| of those, `facebook` or `microsoft` | **0** |
| accounts declared in `abom_owned_scope` | `facebook`, `microsoft` |
| repositories the bill of materials is rooted in | **44**, every one of them Facebook's or Microsoft's |
| what the operator actually owns in this corpus | **1** — `advanced-security/dismiss-alerts` |

So ABOM is producing a bill of materials that names `microsoft/TypeScript`, `facebook/rocksdb`
and `microsoft/calculator` as this operator's to remediate. `db/owned-scope.md` **already measured
the truth** and wrote it down — "the one thing the operator owns is queued behind somebody else's
supply chain" — and then a declaration was made that contradicted it, and every measurement since
has been taken against that declaration without anyone noticing.

Including the interface audit of the same day, which recorded *2 declared accounts ✓* as a passing
cross-check, and `ui/owned-scope-editor.md`, which verified its preview against 44 roots, a
closure of 486 and 2,292 queued rows. Those numbers are arithmetically right and describe
somebody else's supply chain.

**This is the first architectural invariant, applied to ownership instead of vulnerability.** ABOM
refuses to derive a security verdict it did not earn from a rule ID. It is currently deriving an
*ownership* verdict it did not earn from a row in a table.

## What is already true

| | |
| --- | --- |
| `abom_owned_scope` | `owner`, `kind`, `enabled`, `includes_only`, `declared_at` |
| what it records about who declared it, or how | **nothing** |
| `ghauth.Verify` | returns the accounts the operator belongs to, each with a role |
| `ghauth.Problem` | already separates *gh is broken* from *gh answered* |
| `store.Account.CanDeclare` | **shipped** — `owner` or `admin` only, decided in Go and carried to the interface as `canDeclare` |
| the membership list | **shipped** — offers only declarable accounts, 13 of 33 on this operator's identity |
| the declared table | **shipped** — carries the role `gh` reports, or *not reported* |
| discovery, pruning, the BOM, every "yours" figure | all bounded by this table |

A row written by `codeql-abom own`, by the editor, by `curl`, or by anyone with write access to
the file is indistinguishable from any other. The architecture instruction says ownership is
"declared by the operator, confirmed by `ghauth`" — the store cannot substantiate the first half.

**The eligibility gate shipped on 2026-09-07 and closes the front door, not the back one.** A
member role can no longer be *offered* for declaring, and a declaration carrying one is drawn as
such. Nothing stops the same row being written directly, and nothing distinguishes a row the
operator made from one that was already in the file — which is the whole of this plan.

## The design

### Belonging is not being answerable

`gh` reports **33** accounts for this operator and they are answerable for **13**. A `member` can
read an organization and change nothing in it, so offering all 33 as declarable is what let
`facebook` be declared in the first place. Eligibility is `owner` or `admin`, decided once in
`store.Account.CanDeclare` and carried to the interface as `canDeclare` rather than re-derived
there from the role string.

This is deliberately coarse and is **not** the intended end state. GitHub has finer roles, and a
scoped token would answer the real question — what this credential may actually do — rather than
inferring it from a role name. Until then, coarse and conservative: the failure mode of being too
narrow is an operator who has to declare by name, and the failure mode of being too wide is a bill
of materials addressed to the wrong person.

An account `gh` does not report at all stays declarable by name, because ownership is stated rather
than inferred. An account `gh` *does* report, with a role that can change nothing, is refused —
`gh` was asked and answered.

### Contradicted is not unconfirmed

Three states, and they currently collapse into two:

- **Unknown.** `gh` has not been asked, or could not be reached. `ghauth.Problem` carries this
  already, with the command that fixes it.
- **Confirmed.** `gh` answered and the operator belongs to the account.
- **Contradicted.** `gh` answered, listing 33 accounts, and this is not one of them.

Today the last two both render *not confirmed by gh*, in amber, beside a green ownership check
that outweighs them. A declaration `gh` contradicts is not a gap in evidence — it is evidence
against, and it has to look like it.

The advisory rule from `ui/owned-scope-editor.md` stands and is not in tension with this:
verification must never *block* a declaration, because an operator can be responsible for an
account they have just lost access to. It may, and must, refuse to present a contradicted
declaration as settled fact.

### A declaration records how it arrived

`declared_by` on `abom_owned_scope`: `interface`, `cli`, or NULL. **NULL means unknown, and rows
that predate the column keep it** — relabeling them `cli` would invent the very provenance this
is for. Additive, through `addedColumns`, because an expansion runs for weeks.

### Nothing is presented as yours until somebody says it is

A declaration that is unknown-provenance or contradicted is *held*, not honored: the interface
already knows how to report every figure undivided, and says so — "Nothing verified — every figure
is reported undivided". That is the correct reading for a scope nobody has affirmed, and it is a
far better first run than 2,548 components attributed to the wrong person.

Affirming is one action and it is the operator's. It is not a dialog to dismiss; it is the
Settings page refusing to draw an ownership mark it cannot account for, next to a control that
resolves it.

## Phases

1. `declared_by`, additive, NULL for everything that exists.
2. The three verification states, in the store's view and on the page. Contradicted loses the
   ownership mark.
3. Figures stay undivided while any enabled declaration is contradicted or unaffirmed, rather than
   being attributed to the operator.
4. The bill of materials states what it is rooted in and how that was established, at the top,
   where `api/bill-of-materials.md` already states coverage.
5. The first run asks. `ui/first-run.md` owns the sequence; this owns what the answer means.

## Verification

- On this corpus as it stands, the bill of materials **does not** present 44 Facebook and
  Microsoft repositories as the operator's.
- Declaring the 13 accounts `gh` reports as owner or admin yields **1 root, a closure of 270 and
  1,642 queued rows** — which is exactly what `db/discovery-boundary.md` measured for the honest
  configuration, so the two agree.
- A row written by `codeql-abom own` says `cli`; one written from the page says `interface`; one
  already in the file says nothing and is not attributed to either.
- An account `gh` contradicts can still be declared, and does not carry the ownership mark.
- Losing network access moves an account to *unknown*, never to *contradicted*.

## Open questions

- **Finer roles, and a scoped token.** `owner`/`admin` is a stand-in for "can act on this account".
  The honest version asks the credential what it may do rather than reading a role name, and would
  also cover a machine account that is neither owner nor admin but holds exactly the rights this
  needs. Deferred deliberately on 2026-09-07, not overlooked.
- **Whether `enabled` already means this.** An unaffirmed declaration could simply be disabled,
  which discovery already ignores. Against it: `enabled` is the operator's own switch for a
  temporarily out-of-scope account, and overloading it would make "I turned this off" and "nobody
  has vouched for this" the same state — the exact collapse this plan exists to undo.
- **What affirmation costs when the declaration is large.** Forty-four repositories is one
  sentence; four thousand is not, and an affirmation nobody reads is theatre.
- **Whether the reference corpus should be repaired or kept as evidence.** Repaired on 2026-09-07
  by the operator, to `data-douser` and `advanced-security`. No corpus now exercises a false
  declaration end to end, which is worth remembering when testing this plan.
