// Package closure lays out one owned repository and the callees its workflows reach as a single
// CodeQL source tree, and maps every path in that tree back to the repository that holds the file.
//
// codeql/actions-all resolves a `uses:` by string equality between the reference with its ref
// stripped and a callable's path with `.github/actions/external/` or `.github/workflows/external/`
// removed. Staging a callee there is therefore enough for dataflow to cross into it, at any depth,
// without a second repository checked out anywhere. The `stage-codeql-actions-callees` skill holds
// the rules this layout follows and the layouts it refuses.
package closure

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	native "github.com/github/actions-lockfile/go/pkg/lockfile"
	"gopkg.in/yaml.v3"
)

const (
	// ActionsPrefix is where a composite action is staged.
	ActionsPrefix = ".github/actions/external/"
	// WorkflowsPrefix is where a reusable workflow is staged.
	WorkflowsPrefix = ".github/workflows/external/"
	// AnchorPath is the workflow synthesized for a repository that holds none.
	AnchorPath = ".github/workflows/abom-closure-anchor.yml"

	// layoutVersion moves the digest of every closure when the way one is laid out changes.
	layoutVersion = "abom-closure-layout/1"
	// maxManifestBytes matches GitHub's own action metadata limit.
	maxManifestBytes = native.MaxActionMetaSize
)

// IndexIncludes are appended to the extractor's default filters. Workflows are indexed only at
// the source root by default, so a staged reusable workflow is silently absent without them.
var IndexIncludes = []string{WorkflowsPrefix + "**/*.yml", WorkflowsPrefix + "**/*.yaml"}

// ErrCollision is one staged callee selected at two commits. The namespace has one slot per
// callee, so a database holding both would analyze code one of the consumers does not run.
var ErrCollision = errors.New("one staged callee is selected at two commits")

// anchorWorkflow gives actions-all a repository root. It names no action, so it reaches nothing.
const anchorWorkflow = `# Synthesized by ABOM. codeql/actions-all resolves a uses: only once some workflow gives it a
# repository root, and this repository holds none. Nothing reported in this file is ingested.
name: abom-closure-anchor
on: workflow_dispatch
permissions: {}
jobs:
  anchor:
    runs-on: ubuntu-latest
    steps:
      - run: "true"
`

// Kind is what a staged callee is to actions-all.
type Kind string

const (
	// Composite is an action whose action.yml says `runs.using: composite`.
	Composite Kind = "composite"
	// ReusableWorkflow is a workflow declaring `on: workflow_call`.
	ReusableWorkflow Kind = "reusable-workflow"
)

// Root is the owned repository a closure is rooted at, checked out at the commit its regular
// analysis describes.
type Root struct {
	Repository string
	Commit     string
	Tree       string
}

// Member is one dependency the closure reaches, checked out at the commit the closure selects.
type Member struct {
	// Repository is owner/name as the corpus spells it, which is what a finding is attributed to.
	Repository string
	// Uses is the target as a `uses:` line names it, without its ref. The staged path is built
	// from this, because resolution is byte equality with it.
	Uses   string
	Commit string
	Tree   string
}

// Skipped is a member that is not staged, and why.
type Skipped struct {
	Member
	Reason string
}

// Staged is one callee placed in the closure tree.
type Staged struct {
	Repository string
	Uses       string
	Commit     string
	Kind       Kind
	// Path is the file's path inside its own repository.
	Path string
	// StagedPath is where the file sits in the closure tree.
	StagedPath string

	tree string
}

// Attribution says which repository holds a file in the closure tree, and where. Excluded is a
// file ABOM synthesized, which is never a finding location.
type Attribution struct {
	Repository string
	Path       string
	Excluded   bool
}

// Layout is a closure tree before it is written.
type Layout struct {
	Root     Root
	Staged   []Staged
	Anchored bool
	// Omitted are root paths not copied because they lead outside the checkout.
	Omitted []string

	byPath map[string]Staged
}

