/**
 * Extraction of GitHub Actions dependency edges for the Actions Bill of Materials (ABOM).
 *
 * Raw `uses:` strings are emitted verbatim. All reference parsing — owner/repo/subpath split,
 * sha vs tag vs branch classification, pinning posture — happens in the Go ingester, where it
 * is unit-testable and can evolve without recompiling and re-running an org-wide MRVA scan.
 */

import actions
private import codeql.actions.ast.internal.Ast as AstInternal

/** Gets the `id:` of `step`, or the empty string when the step declares no id. */
private string stepIdOrEmpty(Step step) {
  if exists(step.getId()) then result = step.getId() else result = ""
}

/** Gets the text of a comment trailing `usesNode` on the same line, `#` and padding stripped. */
private string trailingComment(ScalarValue usesNode) {
  exists(AstInternal::Comment c, string file, int line, int usesEndColumn, int commentStartColumn |
    usesNode.getLocation().hasLocationInfo(file, line, _, line, usesEndColumn) and
    c.getLocation().hasLocationInfo(file, line, commentStartColumn, line, _) and
    commentStartColumn > usesEndColumn and
    result = c.getText().regexpReplaceAll("^[\\s#]+", "").regexpReplaceAll("\\s+$", "")
  )
}

/**
 * Gets the version pin comment on `usesNode` (`uses: actions/checkout@<sha> # v4.2.2`), or `""`.
 *
 * The comment is not part of the scalar value, and MRVA artifacts carry no source archive, so
 * it has to be recovered here or it is unrecoverable downstream.
 */
string pinCommentOf(ScalarValue usesNode) {
  if exists(trailingComment(usesNode))
  then result = trailingComment(usesNode)
  else result = ""
}

/**
 * Holds if `usesNode` is a `uses:` reference that constitutes an ABOM dependency edge.
 *
 * `srcStepIndex` is the zero-based position of the step within its container, or `-1` for
 * job-level reusable workflow calls, which have no enclosing step.
 */
predicate abomEdge(
  ScalarValue usesNode, string edgeKind, string srcKind, string srcPath, string srcJobId,
  int srcStepIndex, string srcStepId, string usesRaw
) {
  // A `uses:` step inside a workflow job.
  exists(UsesStep step, LocalJob job |
    step.getContainer() = job and
    usesNode = step.getCalleeNode() and
    edgeKind = "step-uses-action" and
    srcKind = "job" and
    srcPath = step.getLocation().getFile().getRelativePath() and
    srcJobId = job.getId() and
    job.getStep(srcStepIndex) = step and
    srcStepId = stepIdOrEmpty(step) and
    usesRaw = usesNode.getValue()
  )
  or
  // A `uses:` step inside a composite action's `runs:` block.
  exists(UsesStep step, Runs runs |
    step.getContainer() = runs and
    usesNode = step.getCalleeNode() and
    edgeKind = "composite-step-uses-action" and
    srcKind = "composite_action" and
    srcPath = step.getLocation().getFile().getRelativePath() and
    srcJobId = "" and
    runs.getStep(srcStepIndex) = step and
    srcStepId = stepIdOrEmpty(step) and
    usesRaw = usesNode.getValue()
  )
  or
  // A job whose `uses:` calls a reusable workflow.
  exists(ExternalJob job |
    usesNode = job.getCalleeNode() and
    edgeKind = "job-uses-reusable-workflow" and
    srcKind = "workflow" and
    srcPath = job.getLocation().getFile().getRelativePath() and
    srcJobId = job.getId() and
    srcStepIndex = -1 and
    srcStepId = "" and
    usesRaw = usesNode.getValue()
  )
}
