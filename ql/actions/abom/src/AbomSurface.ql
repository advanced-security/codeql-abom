/**
 * @name ABOM workflow and job surface
 * @description Emits per-job trigger, permission and runner attributes used to weight the
 *              risk of an Actions Bill of Materials graph node.
 * @id abom/surface
 * @tags abom
 *       supply-chain
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

from
  Job job, string srcPath, string jobId, string permissions, string permissionsScope,
  string triggers, string runsOn, boolean secretsInherit, boolean isPrivileged,
  boolean isExternallyTriggerable
where
  srcPath = job.getLocation().getFile().getRelativePath() and
  jobId = job.getId() and
  permissions = effectivePermissions(job, permissionsScope) and
  triggers = triggersOf(job) and
  runsOn = runsOnOf(job) and
  (if hasSecretsInherit(job) then secretsInherit = true else secretsInherit = false) and
  (if job.isPrivileged() then isPrivileged = true else isPrivileged = false) and
  if job.getWorkflow().getOn().getAnEvent().isExternallyTriggerable()
  then isExternallyTriggerable = true
  else isExternallyTriggerable = false
select srcPath, jobId, permissions, permissionsScope, triggers, runsOn, secretsInherit,
  isPrivileged, isExternallyTriggerable
