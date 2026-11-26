// Package querypack reads everything that makes one of your queries run, and bundles it.
//
// A query authored in ABOM is useful elsewhere only as a pack, so what a reader is shown and what
// they take away are the same set of files: the pack's manifest, lock and suite, the query, every
// library of its own pack it imports, its help, the pack's data extensions, and its tests. Read from
// the files rather than from `codeql pack create`, which takes the machine-wide lock and compiles a
// plan that differs on every run; its `inputsDigest` is stable, its bytes are not.
package querypack

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// The roles a component plays, which is how a reader finds one.
const (
	RoleManifest     = "manifest"
	RoleLock         = "lock"
	RoleSuite        = "suite"
	RoleQuery        = "query"
	RoleLibrary      = "library"
	RoleHelp         = "help"
	RoleExtension    = "extension"
	RoleTestManifest = "test-manifest"
	RoleTestLock     = "test-lock"
	RolePostprocess  = "postprocess"
	RoleQLRef        = "qlref"
	RoleFixture      = "fixture"
	RoleExpected     = "expected"
	RoleActual       = "actual"
)

// Component is one file of a query, by its path in the pack directory.
type Component struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Bundled is false only for a test run's output, which describes a run rather than the query.
	Bundled bool `json:"bundled"`
}

// Missing is a component that should exist and does not, or one refused, with why.
type Missing struct {
	Role string `json:"role"`
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Dependency is a pack the lock pins, at the version it resolved.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Manifest is everything that makes one query run.
type Manifest struct {
	Query       string `json:"query"`
	Version     string `json:"version"`
	Pack        string `json:"pack"`
	PackVersion string `json:"packVersion"`
	// Dependencies are what the lock resolved; empty when there is no lock to say.
	Dependencies []Dependency `json:"dependencies"`
	// Outside are the modules the query imports that its pack does not hold, which a dependency
	// supplies.
	Outside []string `json:"outside"`
	// Development is whether the development suite, which selects the whole pack, is there; Deployed
	// whether the deployed suite names this query.
	Development bool        `json:"development"`
	Deployed    bool        `json:"deployed"`
	Components  []Component `json:"components"`
	Missing     []Missing   `json:"missing"`
	// BundleSHA256 is the digest of the archive Write compresses from these components, checked
	// against a download with `gunzip -c bundle.tar.gz | sha256sum`.
	BundleSHA256 string `json:"bundleSha256"`
}

// ExportName is what an export of the bundle is named by: the query, safe in a path, and the
// bundle's digest, so two bundles of one query are told apart.
func (m Manifest) ExportName() string {
	return strings.NewReplacer("/", "-", "@", "-").Replace(m.Query) + "-" + m.BundleSHA256[:12]
}

type reader struct {
	root string
	fsys *os.Root
	m    *Manifest
	seen map[string]bool
}

// Read collects the components of q, a query in the pack at root.
func Read(root string, q authoring.Query) (Manifest, error) {
	m := Manifest{Query: q.ID, Version: q.Version, Dependencies: []Dependency{}, Outside: []string{},
		Components: []Component{}, Missing: []Missing{}}
	fsys, err := os.OpenRoot(root)
	if err != nil {
		return m, err
	}
	defer func() { _ = fsys.Close() }()
	r := &reader{root: root, fsys: fsys, m: &m, seen: map[string]bool{}}

	extensions, err := r.packManifest()
	if err != nil {
		return m, err
	}
	if err := r.lock(); err != nil {
		return m, err
	}
	m.Development, err = r.add(RoleSuite, customql.SuitePath, "the development suite, which selects the whole pack, is not there")
	if err != nil {
		return m, err
	}
	if m.Deployed, err = r.deployed(customql.DeploySuitePath, q.ID); err != nil {
		return m, err
	}

	if ok, err := r.add(RoleQuery, q.Rel, "the query file is not there"); err != nil || !ok {
		return m, err
	}
	imports, err := runner.QueryImports(q.Path)
	if err != nil {
		return m, err
	}
	m.Outside = append(m.Outside, imports.Outside...)
	for _, library := range imports.Libraries {
		if _, err := r.add(RoleLibrary, r.rel(library), "it is imported and not there"); err != nil {
			return m, err
		}
	}
	base := strings.TrimSuffix(q.Rel, ".ql")
	if ok, err := r.addFirst(RoleHelp, base+".md", base+".qhelp"); err != nil {
		return m, err
	} else if !ok {
		r.missing(RoleHelp, base+".md", "no help beside the query")
	}
	for _, pattern := range extensions {
		if err := r.extensions(pattern); err != nil {
			return m, err
		}
	}
	if err := r.tests(q); err != nil {
		return m, err
	}

	slices.SortFunc(m.Components, func(a, b Component) int { return strings.Compare(a.Path, b.Path) })
	files, err := Files(root, m)
	if err != nil {
		return m, err
	}
	h := sha256.New()
	if err := archive(h, files); err != nil {
		return m, err
	}
	m.BundleSHA256 = hex.EncodeToString(h.Sum(nil))
	return m, nil
}

// packManifest adds the pack's manifest and returns the data extension patterns it declares.
func (r *reader) packManifest() ([]string, error) {
	name, ok, err := r.firstExisting("qlpack.yml", "codeql-pack.yml")
	if err != nil || !ok {
		if err == nil {
			r.missing(RoleManifest, "qlpack.yml", "no pack manifest says which pack this query is in")
		}
		return nil, err
	}
	if _, err := r.add(RoleManifest, name, ""); err != nil {
		return nil, err
	}
	var manifest struct {
		Name           string    `yaml:"name"`
		Version        string    `yaml:"version"`
		DataExtensions yaml.Node `yaml:"dataExtensions"`
	}
	if err := r.decode(name, &manifest); err != nil {
		return nil, err
	}
	r.m.Pack, r.m.PackVersion = manifest.Name, manifest.Version
	var patterns []string
	switch manifest.DataExtensions.Kind {
	case yaml.ScalarNode:
		patterns = []string{manifest.DataExtensions.Value}
	case yaml.SequenceNode:
		if err := manifest.DataExtensions.Decode(&patterns); err != nil {
			return nil, fmt.Errorf("reading the data extensions %s declares: %w", name, err)
		}
	}
	return patterns, nil
}

func (r *reader) lock() error {
	const name = "codeql-pack.lock.yml"
	ok, err := r.add(RoleLock, name, "no lock beside the pack manifest, so the versions it compiles against are not pinned")
	if err != nil || !ok {
		return err
	}
	var lock struct {
		Dependencies map[string]struct {
			Version string `yaml:"version"`
		} `yaml:"dependencies"`
	}
	if err := r.decode(name, &lock); err != nil {
		return err
	}
	for pack, d := range lock.Dependencies {
		r.m.Dependencies = append(r.m.Dependencies, Dependency{Name: pack, Version: d.Version})
	}
	slices.SortFunc(r.m.Dependencies, func(a, b Dependency) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// extensions adds the files a declared pattern matches in this pack, never in a pack nested in it.
func (r *reader) extensions(pattern string) error {
	match, err := globRegexp(pattern)
	if err != nil {
		return fmt.Errorf("the data extension pattern %q: %w", pattern, err)
	}
	var found []string
	err = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && p != r.root && (strings.HasPrefix(d.Name(), ".") || holdsPack(p)):
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		if rel := r.rel(p); match.MatchString(rel) {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("finding the data extensions %q names: %w", pattern, err)
	}
	if len(found) == 0 {
		r.missing(RoleExtension, pattern, "the pack declares data extensions here, and nothing matches")
	}
	for _, rel := range found {
		if _, err := r.add(RoleExtension, rel, ""); err != nil {
			return err
		}
	}
	return nil
}

// tests adds each test of q: its .qlref, the postprocess it names, its test pack, and every file in
// its directory but the database a run built there.
func (r *reader) tests(q authoring.Query) error {
	if len(q.Tests) == 0 {
		name := strings.TrimSuffix(path.Base(q.Rel), ".ql")
		r.missing(RoleQLRef, "test/"+name+"/"+name+".qlref", "nothing tests "+q.Rel)
		return nil
	}
	packs := map[string]bool{}
	for _, qlref := range q.Tests {
		_, _, packDir := runner.PackOf(qlref)
		if packDir != "" && !packs[packDir] {
			packs[packDir] = true
			if err := r.testPack(packDir); err != nil {
				return err
			}
		}
		if err := r.test(qlref, packDir); err != nil {
			return err
		}
	}
	return nil
}

// testPack adds the manifest and lock of the pack a test lives in.
func (r *reader) testPack(packDir string) error {
	prefix := r.rel(packDir) + "/"
	name, ok, err := r.firstExisting(prefix+"qlpack.yml", prefix+"codeql-pack.yml")
	switch {
	case err != nil:
		return err
	case ok:
		if _, err := r.add(RoleTestManifest, name, ""); err != nil {
			return err
		}
	default:
		r.missing(RoleTestManifest, prefix+"qlpack.yml", "no manifest names the test pack")
	}
	_, err = r.add(RoleTestLock, prefix+"codeql-pack.lock.yml",
		"the test pack has no lock, so what its tests compile against is not pinned")
	return err
}

func (r *reader) test(qlref, packDir string) error {
	rel := r.rel(qlref)
	listed, err := r.add(RoleQLRef, rel, "")
	if err != nil {
		return err
	}
	if postprocess, err := r.postprocessOf(rel, listed); err != nil {
		return err
	} else if postprocess != "" {
		candidates := []string{}
		if packDir != "" {
			candidates = append(candidates, r.rel(filepath.Join(packDir, filepath.FromSlash(postprocess))))
		}
		candidates = append(candidates, path.Clean(postprocess))
		if ok, err := r.addFirst(RolePostprocess, candidates...); err != nil {
			return err
		} else if !ok {
			r.missing(RolePostprocess, candidates[0], rel+" names a postprocess query that is not there")
		}
	}
	base := strings.TrimSuffix(rel, ".qlref")
	if ok, err := r.exists(base + ".expected"); err != nil {
		return err
	} else if !ok {
		r.missing(RoleExpected, base+".expected", "no .expected beside the test")
	}
	dir := filepath.Dir(qlref)
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && strings.HasSuffix(d.Name(), ".testproj"):
			return filepath.SkipDir
		case d.IsDir() || filepath.Ext(p) == ".qlref":
			return nil
		}
		role := RoleFixture
		switch filepath.Ext(p) {
		case ".expected":
			role = RoleExpected
		case ".actual":
			role = RoleActual
		}
		_, err = r.add(role, r.rel(p), "")
		return err
	})
}

