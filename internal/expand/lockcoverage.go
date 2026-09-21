package expand

import (
	"github.com/advanced-security/codeql-abom/internal/lockfile"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// recordLockCoverage notes what the tree's Actions lockfile says about each of its workflows.
//
// Read from the tree at the commit being analyzed, never from the repository's current state: a
// lock describes the revision it was committed with.
func (e *Expander) recordLockCoverage(repository string, tree Tree) error {
	lock, rows, err := lockfile.Survey(tree.Dir)
	if err != nil {
		return err
	}
	return e.Store.RecordLockCoverage(repository, tree.SHA, store.LockCoverageRows(
		string(lock.Coverage), lock.Schema, lock.Digest, lock.Reason, rows))
}
