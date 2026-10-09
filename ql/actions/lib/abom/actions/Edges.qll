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

/** Gets the mapping that writes `usesNode` at this step or job. */
private InternalYaml::YamlMapping useHost(
  ScalarValue usesNode, string srcPath, string srcJobId, int srcStepIndex
) {
  exists(UsesStep step |
    stepPosition(step, _, srcPath, srcJobId, srcStepIndex) and
    usesNode = step.getCalleeNode() and
    result = step.(AstInternal::UsesStepImpl).getNode()
  )
  or
  exists(ExternalJob job |
    usesNode = job.getCalleeNode() and
    srcPath = job.getLocation().getFile().getRelativePath() and
    srcJobId = job.getId() and
    srcStepIndex = -1 and
    result = job.(AstInternal::ExternalJobImpl).getNode()
  )
}

/**
 * Gets an alias in `host` that writes `usesScalar`.
 *
 * The anchored scalar's location is shared by every alias of it. An edge keyed on
 * that location records one use, whichever was written last.
 */
private InternalYaml::YamlNode aliasWritten(
  InternalYaml::YamlMapping host, InternalYaml::YamlScalar usesScalar
) {
  exists(InternalYaml::YamlAliasNode alias |
    host.getAChildNode() = alias and
    alias.eval() = usesScalar and
    result = alias
  )
  or
  exists(
    InternalYaml::YamlAliasNode alias, InternalYaml::YamlMapping merged, InternalYaml::YamlMergeKey key
  |
    host.maps(key, merged) and
    host.getAChildNode() = alias and
    alias.eval() = merged and
    not host.getAChildNode() = usesScalar and
    merged.getAChildNode*() = usesScalar and
    result = alias
  )
}

/**
 * Gets the node that writes this use, which is the alias when the `uses:` is not
 * written at the anchored scalar.
 */
private InternalYaml::YamlNode writtenUse(
  ScalarValue usesNode, string srcPath, string srcJobId, int srcStepIndex
) {
  exists(InternalYaml::YamlMapping host, InternalYaml::YamlScalar usesScalar |
    host = useHost(usesNode, srcPath, srcJobId, srcStepIndex) and
    usesScalar = usesNode.(AstInternal::ScalarValueImpl).getNode() and
    (
      result = aliasWritten(host, usesScalar)
      or
      not exists(aliasWritten(host, usesScalar)) and result = usesScalar
    )
  )
}

/** Gets the `steps:` sequence of a job or a composite action's `runs:` block. */
private predicate stepsOf(
  InternalYaml::YamlSequence steps, string srcKind, string srcPath, string srcJobId
) {
  exists(LocalJob job |
    job.(AstInternal::LocalJobImpl).getNode().lookup("steps") = steps and
    srcKind = "job" and
    srcPath = job.getLocation().getFile().getRelativePath() and
    srcJobId = job.getId()
  )
  or
  exists(Runs runs |
    runs.(AstInternal::RunsImpl).getNode().lookup("steps") = steps and
    srcKind = "composite_action" and
    srcPath = runs.getLocation().getFile().getRelativePath() and
    srcJobId = ""
  )
}

/**
 * A step that is itself an alias of a `uses:` step.
 *
 * The actions AST keeps only the anchored mapping, so the alias is not a step and
 * `abomEdge` never emits it. GitHub expands it, and its location is the alias.
 */
private predicate aliasedStepUse(
  ScalarValue usesNode, InternalYaml::YamlNode written, string edgeKind, string srcKind,
  string srcPath, string srcJobId, int srcStepIndex, string srcStepId, string usesRaw
) {
  exists(UsesStep anchored, InternalYaml::YamlAliasNode alias, InternalYaml::YamlSequence steps |
    stepsOf(steps, srcKind, srcPath, srcJobId) and
    steps.getElementNode(srcStepIndex) = alias and
    alias.eval() = anchored.(AstInternal::UsesStepImpl).getNode() and
    usesNode = anchored.getCalleeNode() and
    written = alias and
    edgeKind = stepEdgeKind(srcKind) and
    srcStepId = stepIdOrEmpty(anchored) and
    usesRaw = usesNode.getValue()
  )
}

/**
 * Holds if a `uses:` dependency is written at `startLine`/`startColumn`.
 *
 * The location is the alias's when the use is an alias, so each use is its own edge.
 */
predicate abomUseEdge(
  string edgeKind, string srcKind, string srcPath, string srcJobId, int srcStepIndex,
  string srcStepId, string usesRaw, string pinComment, int startLine, int startColumn,
  int endLine, int endColumn
) {
  exists(ScalarValue usesNode, InternalYaml::YamlNode written |
    (
      abomEdge(usesNode, edgeKind, srcKind, srcPath, srcJobId, srcStepIndex, srcStepId, usesRaw) and
      written = writtenUse(usesNode, srcPath, srcJobId, srcStepIndex)
      or
      aliasedStepUse(usesNode, written, edgeKind, srcKind, srcPath, srcJobId, srcStepIndex, srcStepId,
        usesRaw)
    ) and
    pinComment = pinCommentOf(usesNode) and
    written.getLocation().hasLocationInfo(_, startLine, startColumn, endLine, endColumn)
  )
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
