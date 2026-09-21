package expand

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// recordLockCoverage notes what the tree's Actions lockfile says about each of its workflows.
//
// The repository-level row carries the state of the file itself; one row per workflow carries the
// state of that workflow within it. A workflow the lockfile omits is recorded as uncovered rather
// than left unmentioned, because a gap nothing names cannot be reported.
func (e *Expander) recordLockCoverage(repository string, tree Tree) error {
	lock, err := lockfile.Read(tree.Dir)
	if err != nil {
		return err
	}
	rows := []store.LockCoverage{{
		Coverage: string(lock.Coverage),
		Schema:   lock.Schema,
		Digest:   lock.Digest,
		Reason:   lock.Reason,
	}}
	for _, path := range workflowPaths(tree.Dir) {
		rows = append(rows, store.LockCoverage{
			WorkflowPath: path,
			Coverage:     string(lock.Workflow(path)),
			Schema:       lock.Schema,
			Digest:       lock.Digest,
		})
	}
	return e.Store.RecordLockCoverage(repository, tree.SHA, rows)
}

// workflowPaths lists the repository-relative workflow files a tree holds.
//
// Only the directory GitHub runs workflows from, and not recursively: a `.yml` deeper in the tree
// is not a workflow, and the lockfile keys its entries on this same set of paths.
func workflowPaths(dir string) []string {
	entries, err := os.ReadDir(filepath.Join(dir, ".github", "workflows"))
	if err != nil {
		return nil
	}
	var paths []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".yaml") {
			continue
		}
		paths = append(paths, ".github/workflows/"+name)
	}
	slices.Sort(paths)
	return paths
}
