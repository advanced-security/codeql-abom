# An expression ABOM could not resolve is not a dependency

**Status:** planned. Found by the interface audit on 2026-09-07.

## Why this exists

A workflow may write `uses: ${{ matrix.action }}`. ABOM cannot resolve that without evaluating the
expression, and it does not. What reaches the graph is a node whose key begins with `$`.

Three of them are components of the audited bill of materials:

```text
$/
$/.github/actions/ci-setup
$/.github/actions/setup-bot-auth
```

They sort before every real component, so **the first three rows of the document are artifacts**.
Worse, `db/edge-relationships.md` classifies them: splitting `$/.github/actions/ci-setup` on the
first slash yields the owner `$`, which differs from the containing repository's owner, so **five
relationships are recorded as `cross-owner`**. ABOM is stating that the organization has a
third-party dependency on an account named `$`.

That is a verdict the data does not support, produced by ABOM rather than by a query — the failure
the first architectural invariant exists to prevent. The boundary classification was careful about
the case it anticipated: a target that names no repository at all stays unknown, and `$/` is
correctly among the 81 unclassified. It was not careful about the case where an unresolved
expression happens to *look* like `owner/name`.

## What is already true

| | |
| --- | --- |
| `$/` | `node_kind = 'action'`, owner `$`, repo NULL — unclassified, correctly |
| `$/.github/actions/ci-setup` | owner `$`, repo `.github/actions/ci-setup` — classified **cross-owner** |
| relationships targeting `$…` | 7, of which **5 cross-owner** and 2 unknown |
| bill of materials | all three listed as components, sorted first |
| Explorer | drawn as ordinary action nodes |
| any marking as unresolved | **none** |

The count is small. The defect is that it is a fabricated claim of the exact kind the architecture
is built to refuse, and it is the first thing a reader sees.

## The design

An unresolved reference is a **state**, not a name. It is recorded, it is counted, and it is
reported as something ABOM could not resolve — never as a component with an owner.

- **Detected where it is extracted**, not pattern-matched downstream. The query knows it emitted
  an expression; the Go side is guessing if it looks for a leading `$`.
- **Excluded from boundary classification**, joining the unknown class that `docker://` targets
  already occupy for the same reason: it does not cross an account boundary, because there is no
  account.
- **Kept in the document and counted in coverage.** Dropping them would be the opposite failure —
  a workflow that uses an unresolvable action is a real gap in the picture, and the bill of
  materials states gaps rather than hiding them.
- **Reported as a coverage figure**: "N references could not be resolved", beside the analyzed and
  unanalyzed counts, which is where a reader already looks for what is not known.

## Phases

1. The extraction-side signal, and what the query can actually tell us.
2. A node state for unresolved, and exclusion from classification.
3. Coverage counts it, in the bill of materials and on Discovery.
4. The interface renders it as unresolved rather than as an action named `$`.

## Verification

- No relationship targeting an unresolved expression carries a boundary class.
- The bill of materials counts them and does not sort them first.
- A workflow using `uses: ${{ ... }}` is still visible as a gap, not silently dropped: the
  component count including unresolved, and the count excluding them, both stated.
- On the audited corpus: 3 components and 7 relationships change treatment, 5 of them stop being
  reported as cross-owner.

## Open questions

- **Whether some expressions are resolvable.** `${{ env.ACTION_REF }}` with a literal `env:` block
  is resolvable in principle. Doing that is a CodeQL dataflow question and a much larger plan;
  this one only stops ABOM asserting an answer it does not have.
- **Whether `docker://` should share the state.** Both are "not a GitHub repository", but a
  container image is a resolved reference to a real thing, and an unevaluated expression is not.
  Probably two states, both unknown to the boundary classifier.
