/**
 * @name ABOM resolved callees
 * @description One row per `uses:` call: where it is declared, and the file codeql/actions-all
 *              resolves it to, or the empty string when it resolves to nothing. A closure
 *              database runs this to say which staged callees dataflow can cross into. It is in
 *              no suite: its rows are read by the closure, never ingested by shape.
 * @id abom/actions/resolved-callees
 * @tags abom
 *       supply-chain
 */

import actions
private import codeql.actions.Cfg as Cfg
private import codeql.actions.dataflow.internal.DataFlowPrivate

/** Gets the file `call` resolves to, or the empty string when nothing is staged for it. */
string calleePathOf(DataFlowCall call) {
  if exists(viableCallable(call))
  then result = viableCallable(call).getLocation().getFile().getRelativePath()
  else result = ""
}

from
  DataFlowCall call, Uses uses, string callPath, int startLine, int startColumn,
  string callName, string calleePath
where
  uses = call.(Cfg::Node).getAstNode() and
  // The `uses:` scalar, which is the location an edge is keyed by.
  uses.getCalleeNode().getLocation().hasLocationInfo(_, startLine, startColumn, _, _) and
  callPath = uses.getLocation().getFile().getRelativePath() and
  callName = call.getName() and
  calleePath = calleePathOf(call)
select callPath, startLine, startColumn, callName, calleePath
