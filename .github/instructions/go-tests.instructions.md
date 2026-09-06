---
description: "Use when writing or fixing Go tests for codeql-abom. Covers fail-first, structural invariants over whole results, test naming, surfacing worker-pool failures, and the invariant guards that must not be weakened."
applyTo: "**/*_test.go"
---

# Go tests in codeql-abom

## Fail first

Run a new test and confirm it fails for the reason you expect. One here asserted a string the
component never renders, so it was green against deliberately broken code.

## Never hand-write CodeQL output

BQRS and SARIF fixtures come from the CLI. Use the `regenerate-abom-fixtures` skill, which also
covers hand-built graph fixtures drifting from real ingestion.

## Assert over the whole result, not just your case

Fixtures cover the case you thought of. Splitting nodes per reference passed every unit test and
left 700 edges starting at a node that no longer existed.

```go
// An endpoint naming a node the graph does not contain is a break nothing can draw.
for _, e := range graph.Edges {
    if !keys[e.Source] { t.Errorf("edge %s -> %s starts outside the graph", e.Source, e.Target) }
}
```

Cross-check derived sets against the same set computed independently in SQL. A plausible-looking
ratio does not catch a bad join.

## Naming and comments

Name tests after the behavior they protect, not the function they call. State *why* it matters,
so a future reader can tell a regression from an intentional change.

```go
// A retracted advisory must not be reported: its own publisher no longer stands behind it.
func TestWithdrawnAdvisoriesAreNotReported(t *testing.T) {
```

## Surface failures

Worker-pool jobs fail silently, hiding a consume-phase error behind an unrelated assertion. Wire
`OnEvent` in test harnesses and log the failure.

## Do not weaken the invariant guards

`TestSchemaHasNoVerdictColumns`, `TestGraphTablesAreAppendOnly`,
`TestCommonalityStillOutranksYieldPreference`, `TestSeedRepositoryIsNotDemotedToUpstream`,
`TestRemediationScoreIsSeverityTimesReachOnly`,
`TestUpstreamRepositoriesStillContributeTransitiveEdges`.

If one blocks you, its stated reason may be narrower than its assertion — narrow the test to that
reason and say so. Never delete it silently.
