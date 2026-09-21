/**
 * Where a step sits, shared by every ABOM query that reports one.
 *
 * A step lives either in a workflow job or in a composite action's `runs:` block, and nothing
 * else: the two containers are kept in one place so a query reporting a step and a query
 * reporting an edge out of that step cannot disagree about its index.
 */

import actions

/** Gets the `id:` of `step`, or the empty string when the step declares no id. */
string stepIdOrEmpty(Step step) {
  if exists(step.getId()) then result = step.getId() else result = ""
}

/**
 * Holds if `step` is the `index`th step of the container at `path`, with `jobId` naming the
 * job or empty for a composite action, and `kind` distinguishing the two.
 */
predicate stepPosition(Step step, string kind, string path, string jobId, int index) {
  path = step.getLocation().getFile().getRelativePath() and
  (
    exists(LocalJob job |
      job.getStep(index) = step and
      jobId = job.getId() and
      kind = "job"
    )
    or
    exists(Runs runs |
      runs.getStep(index) = step and
      jobId = "" and
      kind = "composite_action"
    )
  )
}

/**
 * Gets the `if:` condition on `step`, verbatim.
 *
 * Verbatim because ABOM does not decide whether a condition holds: `github.ref == 'refs/heads/main'`
 * is a fact about the workflow, and whether it is true on any given run is not something static
 * analysis can settle. Reporting the expression lets a reader judge; evaluating it would be this
 * tool inventing a verdict.
 */
string conditionOf(Step step) { result = step.getIf().getCondition() }
