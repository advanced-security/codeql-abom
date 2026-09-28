/**
 * Extraction of GitHub Actions dependency edges for the Actions Bill of Materials (ABOM).
 *
 * Raw `uses:` strings are emitted verbatim; the Go ingester parses refs.
 */

import actions
private import codeql.actions.ast.internal.Ast as AstInternal
private import codeql.actions.ast.internal.Yaml as InternalYaml
private import abom.actions.Steps

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
 * MRVA artifacts carry no source archive, so this has to be recovered here.
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
  // A `uses:` step, either inside a workflow job or inside a composite action's `runs:` block.
  exists(UsesStep step |
    usesNode = step.getCalleeNode() and
    stepPosition(step, srcKind, srcPath, srcJobId, srcStepIndex) and
    edgeKind = stepEdgeKind(srcKind) and
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

/** Gets the edge kind reported for a `uses:` step in a container of kind `srcKind`. */
private string stepEdgeKind(string srcKind) {
  srcKind = "job" and result = "step-uses-action"
  or
  srcKind = "composite_action" and result = "composite-step-uses-action"
}

/**
 * Gets the `needs:` entry in `job` that names `needed`.
 *
 * The entry carries the location so `needs: [a, b]` produces two distinct edges.
 */
private InternalYaml::YamlScalar neededJobRef(Job job, Job needed) {
  needed = job.getANeededJob() and
  exists(InternalYaml::YamlNode needs |
    needs = job.(AstInternal::JobImpl).getNode().(InternalYaml::YamlMapping).lookup("needs") and
    (result = needs or result = needs.getAChildNode()) and
    result.getValue() = needed.getId()
  )
}

/**
 * Holds if `job` must run after `needed`, per its `needs:` declaration.
 *
 * This is the only job ordering GitHub Actions guarantees.
 */
predicate abomNeedsEdge(
  string edgeKind, string srcKind, string srcPath, string srcJobId, int srcStepIndex,
  string srcStepId, string usesRaw, string pinComment, int startLine, int startColumn,
  int endLine, int endColumn
) {
  exists(Job job, Job needed, InternalYaml::YamlScalar ref |
    ref = neededJobRef(job, needed) and
    edgeKind = "job-needs-job" and
    srcKind = "workflow" and
    srcPath = job.getLocation().getFile().getRelativePath() and
    srcJobId = job.getId() and
    srcStepIndex = -1 and
    srcStepId = "" and
    usesRaw = needed.getId() and
    pinComment = "" and
    ref.getLocation().hasLocationInfo(_, startLine, startColumn, endLine, endColumn)
  )
}
