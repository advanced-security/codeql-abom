// Package harness drives an authoring session, in which an agent works a query request through
// ql-mcp, inside bounds ABOM decides, and records every call the agent makes.
package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/github/copilot-sdk/go/rpc"
)

// Server is the key ql-mcp is registered under; the CLI names its tools "<Server>-<tool>".
const Server = "ql-mcp"

// ABOM's own tools: what the catalog holds, the decision of which query a request is about, and a
// test run ABOM records.
const (
	ToolCatalog = "abom_catalog"
	ToolTarget  = "abom_target"
	ToolTest    = "abom_test"
)

// Steps a call works toward, named as authoring.Progress names them.
const (
	StepTarget   = "target"
	StepHelp     = "help"
	StepExpected = "expected"
	StepDatabase = "database"
	StepAST      = "ast"
	StepCompiles = "compiles"
	StepTests    = "tests"
)

// Verdict is what happens to one tool call. The zero value refuses it, so a verdict nobody
// filled in fails closed.
type Verdict struct {
	Allowed bool
	// Review holds the call for an operator, who reads what it would accept.
	Review bool
	// Reason is what the agent is told and what the timeline keeps.
	Reason string
	// Step is the authoring step the call works toward, empty when it is none.
	Step string
}

func refuse(format string, a ...any) Verdict { return Verdict{Reason: fmt.Sprintf(format, a...)} }

// Bounds is what one session may touch: the pack and the libraries to read, and one target
// query's files to write.
type Bounds struct {
	workspace string
	libraries []string
	denied    []string
	query     string
	tests     string
	// firstChange says why the target query must stay as it is, or is empty when it may change.
	firstChange func() string
}

// NewBounds resolves the pack and the library roots through every link, since a check against
// an unresolved path is a check against a name.
func NewBounds(workspace string, libraries []string) (Bounds, error) {
	ws, err := realDir(workspace)
	if err != nil {
		return Bounds{}, fmt.Errorf("the query pack: %w", err)
	}
	b := Bounds{workspace: ws}
	for _, lib := range libraries {
		dir, err := realDir(lib)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Bounds{}, fmt.Errorf("the library root %s: %w", lib, err)
		}
		b.libraries = append(b.libraries, dir)
	}
	return b, nil
}

// Deny puts paths out of reach even where the pack contains them: a session's own home, and
// ABOM's database, when a pack was configured around them.
func (b Bounds) Deny(paths ...string) (Bounds, error) {
	denied := slices.Clone(b.denied)
	for _, p := range paths {
		real, err := b.resolve(p)
		if err != nil {
			return Bounds{}, fmt.Errorf("denying %s: %w", p, err)
		}
		denied = append(denied, real)
	}
	b.denied = denied
	return b, nil
}

// Read adds directories a session reads and never writes: where ql-mcp puts what CodeQL printed.
func (b Bounds) Read(dirs ...string) (Bounds, error) {
	libraries := slices.Clone(b.libraries)
	for _, dir := range dirs {
		real, err := realDir(dir)
		if err != nil {
			return Bounds{}, fmt.Errorf("reading %s: %w", dir, err)
		}
		libraries = append(libraries, real)
	}
	b.libraries = libraries
	return b, nil
}

// Bind names the query a session writes and the directory its tests live in.
func (b Bounds) Bind(query, tests string) (Bounds, error) {
	q, err := b.resolve(query)
	if err != nil {
		return Bounds{}, err
	}
	t, err := b.resolve(tests)
	if err != nil {
		return Bounds{}, err
	}
	src, test := filepath.Join(b.workspace, "src"), filepath.Join(b.workspace, "test")
	switch {
	case filepath.Ext(q) != ".ql" || !inside(src, q):
		return Bounds{}, fmt.Errorf("%s is not a query under %s", query, src)
	case !inside(test, t):
		return Bounds{}, fmt.Errorf("%s is not a test directory under %s", tests, test)
	}
	b.query, b.tests = q, t
	return b, nil
}

// Query and Tests are the bound target's files, empty until one is bound.
func (b Bounds) Query() string { return b.query }

