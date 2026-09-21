# A query's effect across the organization is known before anybody turns it on

**Status:** Nothing here exists. A policy can be simulated before it is adopted; a query cannot.

## Why this exists

ABOM already refuses to let a rule bite before somebody has seen what it would do: a policy is
drafted, **simulated against stored state**, and adopted only if what it would have blocked is
acceptable. `abom_policy_simulation` records the counts that decision was made on.

A query has no equivalent, and it is the more consequential of the two. Adopting a policy changes
what ABOM refuses. Publishing a query pack that an organization's Default Setup picks up changes
what **every developer in the organization sees in their Security tab** — and the person who wrote
the query is usually not the person who will be asked about the four hundred alerts it raised.

So the question a query author needs answered before publishing is precise: *given this query, which
repositories would raise how many alerts, and which of those are already there?* ABOM is in an
unusual position to answer it, because it already holds analyzed databases for the organization and
already knows which repositories have code scanning at all.

**The answer has to come from `regular` mode, and only from `regular` mode.** Default Setup builds
one repository's own checkout. It stages nothing, so it never resolves a `uses:` across a repository
boundary and never sees anything
[`ql/closure-database.md`](../ql/closure-database.md) reveals. A prediction sourced from an extended
database would confidently name alerts that will never appear. Preserve the implemented
[analysis-mode isolation](../../docs/architecture.md#analysis-modes).

## Pending

1. **Run the candidate query across a resolved scope and count what it would report**, per
   repository and per rule. The mechanism is [`api/on-demand-analysis.md`](on-demand-analysis.md);
   what this adds is that the results are held as a **prediction** rather than merged as findings,
   because a query nobody has adopted has not found anything yet. Depends on the on-demand run and
   on [`ql/query-authoring.md`](../ql/query-authoring.md).
2. **Subtract what is already alerted.** A rule that fires 400 times where 380 alerts already exist
   adds 20. The count that matters to the person who will be asked about it is the **delta**, not
   the total. Depends on 1.
3. **State the coverage, and state it first.** A prediction over 31 of 40 repositories is a
   prediction about 31 repositories. The nine are named. Coverage comes from
   [`api/scope-to-databases.md`](scope-to-databases.md), which already reports its gap.
   Depends on 1.
4. **State the enablement condition.** A repository with no Actions analysis enabled raises no alert
   however loudly the query fires, and GitHub's repository language statistics **do not report
   `actions`** — which is the one gap `gh-ghas-audit` is structurally blind to and
   [`api/ghas-enablement.md`](ghas-enablement.md) exists to close. Until that lands, the prediction
   carries the condition rather than the answer: *this many alerts, in the repositories where
   Actions analysis is running*. Depends on 3.
5. **State the merge.** A configuration file applied through the `github-codeql-config-file`
   property is **merged** with the configuration Default Setup generates, so the alerts that appear
   are the union of the default suite and the added pack. A prediction that ignores the merge
   predicts the wrong set — it has to be a delta over what the repository already runs, not a
   standalone count. Depends on 4.
6. **Keep the prediction, so it can be checked afterwards.** Written down before publication, and
   compared with what actually appeared once the pack is live. A prediction nobody revisits is a
   number that was never tested. Depends on 5 and on
   [`ql/pack-promotion.md`](../ql/pack-promotion.md).

## The inference boundary, stated rather than implied

**Exact for the commit analyzed. An inference beyond it.** ABOM analyzes a pinned commit; Default
Setup scans a default branch and a pull request head. Where those agree, the prediction is a
measurement; where they do not, it is an extrapolation, and the size of the gap is how stale the
analysis is.

This is the same refusal [`api/dependency-graph-crosscheck.md`](dependency-graph-crosscheck.md)
already makes for a different reason — *an SBOM describes a default branch and we analyze a pinned
commit* — and it is not softened here because the prediction is more useful if it is believed. **A
prediction that does not name its own staleness is the failure mode.**

## The constraints that must not be broken

**A simulation creates nothing.** No alert, no finding, no submission, no write to any repository.
The output is a count and a list, held against the candidate query, and it disappears if the query
is discarded.

**A prediction is sourced from `regular` mode.** Enforced, not documented: a simulation over
extended rows is refused rather than labelled.

**A predicted alert is not a finding.** It is what *would* be reported by a query nobody has
adopted. Merging predictions into `abom_finding` would make the counts on every surface include
things nobody chose to look for.

**Say what was not predicted.** Repositories with no database, with no Actions analysis, or with an
analysis too old to trust are named individually. A confident number over an unnamed subset is worse
than no number, because it will be quoted.

**A security verdict still comes only from a rule id**, and a simulation adds no verdict of its own.
It says what a rule would say, not whether the rule is right.

## Verification

- A query with a known result set on the crawled organization predicts that set exactly, compared
  against a real run of the same query through the ordinary lane.
- The prediction and the actual alerts are compared once for real, on `codeql-abom-test1`, with the
  prediction written down **before** the pack is published. A disagreement is the finding, and it is
  recorded rather than reconciled.
- Coverage plus predicted repositories equals the repositories the scope named, always.
- A simulation asked for `mode=extended` is refused, with its reason.
- The delta is checked against an alert set that already exists: re-predicting an adopted query
  returns zero new alerts.
