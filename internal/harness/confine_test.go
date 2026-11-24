package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/authoring"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/github/copilot-sdk/go/rpc"
)

// recorded is what two live sessions sent the host, with machine paths as placeholders.
type recorded struct {
	Calls []struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	} `json:"calls"`
	Permissions []json.RawMessage `json:"permissions"`
}

// place lays out a pack like the one the recording ran in, with a library cache beside it.
type place struct {
	root, ws, codeql, qlmcp string
}

func newPlace(t *testing.T) place {
	t.Helper()
	root := t.TempDir()
	p := place{
		root:   root,
		ws:     filepath.Join(root, "ws"),
		codeql: filepath.Join(root, "home", ".codeql"),
		qlmcp:  filepath.Join(root, "qlmcp"),
	}
	for _, dir := range []string{
		filepath.Join(p.ws, "src"),
		filepath.Join(p.ws, "test", "CheckoutTagPin", ".github", "workflows"),
		filepath.Join(p.ws, "test", "SelfHostedRunner"),
		filepath.Join(p.codeql, "packages", "codeql", "actions-all", "0.6.2"),
		filepath.Join(p.qlmcp, "ql", "actions"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// bound is the recording's own target: CheckoutTagPin, tested under test/CheckoutTagPin.
func (p place) bound(t *testing.T) Bounds {
	t.Helper()
	b, err := NewBounds(p.ws, []string{filepath.Join(p.codeql, "packages")})
	if err != nil {
		t.Fatal(err)
	}
	b, err = b.Bind(filepath.Join(p.ws, "src", "CheckoutTagPin.ql"),
		filepath.Join(p.ws, "test", "CheckoutTagPin"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (p place) recording(t *testing.T) recorded {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "recorded-session.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.NewReplacer("$WS", p.ws, "$CODEQL", p.codeql, "$QLMCP", p.qlmcp,
		"$OUTSIDE", p.root).Replace(string(body))
	var r recorded
	if err := json.Unmarshal([]byte(text), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// request decodes a recorded permission request with the decoder the SDK uses on the wire.
func request(t *testing.T, raw json.RawMessage) rpc.PermissionRequest {
	t.Helper()
	var data rpc.PermissionRequestedData
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"requestId":"r","permissionRequest":%s}`, raw)),
		&data); err != nil {
		t.Fatal(err)
	}
	return data.PermissionRequest
}

// inTarget is the loosest thing a write the session may make can be: somewhere under the target's
// query or test directory.
func (p place) inTarget(path string) bool {
	return strings.HasPrefix(path, filepath.Join(p.ws, "src", "CheckoutTagPin.")) ||
		strings.HasPrefix(path, filepath.Join(p.ws, "test", "CheckoutTagPin")+string(filepath.Separator))
}

// The whole recording, not a chosen case: an agent that could not make a directory tried the
// shell, a manifest and a scaffolding tool with a traversing name, and none of them may land.
func TestNoRecordedWriteLandsOutsideTheTarget(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	r := p.recording(t)

	for _, raw := range r.Permissions {
		req, ok := request(t, raw).(*rpc.PermissionRequestWrite)
		if !ok {
			continue
		}
		if v := b.Request(req); v.Allowed && !p.inTarget(req.FileName) {
			t.Errorf("write of %s allowed outside the target", req.FileName)
		}
	}
	for _, c := range r.Calls {
		if c.Tool != "create" && c.Tool != "edit" {
			continue
		}
		path, _ := c.Args["path"].(string)
		if v := b.Call(c.Tool, c.Args); v.Allowed && !p.inTarget(path) {
			t.Errorf("%s of %s allowed outside the target", c.Tool, path)
		}
	}
}

func TestTheRecordedLoopIsAllowedAndItsDetoursAreNot(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	r := p.recording(t)
	ws := p.ws

	// A file tool is known by the file it names; its text is the agent's and decides nothing.
	subject := func(tool string, args map[string]any) string {
		if path, ok := args["path"].(string); ok && (tool == "create" || tool == "edit") {
			return tool + " " + path
		}
		body, _ := json.Marshal(args)
		return tool + " " + string(body)
	}
	allowed := map[string]bool{}
	for _, c := range r.Calls {
		allowed[subject(c.Tool, c.Args)] = b.Call(c.Tool, c.Args).Allowed
	}
	call := func(tool, args string) (bool, bool) {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(strings.ReplaceAll(args, "$WS", ws)), &parsed); err != nil {
			t.Fatal(err)
		}
		got, ok := allowed[subject(tool, parsed)]
		return got, ok
	}

	for _, tc := range []struct {
		tool, args string
		want       bool
	}{
		{"view", `{"path":"$WS/src/SelfHostedRunner.ql"}`, true},
		{"create", `{"path":"$WS/src/CheckoutTagPin.ql"}`, true},
		{"create", `{"path":"$WS/test/CheckoutTagPin/.github/workflows/checkout.yml"}`, true},
		{"ql-mcp-codeql_test_extract", `{"tests":["$WS/test/CheckoutTagPin"]}`, true},
		{"ql-mcp-codeql_query_run", `{"database":"$WS/test/CheckoutTagPin/CheckoutTagPin.testproj","queryLanguage":"actions","queryName":"PrintAST"}`, true},
		{"ql-mcp-codeql_query_compile", `{"query":"$WS/src/CheckoutTagPin.ql"}`, true},
		{"ql-mcp-codeql_query_run", `{"database":"$WS/test/CheckoutTagPin/CheckoutTagPin.testproj","query":"$WS/src/CheckoutTagPin.ql"}`, true},
		{"ql-mcp-codeql_test_run", `{"tests":["$WS/test/CheckoutTagPin/CheckoutTagPin.qlref"]}`, true},

		// Evaluating writes a cache into the database it runs against, which is another test's.
		{"ql-mcp-codeql_query_run", `{"database":"$WS/test/SelfHostedRunner/SelfHostedRunner.testproj","queryLanguage":"actions","queryName":"PrintAST"}`, false},
		// ABOM lays out the target; this tool writes another layout, and was used to make directories.
		{"ql-mcp-create_codeql_query", `{"basePath":"$WS/test/CheckoutTagPin","description":"scaffold helper used only to create the .github/workflows directory","language":"actions","queryId":"abom/scaffold-helper","queryName":"../../CheckoutTagPin/.github/workflows/Scaffold"}`, false},
		{"ql-mcp-codeql_pack_install", `{}`, false},
		{"ql-mcp-codeql_test_extract", `{"testPath":"$WS/test/CheckoutTagPin"}`, false},
		// One path where a list is taken means that one test; ABOM passes it as a list.
		{"ql-mcp-codeql_test_extract", `{"tests":"$WS/test/CheckoutTagPin"}`, true},
		{"bash", `{"command":"mkdir -p $WS/test/CheckoutTagPin/.github/workflows","description":"Create workflows directory"}`, false},
		{"edit", `{"path":"$WS/test/CheckoutTagPin/CheckoutTagPin.qlref"}`, false},
		{"create", `{"path":"$WS/codeql-workspace.yml"}`, false},
		{"create", `{"path":"$WS/test/CheckoutTagPin/checkout.yml.tmp"}`, false},
	} {
		got, ok := call(tc.tool, tc.args)
		if !ok {
			t.Errorf("%s %s is not in the recording", tc.tool, tc.args)
			continue
		}
		if got != tc.want {
			t.Errorf("%s %s: allowed %v, want %v", tc.tool, tc.args, got, tc.want)
		}
	}
}

func TestTheRecordedPermissionRequestsAreDecidedLikeTheCallsBeforeThem(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	ws, lib := p.ws, filepath.Join(p.codeql, "packages")

	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"kind":"read","path":"$WS/test/SelfHostedRunner/SelfHostedRunner.expected"}`, true},
		{`{"kind":"read","path":"$LIB/codeql/actions-all/0.6.2/codeql/actions/Ast.qll"}`, true},
		{`{"kind":"read","path":"$CODEQL"}`, false},
		{`{"kind":"read","path":"$QLMCP/ql/actions"}`, false},
		{`{"kind":"read","path":"$OUTSIDE"}`, false},
		{`{"kind":"write","fileName":"$WS/test/CheckoutTagPin/.github/workflows/checkout.yml"}`, true},
		{`{"kind":"write","fileName":"$WS/src/CheckoutTagPin.ql"}`, true},
		{`{"kind":"write","fileName":"$WS/codeql-workspace.yml"}`, false},
		{`{"kind":"write","fileName":"$WS/test/CheckoutTagPin/CheckoutTagPin.qlref"}`, false},
		{`{"kind":"mcp","serverName":"ql-mcp","toolName":"ql-mcp-codeql_test_run","readOnly":false,"args":{"tests":["$WS/test/CheckoutTagPin/CheckoutTagPin.qlref"]}}`, true},
		{`{"kind":"mcp","serverName":"ql-mcp","toolName":"ql-mcp-codeql_pack_install","readOnly":false,"args":{}}`, false},
		{`{"kind":"mcp","serverName":"other","toolName":"other-codeql_test_run","readOnly":false,"args":{"tests":["$WS/test/CheckoutTagPin/CheckoutTagPin.qlref"]}}`, false},
		{`{"kind":"shell","fullCommandText":"ls","commands":[],"possiblePaths":[],"possibleUrls":[],"hasWriteFileRedirection":false,"canOfferSessionApproval":false,"intention":"list"}`, false},
	} {
		raw := strings.NewReplacer("$WS", ws, "$LIB", lib, "$CODEQL", p.codeql, "$QLMCP", p.qlmcp,
			"$OUTSIDE", p.root).Replace(tc.raw)
		if got := b.Request(request(t, json.RawMessage(raw))); got.Allowed != tc.want {
			t.Errorf("%s: allowed %v, want %v (%s)", tc.raw, got.Allowed, tc.want, got.Reason)
		}
	}
}

// Each of these widens what one tool call can do to something a person never asked for:
// `learn` accepts output unread, `additionalArgs` passes any flag, and the rest write or read
// wherever they name.
func TestAnArgumentTheLoopDoesNotNeedIsRefusedByName(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	query := filepath.Join(p.ws, "src", "CheckoutTagPin.ql")
	db := filepath.Join(p.ws, "test", "CheckoutTagPin", "CheckoutTagPin.testproj")
	tests := []any{filepath.Join(p.ws, "test", "CheckoutTagPin")}

	for _, tc := range []struct {
		tool string
		args map[string]any
		name string
	}{
		{"ql-mcp-codeql_test_run", map[string]any{"tests": tests, "learn": true}, "learn"},
		{"ql-mcp-codeql_test_run", map[string]any{"tests": tests, "additionalArgs": []any{"--search-path=/"}}, "additionalArgs"},
		{"ql-mcp-codeql_test_run", map[string]any{"tests": tests, "logDir": p.root}, "logDir"},
		{"ql-mcp-codeql_query_compile", map[string]any{"query": query, "output": filepath.Join(p.root, "x.qlo")}, "output"},
		{"ql-mcp-codeql_query_compile", map[string]any{"query": query, "library": p.root}, "library"},
		{"ql-mcp-codeql_query_run", map[string]any{"database": db, "queryName": "PrintAST", "queryLanguage": "actions", "queryPack": p.root}, "queryPack"},
		{"ql-mcp-codeql_query_run", map[string]any{"database": db, "query": query, "external": []any{"p=/etc/hosts"}}, "external"},
		{"ql-mcp-codeql_test_extract", map[string]any{"testPath": tests[0]}, "testPath"},
		// ql-mcp passes it on as --language, which `codeql test extract` rejects; a live session lost a
		// CodeQL call to it.
		{"ql-mcp-codeql_test_extract", map[string]any{"tests": tests, "language": "actions"}, "language"},
	} {
		v := b.Call(tc.tool, tc.args)
		if v.Allowed {
			t.Errorf("%s with %s allowed", tc.tool, tc.name)
		}
		if !strings.Contains(v.Reason, tc.name) {
			t.Errorf("%s refused without naming %s: %q", tc.tool, tc.name, v.Reason)
		}
	}
}

// ql-mcp advertises no argument schema for these tools, so the refusal is where the agent learns
// what a tool takes; the recording shows it guessing `testPath`, `dir` and `path`.
func TestARefusedArgumentIsAnsweredWithTheArgumentsATakes(t *testing.T) {
	b := newPlace(t).bound(t)
	v := b.Call("ql-mcp-codeql_test_extract", map[string]any{"testPath": "x"})
	if !strings.Contains(v.Reason, "tests") {
		t.Errorf("refusal does not say the tool takes tests: %q", v.Reason)
	}
	// A live session sent the files as a list; ql-mcp takes them as one string, so ABOM passes them
	// that way and tells the agent the form.
	v = b.Call("ql-mcp-codeql_query_run", map[string]any{"queryName": "PrintAST", "queryLanguage": "actions",
		"database": b.database(), "sourceFiles": []any{".github/workflows/checkout.yml", "action.yml"}})
	if !v.Allowed || !strings.Contains(v.Reason, "comma") {
		t.Errorf("a list of source files: %+v, want it passed on, saying they are separated by commas", v)
	}
}

// Accepting makes the query's own output its expectation, so a test could never fail; the .expected
// is written from the help and the fixtures before the query exists.
func TestAcceptingTheQuerysOutputIsRefused(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	args := map[string]any{"tests": []any{
		filepath.Join(p.ws, "test", "CheckoutTagPin", "CheckoutTagPin.qlref")}}

	if v := b.Call("ql-mcp-codeql_test_accept", args); v.Allowed || !strings.Contains(v.Reason, ".expected") {
		t.Errorf("test accept: %+v, want it refused saying to write the .expected", v)
	}
	raw := fmt.Sprintf(`{"kind":"hook","toolName":"ql-mcp-codeql_test_accept","toolArgs":{"tests":[%q]}}`,
		args["tests"].([]any)[0])
	if v := b.Request(request(t, json.RawMessage(raw))); v.Allowed {
		t.Errorf("hook request for test accept: %+v, want it refused", v)
	}
}

// The CLI and ABOM own these: a .qlref can point the test at another query, a .ql under test/ is
// itself a test query, and another test's .expected is not this request's to decide.
func TestTheFilesThatDecideWhatATestMeansAreNotWritable(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	dir := filepath.Join(p.ws, "test", "CheckoutTagPin")

	for _, name := range []string{
		"Other.expected", "CheckoutTagPin.actual", "CheckoutTagPin.qlref", "Other.qlref",
		".github/workflows/CheckoutTagPin.expected",
		".github/workflows/src/Scaffold/Scaffold.ql", "Helper.qll", "qlpack.yml",
		"codeql-pack.yml", "codeql-pack.lock.yml", "codeql-workspace.yml",
		"CheckoutTagPin.testproj/codeql-database.yml", "checkout.yml.tmp",
	} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if v := b.Request(&rpc.PermissionRequestWrite{FileName: path}); v.Allowed {
			t.Errorf("write of test/CheckoutTagPin/%s allowed", name)
		}
	}
	for _, name := range []string{".github/workflows/checkout.yml", "composite/action.yaml"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if v := b.Request(&rpc.PermissionRequestWrite{FileName: path}); !v.Allowed {
			t.Errorf("write of fixture test/CheckoutTagPin/%s refused: %s", name, v.Reason)
		}
	}
	if v := b.Request(&rpc.PermissionRequestWrite{FileName: filepath.Join(dir, "CheckoutTagPin.expected")}); !v.Allowed ||
		v.Step != StepExpected {
		t.Errorf("write of the target's .expected: %+v, want allowed toward %s", v, StepExpected)
	}
	for _, name := range []string{"CheckoutTagPin.md", "CheckoutTagPin.qhelp"} {
		path := filepath.Join(p.ws, "src", name)
		if v := b.Request(&rpc.PermissionRequestWrite{FileName: path}); !v.Allowed {
			t.Errorf("write of help src/%s refused: %s", name, v.Reason)
		}
	}
	if v := b.Request(&rpc.PermissionRequestWrite{FileName: filepath.Join(p.ws, "src",
		"SelfHostedRunner.ql")}); v.Allowed {
		t.Error("write of another query allowed")
	}
}

// A path names where a write goes only after every link in it is followed: a fixture directory
// holding a link to outside would otherwise pass every string comparison.
func TestALinkCannotCarryAWriteOutOfBounds(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	workflows := filepath.Join(p.ws, "test", "CheckoutTagPin", ".github", "workflows")
	outside := filepath.Join(p.root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"file.yml": filepath.Join(outside, "file.yml"),
		"dir":      outside,
		"gone.yml": filepath.Join(outside, "not-there.yml"),
	} {
		if err := os.Symlink(target, filepath.Join(workflows, name)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(workflows, "file.yml"),
		filepath.Join(workflows, "dir", "x.yml"),
		filepath.Join(workflows, "gone.yml"),
		filepath.Join(workflows, "..", "..", "..", "..", "codeql-workspace.yml"),
		p.ws + "/test/CheckoutTagPin/../SelfHostedRunner/.github/workflows/w.yml",
	} {
		if v := b.Request(&rpc.PermissionRequestWrite{FileName: path}); v.Allowed {
			t.Errorf("write through %s allowed", path)
		}
		if v := b.Call("create", map[string]any{"path": path, "file_text": "x"}); v.Allowed {
			t.Errorf("create of %s allowed", path)
		}
	}
	// Relative to the pack, which is where the session works.
	if v := b.Call("create", map[string]any{"path": "test/CheckoutTagPin/.github/workflows/w.yml",
		"file_text": "on: push"}); !v.Allowed {
		t.Errorf("relative fixture path refused: %s", v.Reason)
	}
}

func TestNothingIsWritableBeforeATargetIsDecided(t *testing.T) {
	p := newPlace(t)
	b, err := NewBounds(p.ws, []string{filepath.Join(p.codeql, "packages")})
	if err != nil {
		t.Fatal(err)
	}
	if v := b.Request(&rpc.PermissionRequestWrite{
		FileName: filepath.Join(p.ws, "src", "CheckoutTagPin.ql")}); v.Allowed {
		t.Error("write allowed with no target")
	}
	if v := b.Call("ql-mcp-codeql_test_run", map[string]any{
		"tests": []any{filepath.Join(p.ws, "test", "CheckoutTagPin")}}); v.Allowed {
		t.Error("test run allowed with no target")
	}
	if v := b.Request(&rpc.PermissionRequestRead{Path: filepath.Join(p.ws, "src")}); !v.Allowed {
		t.Errorf("reading the pack refused with no target: %s", v.Reason)
	}
}

func TestOnlyTheLoopsToolsAreOffered(t *testing.T) {
	b := newPlace(t).bound(t)
	for _, tool := range []string{"bash", "read_bash", "sql", "web_fetch", "task", "powershell",
		"github-mcp-server-create_pull_request", "ql-mcp-codeql_database_create", "anything"} {
		if v := b.Call(tool, map[string]any{}); v.Allowed {
			t.Errorf("%s: %+v, want refused", tool, v)
		}
	}
	for _, tool := range []string{ToolCatalog, ToolTarget} {
		if v := b.Call(tool, map[string]any{}); !v.Allowed {
			t.Errorf("ABOM's own %s refused: %s", tool, v.Reason)
		}
	}
}

func TestBindingRefusesATargetOutsideThePack(t *testing.T) {
	p := newPlace(t)
	b, err := NewBounds(p.ws, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, tests string }{
		{filepath.Join(p.root, "Evil.ql"), filepath.Join(p.ws, "test", "Evil")},
		{filepath.Join(p.ws, "src", "Evil.ql"), filepath.Join(p.root, "test", "Evil")},
		{filepath.Join(p.ws, "test", "Evil.ql"), filepath.Join(p.ws, "test", "Evil")},
		{filepath.Join(p.ws, "src", "Evil.qll"), filepath.Join(p.ws, "test", "Evil")},
		{filepath.Join(p.ws, "src", "Evil.ql"), filepath.Join(p.ws, "test")},
	} {
		if _, err := b.Bind(tc.query, tc.tests); err == nil {
			t.Errorf("bound %s tested in %s", tc.query, tc.tests)
		}
	}
}

// A pack configured around ABOM's own files must not hand them to the session: its home holds
// the session's credentials, and the database is what every screen reads.
func TestWhatABOMKeepsIsOutOfReachEvenInsideThePack(t *testing.T) {
	p := newPlace(t)
	home := filepath.Join(p.ws, "test", "CheckoutTagPin", "home")
	db := filepath.Join(p.ws, "abom.db")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	b, err := p.bound(t).Deny(home, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{home, filepath.Join(home, "config.json"), db} {
		if v := b.Request(&rpc.PermissionRequestRead{Path: path}); v.Allowed {
			t.Errorf("read of %s allowed", path)
		}
	}
	if v := b.Call("create", map[string]any{"path": filepath.Join(home, "w.yml")}); v.Allowed {
		t.Error("write into the session's home allowed")
	}
	if v := b.Call("ql-mcp-codeql_resolve_metadata", map[string]any{"query": db}); v.Allowed {
		t.Error("metadata read of the database allowed")
	}
}

// Arguments come from a model, so any shape can arrive; each one is refused, never a panic.
func TestAMalformedCallIsRefusedRatherThanCrashingTheGate(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	for _, tc := range []struct {
		tool string
		args any
	}{
		{"create", nil},
		{"create", "a string"},
		{"create", map[string]any{"path": 7}},
		{"view", map[string]any{"path": []any{"a", "b"}}},
		{"ql-mcp-codeql_test_run", map[string]any{"tests": []any{nil}}},
		{"ql-mcp-codeql_test_run", map[string]any{"tests": nil}},
		{"ql-mcp-codeql_query_run", map[string]any{"database": 1, "query": true}},
		{"ql-mcp-codeql_lsp_diagnostics", map[string]any{"ql_code": "x", "workspace_uri": "http://x"}},
		{"ql-mcp-codeql_test_run", map[string]any{"tests": []any{"\x00"}}},
	} {
		if v := b.Call(tc.tool, tc.args); v.Allowed {
			t.Errorf("%s %v: %+v, want refused", tc.tool, tc.args, v)
		}
	}
	if v := b.Request(nil); v.Allowed {
		t.Error("an empty request allowed")
	}
}

// The timeline names each call by the step it works toward, in the words the step marks use.
func TestStepsAreNamedAsTheProgressNamesThem(t *testing.T) {
	names := map[string]bool{authoring.TargetStep(store.QueryRequest{}).Step: true}
	for _, s := range authoring.Unstarted("") {
		names[s.Step] = true
	}
	for _, step := range []string{StepTarget, StepHelp, StepExpected, StepDatabase, StepAST,
		StepCompiles, StepTests} {
		if !names[step] {
			t.Errorf("step %q is not one authoring reports", step)
		}
	}
}

// ql-mcp documents a plain directory for workspace_uri and resolves it itself, and its language
// server answers where a library class is defined in one call, where a live session's search of
// the package cache timed out. The calls read only what a session reads, and pass through as sent.
func TestTheLanguageServerToolsTakeThePackAndReadWhatASessionReads(t *testing.T) {
	p := newPlace(t)
	lib := filepath.Join(p.codeql, "packages", "codeql", "actions-all", "0.6.2", "Ast.qll")
	if err := os.WriteFile(lib, []byte("class UsesStep extends Step { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := p.bound(t)
	unbound, err := NewBounds(p.ws, []string{filepath.Join(p.codeql, "packages")})
	if err != nil {
		t.Fatal(err)
	}
	at := func(file string, more map[string]any) map[string]any {
		args := map[string]any{"file_path": file, "line": 3, "character": 7}
		for k, v := range more {
			args[k] = v
		}
		return args
	}
	for _, tc := range []struct {
		name, tool string
		b          Bounds
		args       map[string]any
		want       bool
	}{
		{"the pack as a relative path", "codeql_lsp_diagnostics", b, map[string]any{"ql_code": "x", "workspace_uri": "."}, true},
		{"the pack as an absolute path", "codeql_lsp_diagnostics", b, map[string]any{"ql_code": "x", "workspace_uri": p.ws}, true},
		{"the pack as a file URI", "codeql_lsp_diagnostics", b, map[string]any{"ql_code": "x", "workspace_uri": "file://" + p.ws}, true},
		{"a workspace outside the pack", "codeql_lsp_diagnostics", b, map[string]any{"ql_code": "x", "workspace_uri": p.root}, false},
		{"a definition in the query", "codeql_lsp_definition", b, at("src/CheckoutTagPin.ql", map[string]any{"workspace_uri": "."}), true},
		{"a definition in a library", "codeql_lsp_definition", b, at(lib, nil), true},
		{"a definition before a target is chosen", "codeql_lsp_definition", unbound, at(lib, nil), true},
		{"references in a file outside", "codeql_lsp_references", b, at(filepath.Join(p.root, "x.qll"), nil), false},
		{"a library's definitions by name", "codeql_lsp_document_symbols", b, map[string]any{"file_path": lib, "names_only": true}, true},
		{"completions over unsaved text", "codeql_lsp_completion", b, at("src/CheckoutTagPin.ql", map[string]any{"file_content": "import actions\n"}), true},
		{"libraries resolved from a path it names", "codeql_lsp_definition", b, at(lib, map[string]any{"search_path": p.root}), false},
	} {
		v := tc.b.Call(Server+"-"+tc.tool, tc.args)
		if v.Allowed != tc.want {
			t.Errorf("%s: allowed %v, want %v (%s)", tc.name, v.Allowed, tc.want, v.Reason)
		}
		if v.Allowed && v.Args != nil {
			t.Errorf("%s: forwarded as %v, want as sent", tc.name, v.Args)
		}
	}
}

// A GPT model searches with rg and writes with apply_patch, the only tools it has for either; they
// keep to the bounds grep, create and edit keep to, and a patch is judged by every file it names.
func TestAModelsOwnSearchAndPatchToolsKeepToTheSameBounds(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	patch := func(body string) string { return "*** Begin Patch\n" + body + "\n*** End Patch\n" }
	for _, tc := range []struct {
		name, tool string
		args       any
		want       bool
	}{
		{"a search in the pack", "rg", map[string]any{"pattern": "uses", "paths": "src"}, true},
		{"a search outside it", "rg", map[string]any{"pattern": "uses", "paths": p.root}, false},
		// The CLI's search tools take one directory or a list, and every one in a list must be readable.
		{"a glob over two pack directories", "glob", map[string]any{"pattern": "**/*", "paths": []any{"src", "test/CheckoutTagPin"}}, true},
		{"a list reaching outside", "rg", map[string]any{"pattern": "uses", "paths": []any{"src", p.root}}, false},
		{"an update to the query", "apply_patch",
			map[string]any{"input": patch("*** Update File: src/CheckoutTagPin.ql\n@@\n-a\n+b")}, true},
		{"a new fixture, as a bare string", "apply_patch",
			patch("*** Add File: test/CheckoutTagPin/.github/workflows/w.yml\n+on: push"), true},
		{"the query and a fixture", "apply_patch", map[string]any{"input": patch(
			"*** Update File: src/CheckoutTagPin.ql\n@@\n-a\n+b\n*** Add File: test/CheckoutTagPin/.github/workflows/w.yml\n+on: push")}, true},
		{"one file it may not write", "apply_patch", map[string]any{"input": patch(
			"*** Update File: src/CheckoutTagPin.ql\n@@\n-a\n+b\n*** Update File: test/CheckoutTagPin/CheckoutTagPin.qlref\n@@\n-a\n+b")}, false},
		{"a delete", "apply_patch",
			map[string]any{"input": patch("*** Delete File: test/CheckoutTagPin/.github/workflows/w.yml")}, false},
		{"a move", "apply_patch",
			map[string]any{"input": patch("*** Update File: src/CheckoutTagPin.ql\n*** Move to: src/Other.ql\n@@\n-a\n+b")}, false},
		{"a patch naming no file", "apply_patch", map[string]any{"input": patch("")}, false},
		{"no patch at all", "apply_patch", map[string]any{}, false},
	} {
		if got := b.Call(tc.tool, tc.args); got.Allowed != tc.want {
			t.Errorf("%s: allowed %v (%s), want %v", tc.name, got.Allowed, got.Reason, tc.want)
		}
	}
}

// A ql-mcp call whose meaning is plain but whose form the server would not take is corrected and
// allowed, with the agent told how, rather than refused: each of these cost a live session a refused
// call and a retry. What a correction produces is itself allowed as it stands.
func TestAPlainQlmcpCallIsCorrectedRatherThanRefused(t *testing.T) {
	p := newPlace(t)
	b := p.bound(t)
	db, tests, query := b.database(), filepath.Join(p.ws, "test", "CheckoutTagPin"), b.query
	printAST := func(extra map[string]any) map[string]any {
		out := map[string]any{"database": db, "queryName": "PrintAST", "queryLanguage": "actions",
			"sourceFiles": ".github/workflows/checkout.yml"}
		for k, v := range extra {
			if v == nil {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}
	for _, tc := range []struct {
		name, tool string
		args, want map[string]any
	}{
		{"the dataset folder inside the test database", "codeql_query_run",
			printAST(map[string]any{"database": filepath.Join(db, "db-actions")}), printAST(nil)},
		{"the test directory holding the test database", "codeql_query_run",
			printAST(map[string]any{"database": "test/CheckoutTagPin"}), printAST(nil)},
		{"fixture files as a list", "codeql_query_run",
			printAST(map[string]any{"sourceFiles": []any{".github/workflows/a.yml", "action.yml"}}),
			printAST(map[string]any{"sourceFiles": ".github/workflows/a.yml,action.yml"})},
		{"a tool query with no language", "codeql_query_run",
			printAST(map[string]any{"queryLanguage": nil}), printAST(nil)},
		{"a compile naming no query", "codeql_query_compile",
			map[string]any{}, map[string]any{"query": query}},
		{"tests named by one path", "codeql_test_run",
			map[string]any{"tests": tests}, map[string]any{"tests": []any{tests}}},
	} {
		v := b.Call(Server+"-"+tc.tool, tc.args)
		if !v.Allowed || !reflect.DeepEqual(v.Args, tc.want) || v.Reason == "" {
			t.Errorf("%s: allowed %v, args %v (%s); want %v with what was corrected", tc.name, v.Allowed, v.Args, v.Reason, tc.want)
			continue
		}
		if again := b.Call(Server+"-"+tc.tool, v.Args); !again.Allowed || again.Args != nil {
			t.Errorf("%s: the corrected call is %+v, want it allowed as it stands", tc.name, again)
		}
		mcp := &rpc.PermissionRequestMCP{ServerName: Server, ToolName: Server + "-" + tc.tool, Args: tc.args}
		if got := b.Request(mcp); !got.Allowed {
			t.Errorf("%s: the permission request that follows is refused: %s", tc.name, got.Reason)
		}
	}
	for name, args := range map[string]map[string]any{
		"another test's database":          printAST(map[string]any{"database": filepath.Join(p.ws, "test", "SelfHostedRunner")}),
		"neither a query nor a tool query": {"database": db},
	} {
		if v := b.Call(Server+"-codeql_query_run", args); v.Allowed {
			t.Errorf("%s was allowed: %+v", name, v)
		}
	}
	// Which database a run reads is never assumed, and running the query against its test database
	// is what the test tools are for, so the refusal says both. A null or blank database names none
	// either, and gets the same answer rather than a complaint about its type.
	for name, database := range map[string]any{"absent": nil, "null": "null", "empty": "", "blank": "  "} {
		args := printAST(map[string]any{"database": nil})
		if name != "absent" {
			args["database"] = database
			if database == "null" {
				args["database"] = nil
			}
		}
		v := b.Call(Server+"-codeql_query_run", args)
		if v.Allowed || v.Args != nil || !strings.Contains(v.Reason, "names no database") ||
			!strings.Contains(v.Reason, ToolTest) || !strings.Contains(v.Reason, "codeql_test_run") {
			t.Errorf("a run whose database is %s is %+v, want it refused, pointing at the test tools", name, v)
		}
	}
}