// Guard holds every write to the target query on why, which says what stands in the way or is
// empty when nothing does.
func (b Bounds) Guard(why func() string) Bounds {
	b.firstChange = why
	return b
}

func (b Bounds) Tests() string { return b.tests }

// Workspace is the pack, resolved.
func (b Bounds) Workspace() string { return b.workspace }

func (b Bounds) bound() bool { return b.query != "" }

func (b Bounds) stem() string { return strings.TrimSuffix(filepath.Base(b.query), ".ql") }

// database is where the CLI extracts a test directory: named after the directory, inside it.
func (b Bounds) database() string {
	return filepath.Join(b.tests, filepath.Base(b.tests)+".testproj")
}

// rel names p as the agent sees the pack, falling back to the whole path.
func (b Bounds) rel(p string) string {
	if r, err := filepath.Rel(b.workspace, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return real, nil
}

// resolve makes p absolute against the pack and follows every link in it that exists. What does
// not exist yet cannot be a link; a link that points nowhere is refused, because writing through
// it creates its target wherever it points.
func (b Bounds) resolve(p string) (string, error) {
	switch {
	case p == "":
		return "", errors.New("no path was named")
	case strings.ContainsRune(p, 0):
		return "", errors.New("a path cannot hold a NUL byte")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(b.workspace, p)
	}
	p = filepath.Clean(p)
	var rest []string
	for cur := p; ; {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", fmt.Errorf("%s leads nowhere: %w", cur, err)
			}
			return filepath.Join(append([]string{real}, rest...)...), nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("nothing on the way to %s exists", p)
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// within reports whether p is root or beneath it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func inside(root, p string) bool { return p != root && within(root, p) }

func (b Bounds) isDenied(p string) bool {
	return slices.ContainsFunc(b.denied, func(root string) bool { return within(root, p) })
}

func (b Bounds) readable(path string) error {
	p, err := b.resolve(path)
	if err != nil {
		return err
	}
	if b.isDenied(p) {
		return fmt.Errorf("%s is ABOM's own, which no session reads", path)
	}
	if within(b.workspace, p) || slices.ContainsFunc(b.libraries, func(lib string) bool {
		return within(lib, p)
	}) {
		return nil
	}
	return fmt.Errorf("%s is outside the query pack and the CodeQL libraries, which is all a session reads",
		path)
}

// fixtureExts are what the Actions extractor reads; anything else under a test directory either
// changes what the test is or is never looked at.
var fixtureExts = []string{".yml", ".yaml"}

// manifests change how a pack resolves, so one under a test directory changes every test in it.
var manifests = []string{"qlpack.yml", "codeql-pack.yml", "codeql-pack.lock.yml", "codeql-workspace.yml"}

// writable reports whether path is one of the target's files, and the step writing it works
// toward.
func (b Bounds) writable(path string) (string, error) {
	if !b.bound() {
		return "", fmt.Errorf("nothing is writable until the query this request is about is decided with %s",
			ToolTarget)
	}
	p, err := b.resolve(path)
	if err != nil {
		return "", err
	}
	dir, stem := filepath.Dir(b.query), b.stem()
	switch {
	case b.isDenied(p):
		return "", fmt.Errorf("%s is ABOM's own, which no session writes", path)
	case p == b.query:
		if b.firstChange != nil {
			if why := b.firstChange(); why != "" {
				return "", errors.New(why)
			}
		}
		return "", nil
	case p == filepath.Join(dir, stem+".md") || p == filepath.Join(dir, stem+".qhelp"):
		return StepHelp, nil
	case !inside(b.tests, p):
		return "", fmt.Errorf("a session writes %s, its help beside it and fixtures under %s, not %s",
			b.rel(b.query), b.rel(b.tests), b.rel(p))
	case within(b.database(), p):
		return "", fmt.Errorf("%s is the test database, which only the CLI writes", b.rel(b.database()))
	}
	name := filepath.Base(p)
	switch ext := filepath.Ext(name); {
	case ext == ".expected":
		return "", errors.New("an .expected file is accepted through codeql_test_accept, which an " +
			"operator reviews, and is never written by hand")
	case slices.Contains([]string{".qlref", ".ql", ".qll", ".actual"}, ext):
		return "", fmt.Errorf("%s decides what the test runs, and only ABOM and the CLI write it", name)
	case slices.Contains(manifests, name):
		return "", fmt.Errorf("%s is a pack manifest, and one under a test directory changes how "+
			"every test in it resolves", name)
	case !slices.Contains(fixtureExts, ext):
		return "", fmt.Errorf("%s is not YAML, and a fixture for an Actions query is: a workflow "+
			"under .github/workflows, or action metadata", name)
	}
	return "", nil
}

// Call decides a tool call as the agent makes it, before anything runs.
func (b Bounds) Call(tool string, args any) Verdict {
	fields, _ := args.(map[string]any)
	switch tool {
	case "view":
		return b.readCall(fields, "path")
	case "glob":
		if p, _ := fields["pattern"].(string); filepath.IsAbs(p) ||
			slices.Contains(strings.Split(filepath.ToSlash(p), "/"), "..") {
			return refuse("a glob pattern is taken from the directory it searches, so it can " +
				"neither be absolute nor climb out with ..")
		}
		return b.readCall(fields, "paths")
	case "grep":
		return b.readCall(fields, "paths")
	case "create", "edit":
		path, _ := fields["path"].(string)
		step, err := b.writable(path)
		if err != nil {
			return refuse("%v", err)
		}
		return Verdict{Allowed: true, Step: step}
	case ToolCatalog, ToolResults, ToolSource, ToolDraft:
		return Verdict{Allowed: true}
	case ToolTarget:
		return Verdict{Allowed: true, Step: StepTarget}
	case ToolTest:
		return Verdict{Allowed: true, Step: StepTests}
	}
	if name, ok := strings.CutPrefix(tool, Server+"-"); ok {
		return b.qlmcp(name, fields)
	}
	return refuse("%s is not part of the authoring loop, which has no shell and works with view, "+
		"glob, grep, create, edit, ql-mcp's test and query tools, and ABOM's %s and %s",
		tool, ToolCatalog, ToolTarget)
}

func (b Bounds) readCall(fields map[string]any, key string) Verdict {
	path := b.workspace
	if v, ok := fields[key]; ok {
		s, ok := v.(string)
		if !ok {
			return refuse("%s names one path", key)
		}
		path = s
	}
	if err := b.readable(path); err != nil {
		return refuse("%v", err)
	}
	return Verdict{Allowed: true}
}

// check judges one argument of a ql-mcp tool.
type check func(b Bounds, v any) error

type loopTool struct {
	step     string
	required []string
	args     map[string]check
}

// loopTools are the ql-mcp tools the loop uses, with every argument each may carry. Anything not
// listed is refused by name: ql-mcp advertises no schema for these, and the arguments left out
// accept output unread, pass any flag to the CLI, or read and write wherever they name.
var loopTools = map[string]loopTool{
	"codeql_test_extract": {step: StepDatabase, required: []string{"tests"}, args: map[string]check{
		"tests": targetTests, "threads": number, "ram": number, "verbose": boolean,
	}},
	"codeql_test_run": {step: StepTests, required: []string{"tests"}, args: map[string]check{
		"tests": targetTests, "show-extractor-output": boolean, "keep-databases": boolean,
		"threads": number, "ram": number, "verbose": boolean,
	}},
	"codeql_test_accept": {step: StepExpected, required: []string{"tests"}, args: map[string]check{
		"tests": targetTests, "verbose": boolean,
	}},
	"codeql_query_compile": {step: StepCompiles, required: []string{"query"}, args: map[string]check{
		"query": targetQuery, "database": targetDatabase, "dump-dil": boolean, "warnings": text,
		"verbose": boolean,
	}},
	"codeql_query_run": {required: []string{"database"}, args: map[string]check{
		"query": targetQuery, "queryName": toolQuery, "queryLanguage": actionsOnly,
		"database": targetDatabase, "sourceFiles": fileList, "format": text, "timeout": number,
	}},
	"codeql_resolve_metadata": {required: []string{"query"}, args: map[string]check{
		"query": packFile, "format": text, "verbose": boolean,
	}},
	"codeql_lsp_diagnostics": {required: []string{"ql_code"}, args: map[string]check{
		"ql_code": text, "log_level": text, "workspace_uri": packURI,
	}},
}

// unbound are the loop tools that read, so they help decide a target before there is one.
var unbound = []string{"codeql_resolve_metadata", "codeql_lsp_diagnostics"}

// loopNames are the ql-mcp tools a session is offered, in a stable order.
func loopNames() []string {
	names := make([]string, 0, len(loopTools))
	for name := range loopTools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// detours are ql-mcp tools an agent reached for in a recorded session, and why each is not the way.
var detours = map[string]string{
	"create_codeql_query": "ABOM lays out the target's query and test directory itself; this tool " +
		"writes another layout, and was used to make directories anywhere",
	"codeql_pack_install": "ABOM resolves the pack's dependencies, and an install rewrites its lock file",
}

// dangerous says what an argument would have done, for a refusal a reader can audit.
var dangerous = map[string]string{
	"learn":          "it accepts the output unread",
	"additionalArgs": "it passes any flag to the CLI",
	"output":         "it writes where it names",
	"logDir":         "it writes where it names",
	"evaluator-log":  "it writes where it names",
	"library":        "it compiles against a library it names",
	"queryPack":      "it runs a pack it names",
	"external":       "it reads files it names",
	"language":       "ql-mcp passes it to codeql test extract, which rejects it; the test pack names its extractor",
}

func (b Bounds) qlmcp(name string, fields map[string]any) Verdict {
	tool, ok := loopTools[name]
	if !ok {
		if why, ok := detours[name]; ok {
			return refuse("%s is not part of the authoring loop: %s", name, why)
		}
		return refuse("%s is not one of ql-mcp's tools the authoring loop uses", name)
	}
	if !b.bound() && !slices.Contains(unbound, name) {
		return refuse("decide the query this request is about with %s before running %s", ToolTarget, name)
	}
	takes := make([]string, 0, len(tool.args))
	for key := range tool.args {
		takes = append(takes, key)
	}
	sort.Strings(takes)
	given := make([]string, 0, len(fields))
	for key := range fields {
		given = append(given, key)
	}
	sort.Strings(given)
	for _, key := range given {
		judge, ok := tool.args[key]
		if !ok {
			v := refuse("%s takes %s; %s is not an argument the authoring loop allows", name,
				strings.Join(takes, ", "), key)
			if why, ok := dangerous[key]; ok {
				v.Reason += ", since " + why
			}
			return v
		}
		if err := judge(b, fields[key]); err != nil {
			return refuse("%s %s: %v", name, key, err)
		}
	}
	for _, key := range tool.required {
		if _, ok := fields[key]; !ok {
			return refuse("%s needs %s; it takes %s", name, key, strings.Join(takes, ", "))
		}
	}

	v := Verdict{Allowed: true, Step: tool.step}
	switch name {
	case "codeql_query_run":
		_, byPath := fields["query"]
		_, byName := fields["queryName"]
		switch {
		case byPath == byName:
			return refuse("codeql_query_run runs either the target's query or a tool query such as " +
				"PrintAST, not both or neither")
		case byName && fields["queryLanguage"] == nil:
			return refuse("codeql_query_run with a queryName needs queryLanguage actions")
		case byName:
			v.Step = StepAST
		}
	case "codeql_test_accept":
		v = Verdict{Review: true, Step: StepExpected, Reason: "an operator reads the output against " +
			"the request's examples before it becomes the expected result"}
	}
	return v
}

func targetTests(b Bounds, v any) error {
	list, ok := v.([]any)
	if !ok || len(list) == 0 {
		return fmt.Errorf("is a list of paths under %s", b.rel(b.tests))
	}
	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return fmt.Errorf("is a list of paths under %s", b.rel(b.tests))
		}
		p, err := b.resolve(s)
		if err != nil {
			return err
		}
		if !within(b.tests, p) {
			return fmt.Errorf("%s is not under %s, the target's test directory", s, b.rel(b.tests))
		}
	}
	return nil
}

func targetQuery(b Bounds, v any) error {
	return b.exactly(v, b.query, "the target query")
}

func targetDatabase(b Bounds, v any) error {
	return b.exactly(v, b.database(), "the target's test database")
}

func (b Bounds) exactly(v any, want, what string) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("is a path")
	}
	p, err := b.resolve(s)
	if err != nil {
		return err
	}
	if p != want {
		return fmt.Errorf("%s is not %s, %s", s, what, b.rel(want))
	}
	return nil
}

