package querypack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fullLock is what `codeql pack install` resolved for codeql/actions-all 0.7.0 on the custom pack.
const fullLock = `---
lockVersion: 1.0.0
dependencies:
  codeql/actions-all:
    version: 0.7.0
  codeql/concepts:
    version: 0.0.34
  codeql/controlflow:
    version: 2.0.44
  codeql/dataflow:
    version: 2.1.16
  codeql/javascript-all:
    version: 2.10.4
  codeql/mad:
    version: 1.0.60
  codeql/regex:
    version: 1.0.60
  codeql/ssa:
    version: 2.0.36
  codeql/threat-models:
    version: 1.1.0
  codeql/tutorial:
    version: 1.0.60
  codeql/typetracking:
    version: 2.0.44
  codeql/util:
    version: 2.0.47
  codeql/xml:
    version: 1.0.60
  codeql/yaml:
    version: 1.0.60
compiled: false
`

// CodeQL's own packaging is the independent reading: the files `codeql pack create` builds the query
// pack from, less what it generates, must be exactly the bundle's files outside the test pack.
func TestCodeQLBuildsTheQueryPackFromExactlyTheBundlesFiles(t *testing.T) {
	if os.Getenv("ABOM_ITEST_CODEQL") == "" {
		t.Skip("set ABOM_ITEST_CODEQL=1 to run `codeql pack create` over a bundle")
	}
	dir, q := pack(t)
	for name, body := range map[string]string{
		"codeql-pack.lock.yml": fullLock,
		"src/Q.ql": "/**\n * @name Q\n * @description q\n * @kind problem\n * @problem.severity warning\n * @id acme/q\n */\n" +
			"import actions\nprivate import lib.Helper\nfrom Workflow w where helper() select w, \"q\"\n",
		"src/lib/Helper.qll": "import Shared\nimport codeql.actions.Ast\npredicate helper() { shared() }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Read(dir, q)
	if err != nil {
		t.Fatal(err)
	}
	var bundle bytes.Buffer
	if err := Write(&bundle, dir, m); err != nil {
		t.Fatal(err)
	}
	var bundled []string
	for _, c := range m.Components {
		if c.Bundled && !strings.HasPrefix(c.Path, "test/") {
			bundled = append(bundled, c.Path)
		}
	}
	// The test pack is a pack of its own, which CodeQL builds the query pack without.
	stage := t.TempDir()
	unpack(t, bundle.Bytes(), stage, func(name string) bool { return slices.Contains(bundled, name) })

	out := t.TempDir()
	cmd := exec.Command("codeql", "pack", "create", "--output", out, "--", stage)
	if said, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("codeql pack create: %v\n%s", err, said)
	}
	contents := filepath.Join(out, filepath.FromSlash(m.Pack), m.PackVersion)
	var built []string
	err = filepath.WalkDir(contents, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && d.Name() == ".codeql":
			return filepath.SkipDir
		case d.IsDir() || d.Name() == ".packinfo" || filepath.Ext(p) == ".qlx":
			return nil
		}
		rel, err := filepath.Rel(contents, p)
		built = append(built, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(built)
	slices.Sort(bundled)
	if !slices.Equal(built, bundled) {
		t.Errorf("codeql pack create built from %v\nthe bundle's query pack holds %v", built, bundled)
	}
}

func unpack(t *testing.T, archive []byte, into string, keep func(string) bool) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	r := tar.NewReader(gz)
	for {
		h, err := r.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if !keep(h.Name) {
			continue
		}
		target := filepath.Join(into, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