// Plan decides what a closure tree holds: the root's own files, every member that is a callable
// at its commit, and an anchor where the root holds no workflow.
//
// A member that is not a callable is skipped and named. Two commits of one staged callee refuse
// the whole closure, because the namespace has room for only one.
func Plan(root Root, members []Member) (Layout, []Skipped, error) {
	layout := Layout{Root: root, byPath: map[string]Staged{}}
	for _, prefix := range []string{ActionsPrefix, WorkflowsPrefix} {
		if _, err := os.Stat(filepath.Join(root.Tree, filepath.FromSlash(prefix))); err == nil {
			return Layout{}, nil, fmt.Errorf("%s already holds %s, and staging would mix its own "+
				"files with the closure's", root.Repository, prefix)
		}
	}
	anchored, err := needsAnchor(root.Tree)
	if err != nil {
		return Layout{}, nil, fmt.Errorf("reading the workflows of %s: %w", root.Repository, err)
	}
	layout.Anchored = anchored

	skipped := []Skipped{}
	slots := map[string]Staged{}
	for _, m := range members {
		s, reason := stage(m)
		if reason != "" {
			skipped = append(skipped, Skipped{Member: m, Reason: reason})
			continue
		}
		slot := string(s.Kind) + ":" + strings.ToLower(s.Uses)
		held, taken := slots[slot]
		switch {
		case !taken:
			slots[slot] = s
		case !strings.EqualFold(held.Commit, s.Commit):
			first, second := held.Commit, s.Commit
			if second < first {
				first, second = second, first
			}
			return Layout{}, nil, fmt.Errorf("%w: %s at %s and at %s", ErrCollision, s.Uses, first, second)
		case held.Uses != s.Uses:
			return Layout{}, nil, fmt.Errorf("%s and %s name one callee, and only one spelling can "+
				"resolve, because resolution is byte equality with the uses: text", held.Uses, s.Uses)
		}
	}
	for _, s := range slots {
		layout.Staged = append(layout.Staged, s)
		layout.byPath[s.StagedPath] = s
	}
	sort.Slice(layout.Staged, func(i, j int) bool {
		return layout.Staged[i].StagedPath < layout.Staged[j].StagedPath
	})
	return layout, skipped, nil
}

// stage decides whether a member is a callable at its commit, reading its checkout rather than
// the graph: the graph records a composite only when it has a `uses:` step, and one made only of
// `run:` steps is the usual injection sink.
func stage(m Member) (Staged, string) {
	owner, repo, within, ok := splitUses(m.Uses)
	if !ok {
		return Staged{}, "the reference names no repository"
	}
	s := Staged{Repository: m.Repository, Uses: m.Uses, Commit: m.Commit, tree: m.Tree}
	if isWorkflowPath(within) {
		body, err := readConfined(m.Tree, within, maxManifestBytes)
		if err != nil {
			return Staged{}, fmt.Sprintf("no workflow read at %s: %v", within, err)
		}
		callable, err := declaresWorkflowCall(body)
		if err != nil {
			return Staged{}, fmt.Sprintf("%s is not readable YAML: %v", within, err)
		}
		if !callable {
			return Staged{}, "a workflow that does not declare on: workflow_call, so it is not a callable"
		}
		s.Kind, s.Path = ReusableWorkflow, within
		s.StagedPath = WorkflowsPrefix + owner + "/" + repo + "/" + within
		return s, ""
	}
	for _, name := range []string{"action.yml", "action.yaml"} {
		file := path.Join(within, name)
		body, err := readConfined(m.Tree, file, maxManifestBytes)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Staged{}, fmt.Sprintf("%s was not read: %v", file, err)
		}
		meta, err := native.ParseActionMeta(string(body))
		if err != nil {
			return Staged{}, fmt.Sprintf("%s is not readable action metadata: %v", file, err)
		}
		if meta.Execution != native.ExecComposite {
			return Staged{}, fmt.Sprintf("a %s action, which is never a callable", meta.Execution)
		}
		s.Kind, s.Path = Composite, file
		s.StagedPath = ActionsPrefix + m.Uses + "/" + name
		return s, ""
	}
	return Staged{}, fmt.Sprintf("no action.yml or action.yaml at %q", within)
}

// splitUses separates owner, repository and the path inside it.
func splitUses(uses string) (owner, repo, within string, ok bool) {
	parts := strings.SplitN(uses, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" || strings.ContainsAny(uses, "@$\\") ||
		strings.Contains(uses, "..") {
		return "", "", "", false
	}
	if len(parts) == 3 {
		within = parts[2]
	}
	return parts[0], parts[1], within, true
}

func isWorkflowPath(p string) bool {
	lower := strings.ToLower(p)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// readConfined reads one file inside a checkout. A link leading out of the checkout is refused,
// so nothing from this machine can enter a database as though a dependency published it.
func readConfined(dir, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	if limit > 0 && info.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes, over the %d GitHub reads", name, info.Size(), limit)
	}
	return io.ReadAll(f)
}

// declaresWorkflowCall reports whether a workflow can be called, in any of the three forms `on:`
// takes.
func declaresWorkflowCall(body []byte) (bool, error) {
	var doc struct {
		On yaml.Node `yaml:"on"`
	}
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return false, err
	}
	const event = "workflow_call"
	switch doc.On.Kind {
	case yaml.ScalarNode:
		return doc.On.Value == event, nil
	case yaml.SequenceNode:
		for _, item := range doc.On.Content {
			if item.Value == event {
				return true, nil
			}
		}
	case yaml.MappingNode:
		for i := 0; i < len(doc.On.Content); i += 2 {
			if doc.On.Content[i].Value == event {
				return true, nil
			}
		}
	}
	return false, nil
}

