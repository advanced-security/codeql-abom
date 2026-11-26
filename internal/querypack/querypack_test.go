package querypack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/customql"
)

const queryQ = `/**
 * @name Q
 * @kind problem
 * @id acme/q
 */
import actions
private import lib.Helper
from int i select i, "q"
`

const lock = `---
lockVersion: 1.0.0
dependencies:
  codeql/actions-all:
    version: 0.7.0
  codeql/util:
    version: 2.0.47
compiled: false
`

// pack lays out a scaffolded pack holding acme/q, with a library, help, a data extension and a test,
// and acme/other beside it, whose files must never appear in acme/q's components.
func pack(t *testing.T) (string, authoring.Query) {
	t.Helper()
	dir := t.TempDir()
	if _, err := customql.Scaffold(dir, "2.27.0"); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "qlpack.yml"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"qlpack.yml":                      string(manifest) + "dataExtensions:\n  - ext/**/*.model.yml\n",
		"codeql-pack.lock.yml":            lock,
		"test/codeql-pack.lock.yml":       lock,
		"src/Q.ql":                        queryQ,
		"src/Q.md":                        "# Q\n",
		"src/lib/Helper.qll":              "import Shared\nimport codeql.actions.Ast\n",
		"src/lib/Shared.qll":              "predicate shared() { any() }\n",
		"src/lib/Unused.qll":              "predicate unused() { any() }\n",
		"src/Other.ql":                    "/**\n * @kind problem\n * @id acme/other\n */\nimport lib.Unused\nfrom int i select i, \"o\"\n",
		"src/Other.md":                    "# Other\n",
		"ext/sinks/a.model.yml":           "extensions: []\n",
		"test/Q/.github/workflows/ci.yml": "on: push # $ Alert\n",
		"test/Q/Q.expected":               "| ci.yml |\n",
		"test/Q/Q.actual":                 "| ci.yml | extra |\n",
		"test/Q/Q.testproj/db.trap":       "built\n",
		"test/Other/action.yml":           "runs: {}\n",
		"test/Other/Other.expected":       "\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"Q", "Other"} {
		if _, err := customql.ScaffoldTest(dir, name); err != nil {
			t.Fatal(err)
		}
	}
	q, found, err := authoring.Find(dir, "acme/q")
	if err != nil || !found {
		t.Fatalf("finding acme/q: %v, %v", found, err)
	}
	return dir, q
}

func roles(m Manifest) map[string]string {
	out := map[string]string{}
	for _, c := range m.Components {
		out[c.Path] = c.Role
	}
	return out
}

