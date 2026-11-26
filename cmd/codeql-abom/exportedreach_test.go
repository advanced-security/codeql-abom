package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// An exported name in internal/ that only tests use is dead code with a witness.
//
// `TestEveryInternalPackageIsReachableFromABinary` catches a whole package nobody reaches, and
// `unused` catches unexported symbols nothing uses. Between them sits a gap neither sees: an
// exported symbol in an internal package, which `unused` assumes is API and leaves alone, and
// which a package-level reachability check counts as reached because a sibling file is reached.
// internal/ has no external consumers, so "exported" there means nothing except that a test can
// see it.
//
// Package-level functions, types, variables and constants, each known by its package and name:
// from another package one can only be named through its package's import, and inside its own
// only bare. Methods and fields are left out: one satisfying an interface, or read by
// encoding/json, has no textual use, so flagging them would report the working code. So is a
// constant with a value of its own, which names a value the data can hold whether or not Go code
// spells it; one re-exporting another package's is checked.
func TestNoExportedNameInInternalIsOnlyUsedByTests(t *testing.T) {
	root := repoRoot(t)
	const module = "github.com/advanced-security/codeql-abom/"

	declared := map[exportedName]string{}          // the file that declares it
	usedFrom := map[exportedName]map[string]bool{} // the package directories using it
	use := func(n exportedName, from string) {
		if usedFrom[n] == nil {
			usedFrom[n] = map[string]bool{}
		}
		usedFrom[n][from] = true
	}

	// infra holds the module's own test tooling, such as the issuer a local cluster signs in through:
	// a command, so what it uses is used.
	for _, top := range []string{"internal", "cmd", "infra"} {
		forEachGoFile(t, filepath.Join(root, top), func(path string, file *ast.File) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			dir := filepath.Dir(path)

			// The declaration's own name is not a use of it. Skipping by *identifier* rather than
			// by file matters: `Batch` and `OrgKey` are both used in the file that declares them.
			own := map[*ast.Ident]bool{}
			if top == "internal" {
				for _, id := range exportedDeclarations(file) {
					own[id] = true
					declared[exportedName{dir, id.Name}] = path
				}
			}
			imported := map[string]string{} // local name -> package directory
			for _, imp := range file.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				rel, ok := strings.CutPrefix(p, module)
				if !ok {
					continue
				}
				name := pathpkg.Base(p)
				if imp.Name != nil {
					name = imp.Name.Name
				}
				imported[name] = filepath.Join(root, rel)
			}

			var visit func(ast.Node) bool
			visit = func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.SelectorExpr:
					if x, ok := v.X.(*ast.Ident); ok {
						if pkg, ok := imported[x.Name]; ok {
							use(exportedName{pkg, v.Sel.Name}, dir)
							return false
						}
					}
					// A field or method selected from a value names nothing at package level.
					ast.Inspect(v.X, visit)
					return false
				case *ast.Ident:
					if !own[v] {
						use(exportedName{dir, v.Name}, dir)
					}
				}
				return true
			}
			ast.Inspect(file, visit)
		})
	}

	var orphans []string
	for n, file := range declared {
		if len(usedFrom[n]) == 0 {
			orphans = append(orphans, n.name+" ("+strings.TrimPrefix(file, root+"/")+")")
		}
	}
	sort.Strings(orphans)

	if len(orphans) > 0 {
		t.Errorf("%d exported name(s) in internal/ that no production code uses:\n\t%s\n"+
			"Unexport it, delete it, or give it a use. internal/ has no consumers outside this "+
			"module, so an exported name only tests reach is dead code that reviews as API.",
			len(orphans), strings.Join(orphans, "\n\t"))
	}
}

type exportedName struct{ dir, name string }

// reexports reports whether the i-th constant of spec is another package's, named through it.
func reexports(spec *ast.ValueSpec, i int) bool {
	if i >= len(spec.Values) {
		return false
	}
	sel, ok := spec.Values[i].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	_, ok = sel.X.(*ast.Ident)
	return ok
}

// exportedDeclarations are the identifiers of a file's exported package-level functions, types,
// variables, and constants re-exporting another package's.
func exportedDeclarations(file *ast.File) []*ast.Ident {
	var out []*ast.Ident
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.IsExported() {
				out = append(out, d.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, s.Name)
					}
				case *ast.ValueSpec:
					for i, id := range s.Names {
						if id.IsExported() && (d.Tok == token.VAR || reexports(s, i)) {
							out = append(out, id)
						}
					}
				}
			}
		}
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for dir != "/" {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the working directory")
	return ""
}

func forEachGoFile(t *testing.T, root string, fn func(path string, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Directories holding no Go source of this module: git metadata, the npm and .NET trees,
		// and `.abom`, which since `make test` points TMPDIR inside the checkout also holds the
		// toolchain's build scratch. Those come and go while the walk is running.
		if d.IsDir() && path != root {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" ||
				name == "bin" || name == "obj" {
				return filepath.SkipDir
			}
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		fn(path, parsed)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}
