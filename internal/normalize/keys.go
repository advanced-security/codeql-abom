package normalize

import (
	"strconv"
	"strings"
)

// Node keys are the graph's identity scheme. They are deterministic and lowercased so that
// re-ingesting an unchanged repository produces byte-identical keys and upserts collapse,
// which is what makes incremental accumulation across many scans idempotent.

// RepoKey identifies a repository node.
func RepoKey(repoFullName string) string {
	return "repo:" + strings.ToLower(repoFullName)
}

// WorkflowKey identifies a workflow file within a repository.
//
// The form is owner/repo/path, identical to how another repository's `uses:` names it, so a
// reusable workflow and every call site collapse onto one node.
func WorkflowKey(repoFullName, path string) string {
	return strings.ToLower(repoFullName + "/" + strings.TrimPrefix(path, "/"))
}

// CompositeActionKey identifies an action defined inside a repository.
//
// The action file itself is stripped because `uses:` names the directory, not the manifest,
// so `octo-org/lib/greet@v1` and octo-org/lib's own greet/action.yml resolve to one node.
func CompositeActionKey(repoFullName, path string) string {
	p := strings.ToLower(strings.TrimPrefix(path, "/"))
	for _, manifest := range []string{"action.yml", "action.yaml"} {
		if p == manifest {
			p = ""
			break
		}
		if trimmed, ok := strings.CutSuffix(p, "/"+manifest); ok {
			p = trimmed
			break
		}
	}
	if p == "" {
		return strings.ToLower(repoFullName)
	}
	return strings.ToLower(repoFullName) + "/" + p
}

// JobKey identifies a job within a workflow.
func JobKey(repoFullName, path, jobID string) string {
	return "job:" + strings.ToLower(repoFullName) + ":" + strings.ToLower(path) + ":" + jobID
}

// StepKey identifies a step by its position within a container.
//
// Steps are keyed by index rather than by `id:` because `id:` is optional and frequently
// absent, whereas position is always defined.
func StepKey(repoFullName, path, jobID string, stepIndex int) string {
	return "step:" + strings.ToLower(repoFullName) + ":" + strings.ToLower(path) + ":" +
		jobID + ":" + strconv.Itoa(stepIndex)
}

// EdgeKey identifies a dependency edge by the source location that declares it.
//
// A given file position can hold at most one `uses:`, so the location is a natural key and
// avoids hashing every edge during bulk load.
func EdgeKey(repoFullName, path string, startLine, startColumn int) string {
	return strings.ToLower(repoFullName) + ":" + strings.ToLower(path) + ":" +
		strconv.Itoa(startLine) + ":" + strconv.Itoa(startColumn)
}