// Everything that makes a query run, and only that: its pack's manifest, lock and suite, the query,
// every library of its own pack it imports however far away, its help, the pack's data extensions,
// and its tests with the test pack, the postprocess they name, their fixtures and outputs. A sibling
// query's files, a library nothing imports and a test's built database are none of it.
func TestAQuerysComponentsAreEverythingThatMakesItRunAndNothingElse(t *testing.T) {
	dir, q := pack(t)

	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"qlpack.yml":                      RoleManifest,
		"codeql-pack.lock.yml":            RoleLock,
		"codeql-suites/abom-custom.qls":   RoleSuite,
		"src/Q.ql":                        RoleQuery,
		"src/lib/Helper.qll":              RoleLibrary,
		"src/lib/Shared.qll":              RoleLibrary,
		"src/Q.md":                        RoleHelp,
		"ext/sinks/a.model.yml":           RoleExtension,
		"test/qlpack.yml":                 RoleTestManifest,
		"test/codeql-pack.lock.yml":       RoleTestLock,
		"test/" + customql.Postprocess:    RolePostprocess,
		"test/Q/Q.qlref":                  RoleQLRef,
		"test/Q/.github/workflows/ci.yml": RoleFixture,
		"test/Q/Q.expected":               RoleExpected,
		"test/Q/Q.actual":                 RoleActual,
	}
	got := roles(m)
	for path, role := range want {
		if got[path] != role {
			t.Errorf("%s is %q, want %q", path, got[path], role)
		}
	}
	for path, role := range got {
		if _, ok := want[path]; !ok {
			t.Errorf("%s (%s) is not one of acme/q's components", path, role)
		}
	}
	for _, c := range m.Components {
		body, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Path)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if c.SHA256 != hex.EncodeToString(sum[:]) || c.Size != int64(len(body)) {
			t.Errorf("%s digest %s size %d, want the file's", c.Path, c.SHA256, c.Size)
		}
		if c.Bundled == (c.Role == RoleActual) {
			t.Errorf("%s bundled = %v: only a run's output stays out of the bundle", c.Path, c.Bundled)
		}
	}
	if len(m.Missing) != 0 {
		t.Errorf("a complete query is missing %+v", m.Missing)
	}
	if m.Query != "acme/q" || m.Version != q.Version || m.Pack != customql.Pack || m.PackVersion != "2.27.0" {
		t.Errorf("identity = %s %s %s %s", m.Query, m.Version, m.Pack, m.PackVersion)
	}
	if want := []Dependency{{"codeql/actions-all", "0.7.0"}, {"codeql/util", "2.0.47"}}; !slices.Equal(m.Dependencies, want) {
		t.Errorf("dependencies = %v, want the lock's %v", m.Dependencies, want)
	}
	if want := []string{"actions", "codeql.actions.Ast"}; !slices.Equal(m.Outside, want) {
		t.Errorf("outside = %v, want %v", m.Outside, want)
	}
	if !m.Development || m.Deployed {
		t.Errorf("development %v, deployed %v; want only the development suite", m.Development, m.Deployed)
	}
	if err := customql.WriteDeployed(dir, []string{"acme/q"}); err != nil {
		t.Fatal(err)
	}
	if m, err = Read(dir, q); err != nil || !m.Deployed {
		t.Errorf("deployed = %v, %v after deploying acme/q", m.Deployed, err)
	}
}

// A component that should exist and does not is named with what it would be, never left out: a
// manifest without help would read as a query that has none to give.
func TestAComponentThatShouldExistAndDoesNotIsNamed(t *testing.T) {
	dir, q := pack(t)
	for _, name := range []string{"src/Q.md", "codeql-pack.lock.yml", "test/Q/Q.expected", "ext/sinks/a.model.yml"} {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}

	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]string{}
	for _, missing := range m.Missing {
		if missing.Why == "" {
			t.Errorf("%s is missing without a reason", missing.Path)
		}
		named[missing.Path] = missing.Role
	}
	for path, role := range map[string]string{
		"src/Q.md": RoleHelp, "codeql-pack.lock.yml": RoleLock, "test/Q/Q.expected": RoleExpected,
		"ext/**/*.model.yml": RoleExtension,
	} {
		if named[path] != role {
			t.Errorf("missing %s = %q, want %q among %+v", path, named[path], role, m.Missing)
		}
	}
	if len(m.Dependencies) != 0 {
		t.Errorf("dependencies %v read from a lock that is not there", m.Dependencies)
	}
}

// A query nothing tests is a fact about the query, said once.
func TestAQueryNothingTestsSaysSo(t *testing.T) {
	dir, q := pack(t)
	if err := os.RemoveAll(filepath.Join(dir, "test", "Q")); err != nil {
		t.Fatal(err)
	}
	q.Tests = nil

	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Missing) != 1 || m.Missing[0].Role != RoleQLRef {
		t.Errorf("missing = %+v, want one entry saying nothing tests src/Q.ql", m.Missing)
	}
}

func entries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		out[h.Name] = body
	}
}

