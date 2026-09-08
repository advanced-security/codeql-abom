# A bill of materials that survives its own size

**Status:** planned. Found by the interface audit on 2026-09-07, against the page shipped by
`api/bill-of-materials.md`.

## Why this exists

The page works and the document is right. Four things about presenting it are not.

**It renders every row.** No pagination, one DOM. The audit's page snapshot came back at **2.7 MB**
at 2,548 components and was too large to read. The Settings page's own accounts grid paginates;
this one does not. At an organization's real scale this is the page that decides whether the
product is usable.

**The row count follows the declaration, so there is no safe size to design for.** The same corpus
serves **2,548** components over the 44 roots it was audited with, and **1,000** over the one root
it honestly holds (`db/declaration-provenance.md`). A single repository — `facebook/rocksdb` —
yields **1,042** on its own. Nothing caps it: the handler and `store.BillOfMaterials` have no
limit, checked when the round 1,000 looked like one.

**Two numbers on one screen are both called components.** The tile said **652**, "depended on by
44 repositories". The grid said **Showing 2,548 of 2,548 components**. Both are correct — 652 is
what something references, 2,548 is everything in the closure including the workflows doing the
referencing — and nothing on the page says so. A reader cannot tell which one is the answer to
"how many components do we have". At the honest declaration the pair is **274 and 1,000**.

**An empty filter result says nothing.** Filtering to a boundary class on a corpus whose edges are
unclassified gives *Showing 0 of 2,548 components* under a bare table header. No message. A reader
concludes they have no cross-account dependencies, which on that corpus is the opposite of true —
there are 4,041 such relationships once classification has run. An absent measurement drawn as a
measurement, in the one place the document is meant to be most careful.

**The caveats are buried behind the roots.** "What this document is, and is not" opens with all 44
repository names inline, six lines of them, before reaching the sentences that matter: that 781 of
3,605 findings carry no result hash, and that advisories are not evaluated against the observed
references. Those are the sentences a reader has to see.

## What is already true

| | |
| --- | --- |
| grid | `MudDataGrid`, all rows, no `Virtualize`, no pager |
| coverage tiles | components / analyzed / references / findings, each with a second line |
| caveat panel | roots inline, then the provenance and advisory statements |
| filters | name, boundary, not-analyzed-only, all client-side |
| exports | three formats, visible as buttons, with the provenance note beneath |
| empty state | none |

The filters are client-side against a document already in memory, which is the right design for a
document that has to be internally consistent — and it is also why the whole document has to
arrive before anything renders.

## The design

- **Virtualize the grid**, keeping the client-side filters. `MudDataGrid` supports it; the
  document stays whole in memory, only the rows drawn are bounded.
- **Name the two counts differently.** The tile counts *dependencies* — things something
  references. The grid lists *everything in the document*, which includes the workflows that do
  the referencing. Two words, and a line saying how they relate.
- **An empty result explains itself**, and distinguishes "nothing matches this filter" from
  "nothing has been classified yet". The second is a coverage statement and belongs in the same
  voice as the rest of the page.
- **Caveats first, roots last.** The roots are checkable detail and belong behind a disclosure;
  the provenance gaps and the advisory caveat are the point.
- **`$`-prefixed components sort first and should not.** See `db/unresolved-references.md` — the
  fix belongs in extraction, but the page should not lead with them meanwhile.

## Phases

1. Virtualized rows.
2. The two counts, named and related.
3. Empty states, including the unclassified case.
4. Caveat ordering, roots behind a disclosure.

## Verification

- The page's DOM stays bounded as the document grows: measured at 2,548 components, where it is
  2.7 MB today.
- Time to interactive on the audited corpus, before and after.
- Every filter combination that returns nothing says why, and the unclassified case says something
  different from the no-match case.
- The two component counts appear with distinct words, and a reader can state what each means
  from the page alone.
- The first sentence of the caveat panel is about evidence, not about roots.

## Open questions

- **Whether filtering should move to the server.** It would bound the payload as well as the DOM,
  and it would break the property that every figure on the page comes from one document. The
  document is the thing being exported; a page that filters a different set than it exports is
  worse than a slow one.