func packFile(b Bounds, v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("is a path")
	}
	p, err := b.resolve(s)
	if err != nil {
		return err
	}
	if !within(b.workspace, p) || b.isDenied(p) {
		return fmt.Errorf("%s is outside the query pack", s)
	}
	return nil
}

func packURI(b Bounds, v any) error {
	s, ok := v.(string)
	if !ok {
		return errors.New("is a file URI")
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" {
		return errors.New("is a file URI")
	}
	return packFile(b, u.Path)
}

func toolQuery(_ Bounds, v any) error {
	if s, _ := v.(string); s != "PrintAST" && s != "PrintCFG" {
		return errors.New("names PrintAST or PrintCFG, the tool queries ql-mcp has for Actions")
	}
	return nil
}

func actionsOnly(_ Bounds, v any) error {
	if s, _ := v.(string); s != "actions" {
		return errors.New("is actions, the only language this pack holds")
	}
	return nil
}

func number(_ Bounds, v any) error {
	switch v.(type) {
	case float64, int, int64, json.Number:
		return nil
	}
	return errors.New("is a number")
}

func boolean(_ Bounds, v any) error {
	if _, ok := v.(bool); !ok {
		return errors.New("is true or false")
	}
	return nil
}

func text(_ Bounds, v any) error {
	if _, ok := v.(string); !ok {
		return errors.New("is one string")
	}
	return nil
}

