// Package checkouts removes working copies of repositories the corpus no longer reaches.
//
// Separate from the graph prune because the risk is different in kind: a tombstoned row is
// recoverable by re-analysis, and a deleted directory tree is only recoverable by fetching it
// again. Everything here is therefore derived forward, from repository names the closure ruled
// out, and never inferred backwards from what happens to be on disk.
package checkouts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dirName is the directory `expand` gives a repository's working copy, without the reference
// suffix. Stated here rather than imported because the two must agree and a change to either is
// a change that has to be made deliberately in both.
func dirName(fullName string) (string, bool) {
	owner, repo, ok := strings.Cut(fullName, "/")
	if !ok || owner == "" || repo == "" {
		return "", false
	}
	return owner + "_" + repo, true
}

// Stale lists the working copies belonging to the named repositories.
//
// A repository has one tree per reference — `owner_repo`, `owner_repo@v4` — so a name matches
// exactly or up to the `@`. Matching on the prefix alone would take `stranger/thing-extra`'s tree
// for `stranger/thing`'s, and that tree may be one the closure still reaches.
func Stale(workDir string, repositories []string) ([]string, error) {
	if workDir == "" || len(repositories) == 0 {
		return nil, nil
	}
	bases := make(map[string]bool, len(repositories))
	for _, r := range repositories {
		if base, ok := dirName(strings.ToLower(r)); ok {
			bases[base] = true
		}
	}
	if len(bases) == 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(workDir)
	// Nothing fetched yet is not a fault; it is the state every install starts in.
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading the work directory: %w", err)
	}

	var out []string
	for _, e := range entries {
		// Only a real directory. ABOM creates nothing else here, so a file or a symlink was put
		// there by somebody else and is not ours to delete.
		if !e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		name := strings.ToLower(e.Name())
		base, _, _ := strings.Cut(name, "@")
		if bases[base] {
			out = append(out, filepath.Join(workDir, e.Name()))
		}
	}
	return out, nil
}

// Remove deletes the given working copies and reports how many went.
//
// Every path is checked back against the work directory before anything is deleted: the paths
// come from a caller, a caller is where a mistake gets in, and the one thing this must never do
// is remove somebody's source tree.
func Remove(workDir string, paths []string) (int, error) {
	root, err := filepath.Abs(workDir)
	if err != nil {
		return 0, fmt.Errorf("resolving the work directory: %w", err)
	}
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return 0, fmt.Errorf("resolving %s: %w", p, err)
		}
		if filepath.Dir(abs) != root {
			return 0, fmt.Errorf(
				"refusing to remove %s: it is not a checkout directly under %s", abs, root)
		}
	}
	var removed int
	for _, p := range paths {
		if err := os.RemoveAll(p); err != nil {
			return removed, fmt.Errorf("removing %s: %w", p, err)
		}
		removed++
	}
	return removed, nil
}