// add records the file at rel, and names it missing with why when it is not there. A path that
// leaves the pack, passes through a link or is not a regular file is refused and named: a symbolic
// link an authoring session left would otherwise put whatever it points at in a download.
func (r *reader) add(role, rel, why string) (bool, error) {
	rel = path.Clean(rel)
	if r.seen[rel] {
		return true, nil
	}
	body, err := readIn(r.fsys, rel)
	var refused *Refused
	switch {
	case errors.As(err, &refused):
		r.missing(role, rel, refused.Why)
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		if why != "" {
			r.missing(role, rel, why)
		}
		return false, nil
	case err != nil:
		return false, err
	}
	sum := sha256.Sum256(body)
	r.seen[rel] = true
	r.m.Components = append(r.m.Components, Component{Role: role, Path: rel, SHA256: hex.EncodeToString(sum[:]),
		Size: int64(len(body)), Bundled: role != RoleActual})
	return true, nil
}

func (r *reader) addFirst(role string, candidates ...string) (bool, error) {
	name, ok, err := r.firstExisting(candidates...)
	if err != nil || !ok {
		return false, err
	}
	return r.add(role, name, "")
}

func (r *reader) firstExisting(candidates ...string) (string, bool, error) {
	for _, c := range candidates {
		ok, err := r.exists(c)
		if err != nil || ok {
			return c, ok, err
		}
	}
	return "", false, nil
}

