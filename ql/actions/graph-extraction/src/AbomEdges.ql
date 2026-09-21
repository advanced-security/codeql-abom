/**
 * @name ABOM dependency edges
 * @description Emits one row per GitHub Actions `uses:` dependency edge, with the raw
 *              reference string and its precise source location, for ingestion into the
 *              Actions Bill of Materials graph.
 * @id abom/edges
 * @tags abom
 *       supply-chain
 *       abom-edges
 */

import actions
import abom.actions.Edges

from
  string edgeKind, string srcKind, string srcPath, string srcJobId, int srcStepIndex,
  string srcStepId, string usesRaw, string pinComment, int startLine, int startColumn,
  int endLine, int endColumn
where
  exists(ScalarValue usesNode |
    abomEdge(usesNode, edgeKind, srcKind, srcPath, srcJobId, srcStepIndex, srcStepId, usesRaw) and
    pinComment = pinCommentOf(usesNode) and
    usesNode.getLocation().hasLocationInfo(_, startLine, startColumn, endLine, endColumn)
  )
  or
  abomNeedsEdge(edgeKind, srcKind, srcPath, srcJobId, srcStepIndex, srcStepId, usesRaw, pinComment,
    startLine, startColumn, endLine, endColumn)
select edgeKind, srcKind, srcPath, srcJobId, srcStepIndex, srcStepId, usesRaw, pinComment,
  startLine, startColumn, endLine, endColumn
