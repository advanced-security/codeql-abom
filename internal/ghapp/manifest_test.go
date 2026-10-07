package ghapp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// sourceOf locates the packages a Use may name.
var sourceOf = map[string]string{"catalog": "../catalog", "expand": "../expand", "events": "../events"}

func parsePackage(t *testing.T, name string) []*ast.File {
	t.Helper()
	dir, ok := sourceOf[name]
	if !ok {
		t.Fatalf("no source directory for package %q", name)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	fset := token.NewFileSet()
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		files = append(files, f)
	}
	return files
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	typ := fn.Recv.List[0].Type
	if star, ok := typ.(*ast.StarExpr); ok {
		typ = star.X
	}
	if id, ok := typ.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// declaration finds the top-level func, method or constant a caller names: pkg.Name or pkg.Type.Method.
func declaration(t *testing.T, caller string) ast.Node {
	t.Helper()
	parts := strings.Split(caller, ".")
	recv, name := "", parts[len(parts)-1]
	if len(parts) == 3 {
		recv = parts[1]
	}
	for _, f := range parsePackage(t, parts[0]) {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Name.Name == name && receiverName(d) == recv {
					return d
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && recv == "" {
						for _, id := range vs.Names {
							if id.Name == name {
								return vs
							}
						}
					}
				}
			}
		}
	}
	return nil
}

func stringLiterals(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// processorRoutes maps each event in events.Processor.handle's switch to the method it returns through, or "".
func processorRoutes(t *testing.T) map[string]string {
	t.Helper()
	handle, ok := declaration(t, "events.Processor.handle").(*ast.FuncDecl)
	if !ok {
		t.Fatal("events.Processor.handle is gone; the event table cannot be traced")
	}
	routes := map[string]string{}
	ast.Inspect(handle.Body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		handler := ""
		for _, stmt := range clause.Body {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok || len(ret.Results) == 0 {
				continue
			}
			if call, ok := ret.Results[0].(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "p" {
						handler = "events.Processor." + sel.Sel.Name
					}
				}
			}
		}
		for _, e := range clause.List {
			if lit, ok := e.(*ast.BasicLit); ok {
				event, _ := strconv.Unquote(lit.Value)
				routes[event] = handler
			}
		}
		return false
	})
	return routes
}

// documentedWithoutPermission are the events GitHub's webhook documentation names no repository or
// organization permission for; meta's "Meta" app permission is in no permission table and no manifest.
var documentedWithoutPermission = map[string]bool{
	"installation_target": true, "security_advisory": true, "meta": true,
}

// Each requested permission and event names an existing call that makes the request; none is a write or unneeded.
func TestEveryRequestedPermissionAndEventIsTracedToANamedCall(t *testing.T) {
	requested := map[string]bool{}
	neededByEvent := map[string]bool{}
	for _, s := range Events {
		neededByEvent[s.Permission] = true
		neededByEvent[s.Also] = true
	}
	for _, g := range Permissions {
		if requested[g.Permission] {
			t.Errorf("%s is listed twice", g.Permission)
		}
		requested[g.Permission] = true
		if g.Access != "read" {
			t.Errorf("%s: %s requested; the App writes nothing, and writes keep the operator's token",
				g.Permission, g.Access)
		}
		if g.Doc == "" {
			t.Errorf("%s names no GitHub documentation that says it covers its uses", g.Permission)
		}
		if len(g.Uses) == 0 && !neededByEvent[g.Permission] {
			t.Errorf("%s is requested and nothing needs it", g.Permission)
		}
		for _, u := range g.Uses {
			decl := declaration(t, u.Caller)
			if decl == nil {
				t.Errorf("%s: %s is justified by %s, which does not exist", g.Permission, u.Calls, u.Caller)
				continue
			}
			if !slices.ContainsFunc(stringLiterals(decl), func(s string) bool { return strings.Contains(s, u.Literal) }) {
				t.Errorf("%s: %s does not make %s; no %q in it", g.Permission, u.Caller, u.Calls, u.Literal)
			}
		}
	}

	routes := processorRoutes(t)
	subscribed := map[string]bool{}
	for _, s := range Events {
		if subscribed[s.Event] {
			t.Errorf("%s is subscribed twice", s.Event)
		}
		subscribed[s.Event] = true
		if s.Permission == "" && !documentedWithoutPermission[s.Event] {
			t.Errorf("%s names no permission, and GitHub documents one for it", s.Event)
		}
		for _, needs := range []string{s.Permission, s.Also} {
			if needs != "" && !requested[needs] {
				t.Errorf("%s needs %s, which is not requested", s.Event, needs)
			}
		}
		if got, ok := routes[s.Event]; !ok || got != s.Handler || got == "" {
			t.Errorf("%s is handled by %q in the processor, not %q", s.Event, got, s.Handler)
		}
	}

	m := NewManifest("abom-test", "https://example.com", "https://smee.io/x", "http://127.0.0.1:1/callback")
	want := map[string]string{}
	for _, g := range Permissions {
		want[g.Permission] = g.Access
	}
	if !reflect.DeepEqual(m.DefaultPermissions, want) {
		t.Errorf("manifest permissions %v, want exactly the table %v", m.DefaultPermissions, want)
	}
	var wantEvents []string
	for _, s := range Events {
		wantEvents = append(wantEvents, s.Event)
	}
	if got := slices.Sorted(slices.Values(m.DefaultEvents)); !slices.Equal(got, slices.Sorted(slices.Values(wantEvents))) {
		t.Errorf("manifest events %v, want exactly the table %v", got, wantEvents)
	}
	// GitHub delivers these to every App and refuses a subscription to them.
	for _, automatic := range []string{"installation", "installation_repositories", "ping"} {
		if slices.Contains(m.DefaultEvents, automatic) {
			t.Errorf("the manifest subscribes to %s, which GitHub sends every App unasked", automatic)
		}
	}
	if !m.HookAttributes.Active || m.HookAttributes.URL != "https://smee.io/x" || m.RedirectURL == "" {
		t.Errorf("manifest = %+v, want an active hook at the given URL and a redirect", m)
	}
}

// covers says whether a Use's literal accounts for a path. One ending in a placeholder names the
// resource itself, so it covers only itself: everything beneath a repository is another request.
func covers(use, path string) bool {
	if strings.HasSuffix(use, "%s") {
		return use == path
	}
	return strings.Contains(path, use)
}

// A REST path added to the reader without a permission fails here, not as a 403 on the first installation.
func TestEveryPathTheReaderBuildsIsCoveredByARequestedPermission(t *testing.T) {
	var uses []Use
	for _, g := range Permissions {
		for _, u := range g.Uses {
			if strings.HasPrefix(u.Caller, "catalog.") {
				uses = append(uses, u)
			}
		}
	}
	checked := 0
	for _, f := range parsePackage(t, "catalog") {
		for _, lit := range stringLiterals(f) {
			if !strings.Contains(lit, "/repos/") && !strings.Contains(lit, "/orgs/") && !strings.Contains(lit, "/users/") {
				continue
			}
			checked++
			if !slices.ContainsFunc(uses, func(u Use) bool { return covers(u.Literal, lit) }) {
				t.Errorf("the reader builds %q, and no requested permission covers it", lit)
			}
		}
	}
	if checked == 0 {
		t.Fatal("found no REST paths in the catalog; the scan is looking in the wrong place")
	}
	if _, err := os.Stat(sourceOf["catalog"]); err != nil {
		t.Fatal(err)
	}
}