// exists reports whether something is at rel inside the pack. A link there exists, so whoever asks
// next names it refused rather than reading the next candidate in its place.
func (r *reader) exists(rel string) (bool, error) {
	rel = path.Clean(rel)
	if !fs.ValidPath(rel) {
		return false, nil
	}
	_, err := r.fsys.Lstat(filepath.FromSlash(rel))
	var refused *Refused
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case errors.As(refusal(r.fsys, rel), &refused):
		return true, nil
	}
	return false, err
}

// Refused is a path the pack does not let out: one outside it, one through a link, or something
// other than a regular file.
type Refused struct {
	Path, Why string
}

func (e *Refused) Error() string { return e.Path + ": " + e.Why }

// File is a bundled component, read back as its manifest listed it.
type File struct {
	Component
	Content []byte
}

// ErrChanged is a component whose content is not what its manifest's digest was taken over.
var ErrChanged = errors.New("it changed since the query's components were read")

// ComponentError is a bundled component that could not be read back as its manifest listed it.
type ComponentError struct {
	Path string
	Err  error
}

func (e *ComponentError) Error() string { return "bundling " + e.Path + ": " + e.Err.Error() }
func (e *ComponentError) Unwrap() error { return e.Err }

// Files reads back every component m bundles from the pack at root, in m's order. What is exported
// is exactly what is bundled, and only as it was when its digest was taken.
func Files(root string, m Manifest) ([]File, error) {
	fsys, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fsys.Close() }()
	var out []File
	for _, c := range m.Components {
		if !c.Bundled {
			continue
		}
		body, err := readIn(fsys, c.Path)
		if err != nil {
			return nil, &ComponentError{Path: c.Path, Err: err}
		}
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != c.SHA256 {
			return nil, &ComponentError{Path: c.Path, Err: ErrChanged}
		}
		out = append(out, File{Component: c, Content: body})
	}
	return out, nil
}

