package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// QueryVersion is the content identity of a query: the query text plus every library in its
// pack that it transitively imports.
//
// This is what `abom_query_run.query_version` holds and what a catalog tracks a query by. It is
// deliberately narrower than the pack digest beside it in a run key: a pack digest moves when any
// file in the pack moves, so it cannot answer "which queries did this edit actually affect", and
// re-running every query in a pack because one unrelated library changed is the cost worth
// avoiding.
//
// Imports that leave the pack contribute their **name** rather than their content. The pack that
// supplies them is already identified on the run by `pack_version` and `pack_digest`, and hashing
// an installed upstream pack per query would read a few hundred files for every query in a suite.
// The name still contributes, so two queries importing different unresolvable modules never share
// a version -- an import nobody could resolve is recorded as such rather than treated as absent.
//
// Comments are not stripped, so a commented-out import contributes a library the query does not
// really use. That direction is safe: it can only make a version move when it did not have to.
func QueryVersion(queryPath string) (string, error) {
	abs, err := filepath.Abs(queryPath)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", queryPath, err)
	}
	_, _, packDir := PackOf(abs)
	if packDir == "" {
		packDir = filepath.Dir(abs)
	}

	hashed := map[string]string{}
	unresolved := map[string]bool{}
	if err := hashClosure(abs, packDir, libraryRoots(packDir), hashed, unresolved); err != nil {
		return "", err
	}

	// Sorted and keyed by the path relative to the pack, so the version is the same wherever the
	// pack is checked out and on either path separator.
	names := make([]string, 0, len(hashed))
	for name := range hashed {
		names = append(names, name)
	}
	sort.Strings(names)
	missing := make([]string, 0, len(unresolved))
	for name := range unresolved {
		missing = append(missing, name)
	}
	sort.Strings(missing)

	h := sha256.New()
	for _, name := range names {
		// Length-prefixed, so no two names and bodies can concatenate to a third that hashes the
		// same.
		fmt.Fprintf(h, "%d:%s\x00%s\x00", len(name), name, hashed[name])
	}
	for _, name := range missing {
		fmt.Fprintf(h, "unresolved %d:%s\x00", len(name), name)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashClosure records one file and follows what it imports. Already-hashed files are skipped,
// which is also what makes a cycle of imports terminate.
func hashClosure(
	path, packDir string, roots []string, hashed map[string]string, unresolved map[string]bool,
) error {
	// Keyed by the path relative to the pack when the file is in it, and by an absolute-ish name
	// otherwise, so a library shared by two packs is still one entry.
	rel := keyFor(path, packDir, roots)
	if _, seen := hashed[rel]; seen {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	body := sha256.New()
	if _, err := io.Copy(body, f); err != nil {
		f.Close()
		return fmt.Errorf("reading %s: %w", path, err)
	}
	f.Close()
	hashed[rel] = hex.EncodeToString(body.Sum(nil))

	source, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for _, module := range importsIn(string(source)) {
		next, ok := resolveModule(module, filepath.Dir(path), roots)
		if !ok {
			unresolved[module] = true
			continue
		}
		if err := hashClosure(next, packDir, roots, hashed, unresolved); err != nil {
			return err
		}
	}
	return nil
}

// keyFor names a file in the version stably, wherever the workspace is checked out.
func keyFor(path, packDir string, roots []string) string {
	for _, root := range append([]string{packDir}, roots...) {
		if rel, err := filepath.Rel(root, path); err == nil && within(root, path) {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(filepath.Base(path))
}

// libraryRoots are the directories a module name may be resolved against: the query's own pack,
// then every other pack in the same CodeQL workspace.
//
// ABOM keeps its queries and the libraries they import in *different* packs -- the queries in
// `ql/actions/graph-extraction/src`, `abom.actions.Edges` in `ql/actions/lib`. Resolving only
// within the query's pack would record that import as a name and never notice it changing, which
// would make a version that cannot answer the one question it exists for.
func libraryRoots(packDir string) []string {
	workspace := workspaceAbove(packDir)
	if workspace == "" {
		return []string{packDir}
	}
	roots := []string{packDir}
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable directory holds no library this can use
		}
		if d.IsDir() {
			if name := d.Name(); name != "." && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "codeql-pack.yml" {
			return nil
		}
		if dir := filepath.Dir(path); dir != packDir {
			roots = append(roots, dir)
		}
		return nil
	})
	// Sorted so two runs resolve an ambiguous module to the same file.
	sort.Strings(roots[1:])
	return roots
}

// workspaceAbove finds the CodeQL workspace a pack belongs to, or "" when it is standalone.
func workspaceAbove(packDir string) string {
	dir := packDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "codeql-workspace.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// importsIn reports the modules a QL source imports, in the order it names them.
func importsIn(source string) []string {
	var out []string
	for _, line := range strings.Split(source, "\n") {
		field := strings.TrimSpace(line)
		field = strings.TrimPrefix(field, "//")
		field = strings.TrimSpace(field)
		field = strings.TrimPrefix(field, "private ")
		rest, ok := strings.CutPrefix(strings.TrimSpace(field), "import ")
		if !ok {
			continue
		}
		// `import Foo as Bar` and `import Foo::Bar` both name Foo first.
		module := strings.TrimSpace(rest)
		if before, _, found := strings.Cut(module, " "); found {
			module = before
		}
		if module = strings.TrimSpace(module); module != "" {
			out = append(out, module)
		}
	}
	return out
}

// resolveModule finds the library a module name refers to, against the importing file's own
// directory first and then each library root. A dotted name is a path: `abom.actions.Edges` is
// `abom/actions/Edges.qll`.
func resolveModule(module, fromDir string, roots []string) (string, bool) {
	relative := filepath.FromSlash(strings.ReplaceAll(module, ".", "/")) + ".qll"
	for _, root := range append([]string{fromDir}, roots...) {
		candidate := filepath.Join(root, relative)
		// A module name is input, so refuse anything that climbs out of the root it resolved
		// against rather than hashing a file this version has no business naming.
		if !within(root, candidate) {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

// within reports whether a path is inside a root.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
