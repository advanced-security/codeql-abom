/**
 * @name Action reference not covered by the Actions lockfile
 * @description A `uses:` reference that no `.github/workflows/actions.lock` entry resolves to a
 *              commit runs whatever its ref points at today, with nothing recording what that was.
 * @kind problem
 * @security-severity 5.0
 * @problem.severity warning
 * @precision high
 * @id abom/actions/uncovered-action-ref
 * @tags security
 *       actions
 *       supply-chain
 *       external/cwe/cwe-829
 */

import actions
import abom.actions.LockCoverage

from LockableUses uses, AbomActionsLock lock
where
  // Missing lockfiles are reported by abom/actions/actions-lock-missing-for-workflow.
  lock.getFile().getRelativePath() = ".github/workflows/actions.lock" and
  not any(AbomActionsLock l).covers(uses)
select uses,
  "'" + uses.getCallee() + "@" + uses.getVersion() +
    "' has no entry in the Actions lockfile resolving it to a commit."
