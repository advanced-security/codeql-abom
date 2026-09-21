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
import abom.actions.Steps

from Step step, string subjectPath, string subjectJob, int subjectStep, string value
where
  stepPosition(step, _, subjectPath, subjectJob, subjectStep) and
  value = conditionOf(step)
select subjectPath, subjectJob, subjectStep, "condition" as name, value