func fileList(_ Bounds, v any) error {
	if _, ok := v.(string); !ok {
		return errors.New("is one string of file names separated by commas, such as " +
			".github/workflows/a.yml,action.yml")
	}
	return nil
}

// Request decides a typed permission request, the CLI's own statement of what a call will touch,
// by the same bounds as the call before it.
func (b Bounds) Request(req rpc.PermissionRequest) Verdict {
	if req == nil {
		return refuse("an empty permission request")
	}
	switch r := req.(type) {
	case *rpc.PermissionRequestRead:
		if r.RequestSandboxBypass != nil && *r.RequestSandboxBypass {
			return refuse("a session never reads outside the sandbox")
		}
		for _, p := range named(r.Path, r.ResolvedPath) {
			if err := b.readable(p); err != nil {
				return refuse("%v", err)
			}
		}
		return Verdict{Allowed: true}
	case *rpc.PermissionRequestWrite:
		if r.RequestSandboxBypass != nil && *r.RequestSandboxBypass {
			return refuse("a session never writes outside the sandbox")
		}
		v := Verdict{Allowed: true}
		for _, p := range named(r.FileName, r.ResolvedPath) {
			step, err := b.writable(p)
			if err != nil {
				return refuse("%v", err)
			}
			v.Step = step
		}
		return v
	case *rpc.PermissionRequestMCP:
		name, ok := strings.CutPrefix(r.ToolName, r.ServerName+"-")
		if r.ServerName != Server || !ok {
			return refuse("%s is not one of %s's tools", r.ToolName, Server)
		}
		fields, _ := r.Args.(map[string]any)
		return b.qlmcp(name, fields)
	case *rpc.PermissionRequestHook:
		if v := b.Call(r.ToolName, r.ToolArgs); v.Review {
			return v
		}
		return refuse("only accepting expected output is held for an operator, not %s", r.ToolName)
	case *rpc.PermissionRequestCustomTool:
		if slices.Contains([]string{ToolCatalog, ToolTarget, ToolTest, ToolResults, ToolSource, ToolDraft}, r.ToolName) {
			return b.Call(r.ToolName, r.Args)
		}
		return refuse("%s is not one of ABOM's tools", r.ToolName)
	}
	return refuse("a %s request is not part of the authoring loop", req.Kind())
}

// named is the path a request states, and the CLI's resolution of it when it gave one.
func named(path string, resolved *string) []string {
	out := []string{path}
	if resolved != nil && *resolved != "" {
		out = append(out, *resolved)
	}
	return out
}
