package toolchain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

type place struct {
	dir   string
	store *store.Store
}

func newPlace(t *testing.T) place {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return place{dir: dir, store: st}
}

// script writes an executable that prints out, or one that never answers when out is "".
func (p place) script(t *testing.T, name, out string) string {
	t.Helper()
	return p.at(t, ".", name, out)
}

// at writes the script into its own directory, so several can carry a tool's own name.
func (p place) at(t *testing.T, sub, name, out string) string {
	t.Helper()
	body := "#!/bin/sh\nprintf '%s\\n' '" + out + "'\n"
	if out == "" {
		body = "#!/bin/sh\nsleep 5\n"
	}
	dir := filepath.Join(p.dir, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func (p place) pack(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(p.dir, "pack")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qlpack.yml"), []byte("name: acme/q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (p place) chain(t *testing.T) *Toolchain {
	t.Helper()
	return &Toolchain{
		Store:   p.store,
		Pack:    p.pack(t),
		CodeQL:  Tool{Path: p.script(t, "codeql", `{"semanticVersion":"2.27.1"}`), Source: "--codeql"},
		Copilot: Tool{Path: p.script(t, "copilot", "1.0.4"), Source: "COPILOT_CLI_PATH"},
		QLMCP:   []string{"sh", "-c", "true"},
		Token:   func(context.Context) error { return nil },
		Timeout: 500 * time.Millisecond,
	}
}

func byKey(checks []Check) map[string]Check {
	m := map[string]Check{}
	for _, c := range checks {
		m[c.Key] = c
	}
	return m
}

func TestEverythingTheLoopNeedsIsReportedWithItsVersion(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	got := byKey(tc.Checks(context.Background(), false))
	for _, key := range []string{KeyPack, KeyCodeQL, KeyCopilot, KeyQLMCP, KeyToken} {
		c, ok := got[key]
		if !ok {
			t.Fatalf("no %s check in %+v", key, got)
		}
		if c.State != Ready {
			t.Errorf("%s is %s: %s", key, c.State, c.Reason)
		}
	}
	if got[KeyCodeQL].Version != "2.27.1" || got[KeyCopilot].Version != "1.0.4" {
		t.Errorf("versions read %q and %q", got[KeyCodeQL].Version, got[KeyCopilot].Version)
	}
	if got[KeyCopilot].Source != "COPILOT_CLI_PATH" || !got[KeyCopilot].Settable || got[KeyPack].Settable {
		t.Errorf("source or settability wrong: %+v / %+v", got[KeyCopilot], got[KeyPack])
	}
}

func TestEachMissingDependencySaysWhyAndWhatToDo(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	tc.Pack = ""
	tc.Copilot = Tool{}
	tc.CodeQL = Tool{Path: filepath.Join(p.dir, "absent", "codeql"), Source: "--codeql"}
	tc.QLMCP = []string{"no-such-launcher-abom", "-y", "x"}
	tc.Token = func(context.Context) error { return errors.New("not logged in") }

	got := byKey(tc.Checks(context.Background(), false))
	for _, key := range []string{KeyPack, KeyCodeQL, KeyCopilot, KeyQLMCP, KeyToken} {
		c := got[key]
		if c.State != Problem || c.Reason == "" || c.Remedy == "" {
			t.Errorf("%s: %+v, want a problem with a reason and a remedy", key, c)
		}
	}
	if !strings.Contains(got[KeyQLMCP].Reason, "no-such-launcher-abom") {
		t.Errorf("the ql-mcp problem does not name the launcher: %q", got[KeyQLMCP].Reason)
	}
}

func TestACLIThatNeverAnswersIsAProblemNotAHang(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	tc.Copilot = Tool{Path: p.script(t, "shim", ""), Source: "--copilot-cli"}
	start := time.Now()
	c := byKey(tc.Checks(context.Background(), false))[KeyCopilot]
	if c.State != Problem || !strings.Contains(c.Reason, "--version") {
		t.Errorf("a CLI that waits for input read as %+v", c)
	}
	if time.Since(start) > 4*time.Second {
		t.Errorf("checking waited %v for a CLI that never answers", time.Since(start))
	}
}

func TestACopilotPathSetWhileServingIsCheckedKeptAndApplied(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	var applied []string
	tc.ApplyCopilot = func(path string) { applied = append(applied, path) }
	ctx := context.Background()

	notExecutable := filepath.Join(p.dir, "plain", "copilot")
	if err := os.MkdirAll(filepath.Dir(notExecutable), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(p.dir, "folder", "copilot")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"relative":       "bin/copilot",
		"missing":        filepath.Join(p.dir, "absent", "copilot"),
		"directory":      directory,
		"not executable": notExecutable,
		"never answers":  p.at(t, "shim", "copilot", ""),
	} {
		if err := tc.SetTool(ctx, store.ToolCopilot, path, "alice"); !errors.Is(err, ErrUnusable) {
			t.Errorf("%s path %s: %v, want ErrUnusable", name, path, err)
		}
	}
	if saved, _ := p.store.ToolPaths(); len(saved) != 0 || len(applied) != 0 {
		t.Fatalf("a refused path was kept %v or applied %v", saved, applied)
	}

	chosen := p.at(t, "v2", "copilot", "2.0.0")
	if err := tc.SetTool(ctx, store.ToolCopilot, chosen, "alice"); err != nil {
		t.Fatal(err)
	}
	c := byKey(tc.Checks(ctx, false))[KeyCopilot]
	if c.Path != chosen || c.Source != SourceSettings || c.Version != "2.0.0" || c.Saved != "" {
		t.Errorf("after setting, the check reads %+v", c)
	}
	if len(applied) != 1 || applied[0] != chosen {
		t.Errorf("applied %v, want the chosen path once", applied)
	}

	if err := tc.SetTool(ctx, store.ToolCopilot, "", "alice"); err != nil {
		t.Fatal(err)
	}
	c = byKey(tc.Checks(ctx, false))[KeyCopilot]
	if c.Source != "COPILOT_CLI_PATH" || applied[len(applied)-1] != c.Path {
		t.Errorf("clearing returned to %+v, applied %v", c, applied)
	}
}

func TestACodeQLPathSetWhileServingWaitsForTheNextStart(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	inUse := tc.CodeQL.Path
	chosen := p.at(t, "next", "codeql", `{"semanticVersion":"2.28.0"}`)
	if err := tc.SetTool(context.Background(), store.ToolCodeQL, chosen, "alice"); err != nil {
		t.Fatal(err)
	}
	c := byKey(tc.Checks(context.Background(), false))[KeyCodeQL]
	if c.Path != inUse || c.Saved != chosen || !strings.Contains(c.Remedy, "restart") {
		t.Errorf("a CodeQL set while serving reads %+v", c)
	}
	if err := tc.SetTool(context.Background(), store.ToolCodeQL, p.at(t, "liar", "codeql", "not json"), "alice"); !errors.Is(err, ErrUnusable) {
		t.Errorf("a program that is not CodeQL was accepted: %v", err)
	}
}

// Settings is a page anyone who reaches the server can submit. A path it names is run by the server
// as itself, so the page may only choose which copy of the tool runs, never what program runs.
func TestSettingsCanNameOnlyAFileCalledAfterItsTool(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	var applied []string
	tc.ApplyCopilot = func(path string) { applied = append(applied, path) }
	ctx := context.Background()

	// Each of these answers --version as a Copilot CLI would, so only the rule can refuse it.
	another := p.at(t, "other", "helper", "1.0.0")
	p.at(t, "v3", "copilot", "1.0.0")
	p.at(t, "v2", "copilot", "1.0.0")
	for name, path := range map[string]string{
		"another program":         another,
		"a name that ends in it":  p.at(t, "suffix", "not-copilot", "1.0.0"),
		"the other tool's name":   p.at(t, "swapped", "codeql", "1.0.0"),
		"a path through a parent": p.dir + "/v2/../v3/copilot",
	} {
		if err := tc.SetTool(ctx, store.ToolCopilot, path, "alice"); !errors.Is(err, ErrUnusable) {
			t.Errorf("%s (%s): %v, want ErrUnusable", name, path, err)
		}
	}
	if saved, _ := p.store.ToolPaths(); len(saved) != 0 || len(applied) != 0 {
		t.Fatalf("a refused path was kept %v or applied %v", saved, applied)
	}
	if err := tc.SetTool(ctx, store.ToolCodeQL, another, "alice"); !errors.Is(err, ErrUnusable) {
		t.Errorf("CodeQL pointed at another program: %v, want ErrUnusable", err)
	}
}

// Another account that can change the file, or the directory it is named in, could change what the
// server runs as itself, after it was checked.
func TestSettingsRefusesAToolAnotherAccountCouldReplace(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	ctx := context.Background()

	openDir := p.at(t, "shared", "copilot", "1.0.0")
	if err := os.Chmod(filepath.Dir(openDir), 0o777); err != nil {
		t.Fatal(err)
	}
	openFile := p.at(t, "loose", "copilot", "1.0.0")
	if err := os.Chmod(openFile, 0o777); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(p.dir, "linked", "copilot")
	if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(openFile, linked); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"in a directory anyone can write":   openDir,
		"a file anyone can write":           openFile,
		"a link to a file anyone can write": linked,
	} {
		err := tc.SetTool(ctx, store.ToolCopilot, path, "alice")
		if !errors.Is(err, ErrUnusable) || !strings.Contains(err.Error(), "anyone can write") {
			t.Errorf("%s: %v, want it refused because anyone can write it", name, err)
		}
	}
	if saved, _ := p.store.ToolPaths(); len(saved) != 0 {
		t.Fatalf("a path another account could replace was kept: %v", saved)
	}
}

func TestLoadPutsASavedPathAheadOfFlagsAndEnvironment(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	saved := p.script(t, "copilot-saved", "3.0.0")
	if err := p.store.SetToolPath(store.ToolCopilot, saved, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := tc.Load(); err != nil {
		t.Fatal(err)
	}
	if tc.Copilot.Path != saved || tc.Copilot.Source != SourceSettings {
		t.Errorf("after loading, Copilot is %+v", tc.Copilot)
	}
	if tc.CodeQL.Source != "--codeql" {
		t.Errorf("a tool nobody set in Settings changed: %+v", tc.CodeQL)
	}
}

// A Demo fixes its tools at startup and reads its credential from its own setup, so readiness must
// neither offer a path it refuses to save nor send the user to gh, which a Demo never asks.
func TestAFixedToolchainOffersNoPathAndDescribesItsOwnCredential(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	tc.Fixed = true
	tc.Credential = Credential{Name: "Copilot credential", Source: "Demo setup",
		Remedy: "run copilot-login.sh in the Codespace terminal"}
	tc.Token = func(context.Context) error { return errors.New("no Copilot credential has been supplied") }
	got := byKey(tc.Checks(context.Background(), false))
	if got[KeyCodeQL].Settable || got[KeyCopilot].Settable {
		t.Errorf("a fixed toolchain offers a path: %+v / %+v", got[KeyCodeQL], got[KeyCopilot])
	}
	token := got[KeyToken]
	if token.Name != "Copilot credential" || token.Source != "Demo setup" ||
		strings.Contains(token.Reason+token.Remedy, "gh") || !strings.Contains(token.Remedy, "copilot-login.sh") {
		t.Errorf("the credential check reads %+v", token)
	}
	if err := tc.SetTool(context.Background(), "copilot", got[KeyCopilot].Path, "test"); err == nil {
		t.Error("a fixed toolchain saved a tool path")
	}
}