// ReadFile reads the component at rel in the pack at root. It refuses what Refused names, and reads
// through an os.Root, so a link swapped in after the check is not followed out of the pack either.
func ReadFile(root, rel string) ([]byte, error) {
	fsys, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fsys.Close() }()
	return readIn(fsys, path.Clean(rel))
}

func readIn(fsys *os.Root, rel string) ([]byte, error) {
	if err := refusal(fsys, rel); err != nil {
		return nil, err
	}
	return fsys.ReadFile(filepath.FromSlash(rel))
}

// refusal checks every directory on rel's path and then rel itself, nearest the root first.
func refusal(fsys *os.Root, rel string) error {
	if !fs.ValidPath(rel) {
		return &Refused{Path: rel, Why: "it is outside the query directory"}
	}
	parts := strings.Split(rel, "/")
	for i := range parts {
		info, err := fsys.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		switch {
		case err != nil && i < len(parts)-1 && !errors.Is(err, fs.ErrNotExist):
			return &Refused{Path: rel, Why: "a directory on its path cannot be read inside the pack, so ABOM does not follow it"}
		case err != nil:
			return err
		case info.Mode()&fs.ModeSymlink != 0 && i < len(parts)-1:
			return &Refused{Path: rel, Why: "a directory on its path is a link, and ABOM never follows a link out of the pack"}
		case i == len(parts)-1 && !info.Mode().IsRegular():
			return &Refused{Path: rel, Why: "it is not a regular file, and ABOM never follows a link out of the pack"}
		}
	}
	return nil
}

func (r *reader) missing(role, rel, why string) {
	r.m.Missing = append(r.m.Missing, Missing{Role: role, Path: rel, Why: why})
}

func (r *reader) decode(rel string, into any) error {
	body, err := readIn(r.fsys, rel)
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(body, into); err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}
	return nil
}

func (r *reader) rel(abs string) string {
	rel, err := filepath.Rel(r.root, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// holdsPack reports whether a directory is a pack of its own, which the enclosing pack excludes.
func holdsPack(dir string) bool {
	for _, name := range []string{"qlpack.yml", "codeql-pack.yml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// postprocessOf is the postprocess query the .qlref at rel names, empty when it names none or was
// itself refused.
func (r *reader) postprocessOf(rel string, listed bool) (string, error) {
	if !listed {
		return "", nil
	}
	body, err := readIn(r.fsys, rel)
	if err != nil {
		return "", err
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		if value, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), "postprocess:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`), nil
		}
	}
	return "", scanner.Err()
}

// deployed reports whether the deployed suite's include names id.
func (r *reader) deployed(suite, id string) (bool, error) {
	body, err := readIn(r.fsys, suite)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var directives []struct {
		Include struct {
			ID yaml.Node `yaml:"id"`
		} `yaml:"include"`
	}
	if err := yaml.Unmarshal(body, &directives); err != nil {
		return false, fmt.Errorf("reading the deployed suite: %w", err)
	}
	for _, d := range directives {
		var ids []string
		switch d.Include.ID.Kind {
		case yaml.ScalarNode:
			ids = []string{d.Include.ID.Value}
		case yaml.SequenceNode:
			if err := d.Include.ID.Decode(&ids); err != nil {
				return false, fmt.Errorf("reading the deployed suite: %w", err)
			}
		}
		if slices.Contains(ids, id) {
			return true, nil
		}
	}
	return false, nil
}

// globRegexp reads a data extension pattern: `**` crosses directories, `*` and `?` do not.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '*' && strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case c == '*' && strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// epoch dates every entry, so the archive depends on the files' bytes and nothing else.
var epoch = time.Unix(0, 0).UTC()

// Write writes the bundle: a gzipped tar of the bundled components in path order, each with fixed
// ownership, mode and time. A file whose bytes are no longer the ones m recorded is refused rather
// than bundled under a stale digest.
func Write(w io.Writer, root string, m Manifest) error {
	files, err := Files(root, m)
	if err != nil {
		return err
	}
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	if err := archive(gz, files); err != nil {
		return err
	}
	return gz.Close()
}

// archive writes files as a tar in path order, which is the same bytes for the same files: the
// bundle digest is this archive's, so it does not depend on how a Go release compresses it.
func archive(w io.Writer, files []File) error {
	tw := tar.NewWriter(w)
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	for _, f := range files {
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: f.Path, Mode: 0o644,
			Size: int64(len(f.Content)), ModTime: epoch, Format: tar.FormatPAX}); err != nil {
			return err
		}
		if _, err := io.Copy(tw, bytes.NewReader(f.Content)); err != nil {
			return err
		}
	}
	return tw.Close()
}
