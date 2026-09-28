/**
 * Pins how `codeql/actions-all` resolves a `uses:` against staged callees, which a closure
 * database depends on: a staged composite action resolves, a staged JavaScript action never does.
 */

import actions
private import codeql.actions.dataflow.internal.DataFlowPrivate

from DataFlowCall call, string callee
where
  if exists(viableCallable(call))
  then callee = viableCallable(call).getLocation().getFile().getRelativePath()
  else callee = "unresolved"
select call.getName(), callee
