/**
 * @name ABOM step execution conditions
 * @description Emits the `if:` condition guarding each conditional step, verbatim, so that a
 *              step something reaches can be told from one it reaches unconditionally.
 * @id abom/step-conditions
 * @tags abom
 *       supply-chain
 *       abom-observation
 */

import actions

/**
 * Holds if `step` is the `index`th step of a job or composite action in `path`, with `jobId`
 * naming the job or empty for a composite action's `runs:` block.
 */
private predicate stepPosition(Step step, string path, string jobId, int index) {
  path = step.getLocation().getFile().getRelativePath() and
  (
    exists(LocalJob job | job.getStep(index) = step and jobId = job.getId())
    or
    exists(Runs runs | runs.getStep(index) = step and jobId = "")
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
private string conditionOf(Step step) { result = step.getIf().getCondition() }

from Step step, string subjectPath, string subjectJob, int subjectStep, string value
where
  stepPosition(step, subjectPath, subjectJob, subjectStep) and
  value = conditionOf(step)
select subjectPath, subjectJob, subjectStep, "condition" as name, value
