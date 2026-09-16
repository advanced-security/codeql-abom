# Remediation summarizes two things into their more reassuring halves

**Status:** the advisories unit of Inventory, and it rounds once in the reassuring direction.

## Why this exists

Remediation gets the hard parts right. It shows an advisory's ranges and the references actually
observed side by side and states that it is not deciding whether one falls inside the other. It says
CVSS and EPSS are shown for context and are deliberately not part of the score. It splits direct
from transitive. Two smaller summaries undo some of that, and both fail toward reassurance.

**A count over one population, a table over another.** The coverage strip reads *11 live advisories*
and the table holds **3**, with nothing on screen accounting for the other 8. The exclusion is
correct and is the invariant working: those advisories name actions that appear only in upstream
repositories' own CI, which is nobody's supply chain, so telling an operator to fix them would be
the worse failure. But one of the eight is **critical**, and a reader who notices that has no way to
learn why it is absent. Silence reads as completeness, which is the one thing this project refuses
to let a screen do. *Resolved:* the unit carries an `unreached` count beside its total and states
it under the table, so the two populations are both on screen and the difference is named.

**One boolean over an advisory with two answers.** The `Fix` column reads *available*, from a single
`fixable` flag on the advisory. The expanded row shows the advisory carries two ranges — one *fixed
in 3.28.3*, one **no fix published** — and the observed references include **5 repositories on
`v2`**, which falls in the range that has no fix. The collapsed table tells them a fix exists. The
expanded row tells them the truth, and nothing marks the row as worth expanding.

## Pending

1. **Fix availability is reported per range, not per advisory.** `fixable` is computed as "some
   range has a patch", which is the right input and the wrong summary. The alternatives: report the
   worst case, so an advisory with any unfixed range reads as *partial*; report against the
   references actually observed, which is the most useful and the closest this screen has come to
   adjudicating a range; or drop the column from the collapsed row and make the ranges the only
   place fix availability is stated. The second needs a decision about whether it crosses the line
   the footnote draws.
2. **A row whose expansion changes the answer says so.** The fault above is invisible until somebody
   expands a row, and nothing suggests expanding one. Depends on 1 only if the column stays.

## The constraints that must not be broken

**A summary never reads more reassuring than the thing it summarizes.** Rounding toward *available*,
toward *fewer advisories*, toward *nothing to see* is the failure mode a security interface has to
be built against, because every one of those errors is comfortable and therefore survives review.

**Absence of evidence stays distinguishable from evidence of absence.** Eight advisories absent
because they reach nobody is a different screen from eight absent because nothing has been analyzed.
The number that reaches the strip must not make those look alike.

**The interface still does not adjudicate a range.** Reporting fix availability against observed
references, if item 2 chooses it, is arithmetic over what the advisory published and what was seen —
not a judgement that a given pin is vulnerable. That call belongs to the `actions/*` queries, and the
footnote saying so stays.

## Verification

- On a corpus where a live advisory reaches nothing, a reader can say from the screen alone why it
  is not in the table, and tell that from "not analyzed yet".
- An advisory with one patched and one unpatched range never reads as simply fixed at any level of
  collapse.
- The screenshot pass in `.github/skills/verify-abom-ui/`, which is what found both of these; a DOM
  assertion sees a correct boolean and a correct count in every case here.
