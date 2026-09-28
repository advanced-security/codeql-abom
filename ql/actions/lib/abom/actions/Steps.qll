/**
 * Where a step sits, shared by every ABOM query that reports one.
 *
 * A step lives either in a workflow job or in a composite action's `runs:` block.
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
 * ABOM reports the expression rather than trying to evaluate run-time context.
 */
string conditionOf(Step step) { result = step.getIf().getCondition() }
