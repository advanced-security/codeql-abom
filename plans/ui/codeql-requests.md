# A query request says enough to write the query

**Status:** built except the Explorer's tag terms and the catalog's tag chips. Unit-tested; not yet
checked against the real corpus, and no screenshots.

## Why this exists

A request is how an operator asks for a new or improved CodeQL query for GitHub Actions code. It
has to carry what a query needs beyond its test — a name, a remediation, CVE and CWE references,
metadata, examples on both sides, and an exact query id — or the agent invents the help, the
severity and the tags, which are the parts a reader filters findings by. A tag filter is only
worth having if it reads the same in every view.

## Pending

1. **The Explorer applies `tags` and `notTags`.** Inventory, the findings review, `/api/match`,
   policies and filter values apply both; the Explorer leaves them out of `graphTerms` and
   `clientTerms` in `internal/view/spec.go`, so an address carrying one is refused there. The graph
   payload needs one rule→tags map, not tags per facet, and `graph.js` `answersFindingTerms` must
   apply it the way it applies `rules`. Then a tag filter goes beside the rule filter.
   Depends on nothing.
2. **Decide how wide the catalog may be, then add tag chips to it.** At a viewport about 920 px wide the
   table needs **1312 px** in an **890 px** container and scrolls sideways, so Last run and Stage
   start out of view. Measured columns: Query 342, Pack 240, Severity 138 before it was stacked.
   Stacking pack over version and wrapping the id at `/` save about 90 px, which is not enough.
   The fork:
   - fold Crawl suite and Source into the row's detail, where the metadata already is;
   - keep every column and accept the sideways scroll, with Stage pinned to the right;
   - drop the Pack column and carry the pack in the Query cell's second line.
   Tag chips add width, so the decision comes first. Depends on nothing.
3. **The fixture corpus replays an acceptance that no longer exists.** `cmd/abom-corpus`'s
   `layAccept` writes the last run's `.actual` as the `.expected` and records an
   `abom_authoring_review`, and its seeded session ends saying "accepting it is the operator's
   call". So `make corpus`, and every screen read from it, shows the flow the rules now refuse. The corpus should write each
   `.expected` from the fixture's marks before the query, as a session must, and record no review.
   That means re-recording `authoring.json` with `--record-queries`, which runs CodeQL and so waits
   for a moment with no crawl. Depends on nothing.

## Decisions

- **A query id in the catalog means improvement, and only improvement.** Its metadata is shown
  read-only from the catalog; a request may not propose changes to it.
- **An id the catalog does not hold, and `customql.ValidQueryID` accepts, is a new query with that
  id.** An invalid id is refused.
- **An upstream id the operator's pack does not hold is refused at save**, saying why: a session
  writes only inside the operator's pack (`harness.locate`), so accepting it would file a request
  no session can serve.
- **CVE and CWE references become tags**: `external/cve/cve-YYYY-NNNN` and `external/cwe/cwe-NNN`
  (three digits, zero-padded, as the CodeQL style guide spells them).
- **`@security-severity` requires the `security` tag**, as the metadata reference states.
- **No metadata value may contain `*/`**: each is written into a QLDoc comment.

## The constraint that must not be broken

**A linked example is evidence of a commit, not of a branch.** Store the commit it was read at and
the text read there; a test fixture that silently changes when a branch moves is a test nobody ran.

## Verification

- `make verify`.
- On a corpus copy: file a request with two pasted and one linked example; the stored row carries
  the commit and the lines. Start a session; the stub's metadata, the help and one fixture per
  example are on disk.
- `/api/match?tags=external/cwe/cwe-829` counts the same findings as a direct SQL count over
  `abom_rule_tag`, and Inventory and the Explorer agree (`graphterms.spec.mjs`).
- Screenshots of the form and tooltips in both themes (`verify-abom-ui`).
