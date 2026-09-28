// Package lockfile reads `.github/workflows/actions.lock` and reports what it says about a
// repository's action references.
//
// Reading only. ABOM never writes a lock, never relocks and never accepts a moved ref: GitHub's
// tooling owns generation and verification. Parsing is delegated to the official parser rather
// than reimplemented, so the format has one source of truth.
package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	native "github.com/github/actions-lockfile/go/pkg/lockfile"
)

// Coverage is what a lockfile says about a repository or one of its workflows.
//
// The states are distinguished because they permit different interpretations downstream, and
// collapsing any two of them loses a fact: an unsupported lock is not an absent one, and an
// onboarded workflow with no dependencies is not an unonboarded workflow.
type Coverage string

const (
	// NotOnboarded is no lockfile, or a lockfile that does not list this workflow.
	NotOnboarded Coverage = "not_onboarded"
	// Empty is a workflow the lockfile lists with no dependencies: a recorded empty set.
	Empty Coverage = "empty"
	// Covered is a workflow the lockfile lists with dependencies this consumer can read.
	Covered Coverage = "covered"
	// Unsupported is a named failure: present, and unreadable by this consumer.
	Unsupported Coverage = "unsupported"
)

// Lock is one repository's lockfile, or the reason there is nothing to read.
type Lock struct {
	Coverage Coverage
	// Reason is why the lock is Unsupported, and empty otherwise.
	Reason string
	// Schema is the version as written, retained even when it is the reason for the failure.
	Schema string
	// Digest is sha256 over the original bytes, taken before parsing so a file that cannot be
	// read can still be told apart from a different file that also cannot be read.
	Digest string

	file   native.File
	parsed bool
}

// Read reports what the lockfile under root says.
//
// A repository that has not adopted the feature is NotOnboarded, not an error, because most of
// the corpus has not adopted it. An error is reserved for a tree that cannot be read at all.
func Read(root string) (Lock, error) {
	contents, err := readUnderRoot(root, native.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Lock{Coverage: NotOnboarded}, nil
	case err != nil:
		return Lock{}, fmt.Errorf("reading %s: %w", native.Path, err)
	}

	sum := sha256.Sum256(contents)
	lock := Lock{Digest: hex.EncodeToString(sum[:]), Schema: schemaOf(contents)}

	// No workflow paths: the parser validates only the entries the workflows it is handed list,
	// and every entry here can become a selection. A lock with one malformed entry is therefore
	// unsupported as a whole, which is a named failure rather than a partial answer.
	file, err := native.ParseWithPolicy(contents, native.DefaultPolicy())
	if err != nil {
		lock.Coverage = Unsupported
		lock.Reason = reasonFor(err)
		return lock, nil
	}
	lock.file, lock.parsed = file, true
	lock.Schema = file.Version
	lock.Coverage = Covered
	return lock, nil
}

// readUnderRoot reads one path inside the checkout, refusing anything that leaves it.
//
// The trees this reads are downloaded from repositories ABOM does not control, so a symlink in
// one is an instruction from a stranger: joining the path and reading it would let a repository
// nominate any file the process can open as its own lockfile. os.Root resolves every component
// beneath the checkout, so an escape is an error rather than someone else's file.
func readUnderRoot(root, name string) ([]byte, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	return dir.ReadFile(name)
}

// readDirUnderRoot enumerates one directory inside the checkout, on the same terms.
func readDirUnderRoot(root, name string) ([]fs.DirEntry, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	handle, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	return handle.ReadDir(-1)
}

// Workflow reports coverage for one repository-relative workflow path.
func (l Lock) Workflow(path string) Coverage {
	if !l.parsed {
		return l.Coverage
	}
	pins, ok := l.file.LookupWorkflow(path)
	switch {
	case !ok:
		return NotOnboarded
	case len(pins) == 0:
		return Empty
	default:
		return Covered
	}
}

// WorkflowCoverage is one workflow's state within a lockfile. An empty Path is the state of the
// file itself.
type WorkflowCoverage struct {
	Path     string
	Coverage Coverage
}

// Survey reads the lock under root and reports it alongside the coverage of every workflow the
// tree holds.
//
// One place rather than one per caller: a workflow the lockfile omits has to be reported as
// uncovered, and a caller that enumerated differently would disagree about what the corpus says.
func Survey(root string) (Lock, []WorkflowCoverage, error) {
	lock, err := Read(root)
	if err != nil {
		return Lock{}, nil, err
	}
	rows := []WorkflowCoverage{{Coverage: lock.Coverage}}
	for _, path := range workflowPaths(root) {
		rows = append(rows, WorkflowCoverage{Path: path, Coverage: lock.Workflow(path)})
	}
	return lock, rows, nil
}

// workflowPaths lists the repository-relative workflow files a tree holds.
//
// Only the directory GitHub runs workflows from, and not recursively: a `.yml` deeper in the tree
// is not a workflow, and the lockfile keys its entries on this same set of paths.
func workflowPaths(root string) []string {
	entries, err := readDirUnderRoot(root, ".github/workflows")
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

// schemaOf reads the version line without parsing, so an unsupported file still reports the
// version that made it unsupported.
func schemaOf(contents []byte) string {
	for line := range strings.SplitSeq(string(contents), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "version:")
		if !ok {
			continue
		}
		return strings.Trim(strings.TrimSpace(rest), `'"`)
	}
	return ""
}

// reasonFor renders a parse failure, preferring the parser's own position when it has one.
func reasonFor(err error) string {
	var parse *native.ParseError
	if errors.As(err, &parse) {
		return parse.Error()
	}
	return err.Error()
}