// needsAnchor reports whether the root holds no workflow actions-all reads as one: a file at the
// top of .github/workflows/ that declares at least one job. A file with no `jobs:` is not a
// Workflow to the extractor, and without one there is no repository root and nothing resolves.
func needsAnchor(dir string) (bool, error) {
	entries, err := os.ReadDir(filepath.Join(dir, ".github", "workflows"))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.IsDir() || !isWorkflowPath(e.Name()) {
			continue
		}
		body, err := readConfined(dir, ".github/workflows/"+e.Name(), maxManifestBytes)
		if err != nil {
			continue
		}
		var doc struct {
			Jobs yaml.Node `yaml:"jobs"`
		}
		if yaml.Unmarshal(body, &doc) == nil && doc.Jobs.Kind == yaml.MappingNode && len(doc.Jobs.Content) > 0 {
			return false, nil
		}
	}
	return true, nil
}

// Digest identifies what went into the closure: the root at its commit, every staged callee at
// its commit, whether an anchor was synthesized, and the layout and index filters that decide
// what actions-all can resolve. Never a hash of the database, which is not byte-reproducible.
func (l Layout) Digest() string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			// Length-prefixed, so no two different inputs can concatenate to the same bytes.
			fmt.Fprintf(h, "%d:%s\x00", len(p), p)
		}
	}
	write(layoutVersion, strings.Join(IndexIncludes, "\n"))
	write(strings.ToLower(l.Root.Repository), strings.ToLower(l.Root.Commit),
		strconv.FormatBool(l.Anchored))
	staged := append([]Staged(nil), l.Staged...)
	sort.Slice(staged, func(i, j int) bool { return staged[i].StagedPath < staged[j].StagedPath })
	for _, s := range staged {
		write(string(s.Kind), strings.ToLower(s.Repository), strings.ToLower(s.Commit), s.StagedPath)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Attribute maps a path in the closure tree to the repository that holds the file and the path it
// has there. A path under an external prefix that nothing staged is refused rather than guessed,
// because attributing it to the root would report a third party's file as the owner's.
func (l Layout) Attribute(p string) (Attribution, error) {
	if l.Anchored && p == AnchorPath {
		return Attribution{Excluded: true}, nil
	}
	decoded := p
	if unescaped, err := url.PathUnescape(p); err == nil {
		decoded = unescaped
	}
	if s, ok := l.byPath[decoded]; ok {
		// The prefix is built from owner and repository names, which need no escaping, so what
		// follows it in the raw path is the path as the repository's own analysis reports it.
		prefix := strings.TrimSuffix(s.StagedPath, s.Path)
		return Attribution{Repository: s.Repository, Path: strings.TrimPrefix(p, prefix)}, nil
	}
	if strings.HasPrefix(decoded, ActionsPrefix) || strings.HasPrefix(decoded, WorkflowsPrefix) {
		return Attribution{}, fmt.Errorf("%s is under a staging prefix and nothing was staged there", p)
	}
	return Attribution{Repository: l.Root.Repository, Path: p}, nil
}

// Write lays the closure tree out at dst, which must not already hold anything: the root's own
// files, each staged manifest, and the anchor when one is needed.
func (l *Layout) Write(dst string) error {
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s already holds files, and a closure tree is written whole", dst)
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	omitted, err := copyTree(l.Root.Tree, dst)
	if err != nil {
		return fmt.Errorf("copying %s: %w", l.Root.Repository, err)
	}
	l.Omitted = omitted
	for _, s := range l.Staged {
		body, err := readConfined(s.tree, s.Path, maxManifestBytes)
		if err != nil {
			return fmt.Errorf("staging %s at %s: %w", s.Uses, s.Commit, err)
		}
		if err := writeNew(dst, s.StagedPath, body); err != nil {
			return err
		}
	}
	if l.Anchored {
		return writeNew(dst, AnchorPath, []byte(anchorWorkflow))
	}
	return nil
}

// copyTree copies a checkout without its repository metadata, through a root confined to it. A
// link leading outside is left out and named, rather than followed.
func copyTree(src, dst string) ([]string, error) {
	root, err := os.OpenRoot(src)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	omitted := []string{}
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		body, err := readConfined(src, name, 0)
		if err != nil {
			omitted = append(omitted, name)
			return nil //nolint:nilerr // A file that cannot be read inside the checkout is named, not fatal.
		}
		return writeNew(dst, name, body)
	})
	return omitted, err
}

func writeNew(dst, name string, body []byte) error {
	target := filepath.Join(dst, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if _, err := f.Write(body); err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", name, err), f.Close())
	}
	return f.Close()
}
