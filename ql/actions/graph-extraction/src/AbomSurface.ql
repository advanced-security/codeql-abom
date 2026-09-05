/**
 * @name ABOM workflow and job surface
 * @description Emits per-job trigger, permission and runner attributes used to weight the
 *              risk of an Actions Bill of Materials graph node.
 * @id abom/surface
 * @tags abom
 *       supply-chain
 *       abom-observation
 */

import actions
private import codeql.actions.ast.internal.Ast as AstInternal
private import codeql.actions.ast.internal.Yaml as InternalYaml

/** Gets the comma-separated permissions declared directly on `job`. */
private string jobPermissions(Job job) {
  result = concat(string p | p = job.getPermissions().getAPermission() | p, "," order by p)
}

/** Gets the comma-separated permissions declared at the workflow level for `job`. */
private string workflowPermissions(Job job) {
  result =
    concat(string p | p = job.getWorkflow().getPermissions().getAPermission() | p, "," order by p)
}

/**
 * Gets the effective permissions of `job` and, in `scope`, where they came from.
 *
 * A job-level `permissions` block replaces the workflow-level one outright rather than
 * merging with it, so the two are never unioned. The guards test the underlying permission
 * sets, not the concatenations, because `concat` yields `""` instead of no result.
 */
private string effectivePermissions(Job job, string scope) {
  exists(job.getPermissions().getAPermission()) and
  result = jobPermissions(job) and
  scope = "job"
  or
  not exists(job.getPermissions().getAPermission()) and
  exists(job.getWorkflow().getPermissions().getAPermission()) and
  result = workflowPermissions(job) and
  scope = "workflow"
  or
  not exists(job.getPermissions().getAPermission()) and
  not exists(job.getWorkflow().getPermissions().getAPermission()) and
  result = "" and
  scope = "default"
}

/** Holds if `job` forwards every caller secret via `secrets: inherit`. */
private predicate hasSecretsInherit(Job job) {
  exists(InternalYaml::YamlScalar s |
    s = job.(AstInternal::JobImpl).getNode().(InternalYaml::YamlMapping).lookup("secrets") and
    s.getValue() = "inherit"
  )
}

/** Gets the comma-separated names of the events that can trigger `job`'s workflow. */
private string triggersOf(Job job) {
  result =
    concat(string e | e = job.getWorkflow().getOn().getAnEvent().getName() | e, "," order by e)
  or
  not exists(job.getWorkflow().getOn().getAnEvent()) and result = ""
}

/** Gets the comma-separated `runs-on` labels of `job`, or `""` when none are declared. */
private string runsOnOf(Job job) {
  result = concat(string l | l = job.getARunsOnLabel() | l, "," order by l)
  or
  not exists(job.getARunsOnLabel()) and result = ""
}

/**
 * Holds if `name` is `value` for the job at `jobId` in `path`.
 *
 * One row per fact rather than one wide row per job: the ingester stores whatever a query says
 * about a subject without knowing what the query measures, so adding an attribute here needs no
 * change anywhere else.
 */
private predicate surfaceFact(Job job, string path, string jobId, string name, string value) {
  path = job.getLocation().getFile().getRelativePath() and
  jobId = job.getId() and
  (
    name = "permissions" and value = effectivePermissions(job, _)
    or
    name = "permissionsScope" and exists(effectivePermissions(job, value))
    or
    name = "triggers" and value = triggersOf(job)
    or
    name = "runsOn" and value = runsOnOf(job)
    or
    name = "secretsInherit" and
    (if hasSecretsInherit(job) then value = "true" else value = "false")
    or
    name = "privileged" and (if job.isPrivileged() then value = "true" else value = "false")
    or
    name = "externallyTriggerable" and
    if job.getWorkflow().getOn().getAnEvent().isExternallyTriggerable()
    then value = "true"
    else value = "false"
  )
}

from Job job, string subjectPath, string subjectJob, string name, string value
where surfaceFact(job, subjectPath, subjectJob, name, value)
select subjectPath, subjectJob, name, value
