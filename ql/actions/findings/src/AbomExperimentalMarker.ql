/**
 * @name ABOM experimental coverage marker
 * @description Emits one recommendation for every job and every composite action, so that a
 *              repository with no upstream findings still produces findings ABOM can attribute,
 *              ingest and exercise its pipeline against.
 * @kind problem
 * @problem.severity recommendation
 * @precision low
 * @id abom/experimental/coverage-marker
 * @tags abom
 *       abom-experimental
 */

import actions

/**
 * Holds if `subject` is a thing worth marking, described by `message`.
 *
 * Jobs rather than workflows, because marking every job already covers every workflow: a valid
 * workflow declares at least one, and a file that declares none is not extracted as a `Workflow`
 * at all — confirmed by PrintAST, where a `jobs:`-less file yields `OnImpl` and `EventImpl` and no
 * `WorkflowImpl`. Selecting such a workflow is not a stricter reading, it is unreachable code.
 *
 * Composite actions are the case a job-only query misses entirely: a repository that *is* an
 * action has no jobs, so it would produce nothing and be indistinguishable from one nobody
 * analyzed.
 */
private predicate marked(AstNode subject, string message) {
  exists(Job job |
    subject = job and
    message = "ABOM experimental marker for job '" + job.getId() + "'."
  )
  or
  exists(CompositeAction action |
    subject = action and
    message = "ABOM experimental marker for a composite action."
  )
}

from AstNode subject, string message
where marked(subject, message)
select subject, message