// The bundle holds exactly the bundled components, byte for byte, and is the same bytes for the same
// files on every reading, so its digest can stand for it; one edited fixture is a different bundle.
// The digest is the uncompressed archive's: compression differs between Go releases, and a reviewed
// bundle must still be the same bundle on a coordinator built with another.
func TestTheBundleIsTheBundledComponentsAndTheSameBytesForTheSameFiles(t *testing.T) {
	dir, q := pack(t)
	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	var first, second bytes.Buffer
	if err := Write(&first, dir, m); err != nil {
		t.Fatal(err)
	}
	if err := Write(&second, dir, m); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("two bundles of unchanged files differ")
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	archive, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(archive)
	if m.BundleSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("the manifest names bundle %s, the uncompressed archive is %x", m.BundleSHA256, sum)
	}
	got := entries(t, first.Bytes())
	for _, c := range m.Components {
		body, inBundle := got[c.Path]
		if inBundle != c.Bundled {
			t.Errorf("%s in the bundle = %v, want %v", c.Path, inBundle, c.Bundled)
		}
		if inBundle {
			if on, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.Path))); !bytes.Equal(body, on) {
				t.Errorf("%s in the bundle is not the file", c.Path)
			}
		}
	}
	if len(got) != len(m.Components)-1 {
		t.Errorf("the bundle holds %d files, want every component but the .actual", len(got))
	}

	if err := os.WriteFile(filepath.Join(dir, "test", "Q", ".github", "workflows", "ci.yml"), []byte("on: pull_request\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(io.Discard, dir, m); err == nil {
		t.Error("a bundle was written from a file that changed after its manifest was read")
	}
	edited, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	if edited.BundleSHA256 == m.BundleSHA256 {
		t.Error("an edited fixture left the bundle digest where it was")
	}
}

// An authoring session writes into this directory and a reader downloads the bundle, so a link it
// left must never carry what it points at out: it is named as refused, and neither listed nor bundled.
func TestALinkInTheQueryDirectoryIsRefusedNeverFollowed(t *testing.T) {
	dir, q := pack(t)
	secret := filepath.Join(t.TempDir(), "private-key.pem")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "test", "Q", "key.yml")); err != nil {
		t.Fatal(err)
	}

	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	if _, listed := roles(m)["test/Q/key.yml"]; listed {
		t.Error("a link was listed as a component")
	}
	refused := slices.ContainsFunc(m.Missing, func(x Missing) bool { return x.Path == "test/Q/key.yml" })
	if !refused {
		t.Errorf("the link is not named as refused: %+v", m.Missing)
	}
	var bundle bytes.Buffer
	if err := Write(&bundle, dir, m); err != nil {
		t.Fatal(err)
	}
	for name, body := range entries(t, bundle.Bytes()) {
		if bytes.Contains(body, []byte("SECRET")) {
			t.Errorf("%s in the bundle carries what the link points at", name)
		}
	}
}

// A link one directory up is a link all the same: a test naming a postprocess query through a
// directory that links out of the pack must not carry what that directory holds into the bundle or
// an export.
func TestALinkedDirectoryOnAComponentsPathIsRefusedNeverFollowed(t *testing.T) {
	dir, q := pack(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Secret.ql"), []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "test", "linked")); err != nil {
		t.Fatal(err)
	}
	qlref := filepath.Join(dir, "test", "Q", "Q.qlref")
	body, err := os.ReadFile(qlref)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "postprocess:") {
			lines = append(lines, line)
		}
	}
	if err := os.WriteFile(qlref, []byte("postprocess: linked/Secret.ql\n"+strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	if role, listed := roles(m)["test/linked/Secret.ql"]; listed {
		t.Errorf("a file through a linked directory was listed as the %s", role)
	}
	refused := slices.ContainsFunc(m.Missing, func(x Missing) bool {
		return x.Path == "test/linked/Secret.ql" && strings.Contains(x.Why, "link")
	})
	if !refused {
		t.Errorf("the file through a linked directory is not named as refused: %+v", m.Missing)
	}
	var bundle bytes.Buffer
	if err := Write(&bundle, dir, m); err != nil {
		t.Fatal(err)
	}
	for name, body := range entries(t, bundle.Bytes()) {
		if bytes.Contains(body, []byte("SECRET")) {
			t.Errorf("%s in the bundle carries what the linked directory holds", name)
		}
	}
	if _, err := ReadFile(dir, "test/linked/Secret.ql"); err == nil {
		t.Error("ReadFile read through a linked directory")
	}
}
