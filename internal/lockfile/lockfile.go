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
	"path/filepath"
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
	contents, err := os.ReadFile(filepath.Join(root, native.Path))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Lock{Coverage: NotOnboarded}, nil
	case err != nil:
		return Lock{}, fmt.Errorf("reading %s: %w", native.Path, err)
	}

	sum := sha256.Sum256(contents)
	lock := Lock{Digest: hex.EncodeToString(sum[:]), Schema: schemaOf(contents)}

	file, err := native.ParseWithPolicy(contents, native.DefaultPolicy(), native.Path)
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

// Selected reports the commit this lockfile selects for a `uses:` string in a workflow.
//
// The answer is the dependency entry's digest, never its `ref` field. The two disagree whenever an
// entry is keyed by commit and narrowed to the tag it was resolved from, which is the shape the
// CLI writes for an inline SHA, and the entry still names exactly one commit.
func (l Lock) Selected(workflowPath, uses string) (string, bool) {
	if !l.parsed {
		return "", false
	}
	// Anything the lockfile grammar does not describe -- expressions, `docker://`, local and
	// same-repository paths, reusable workflow files -- is not a gap in coverage.
	ref := native.ParseActionRef(uses)
	if ref == nil {
		return "", false
	}
	pins, ok := l.file.LookupWorkflow(workflowPath)
	if !ok {
		return "", false
	}
	want := native.Pin{NWO: ref.NWO(), Owner: ref.Owner, Repo: ref.Repo, Ref: ref.Ref}.Canonical()
	for _, key := range pins {
		pin, ok := native.ParsePin(key)
		if !ok {
			continue
		}
		if pin.Canonical() != want {
			continue
		}
		commit := l.file.Dependencies[key].Commit
		if digest, ok := digestOf(commit); ok {
			return digest, true
		}
	}
	return "", false
}

// digestOf strips the algorithm prefix the lockfile records commits with. An entry whose commit
// is not in that form names nothing runnable, so it selects nothing.
func digestOf(commit string) (string, bool) {
	name, value, ok := strings.Cut(commit, "-")
	if !ok || value == "" {
		return "", false
	}
	switch name {
	case "sha1", "sha256":
		return value, true
	}
	return "", false
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
