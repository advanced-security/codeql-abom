/**
 * @name Workflows without an Actions lockfile
 * @description A repository that runs workflows and keeps no `.github/workflows/actions.lock`
 *              records nothing about which commit any of its `uses:` references resolves to.
 * @kind problem
 * @security-severity 5.0
 * @problem.severity warning
 * @precision high
 * @id abom/actions/actions-lock-missing-for-workflow
 * @tags security
 *       actions
 *       supply-chain
 *       external/cwe/cwe-829
 */

import actions
import abom.actions.LockCoverage

from WorkflowFile reported, int workflows
where
  not hasLockfile() and
  reported = firstWorkflowFile() and
  workflows = count(WorkflowFile f)
select reported,
  "This repository defines " + workflows.toString() +
    " workflow file(s) and no .github/workflows/actions.lock, so no commit is recorded for any " +
    "action it runs."
