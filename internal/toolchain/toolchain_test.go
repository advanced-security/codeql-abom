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
	body := "#!/bin/sh\nprintf '%s\\n' '" + out + "'\n"
	if out == "" {
		body = "#!/bin/sh\nsleep 5\n"
	}
	path := filepath.Join(p.dir, name)
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

	notExecutable := filepath.Join(p.dir, "plain")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"relative":       "bin/copilot",
		"missing":        filepath.Join(p.dir, "absent"),
		"directory":      p.dir,
		"not executable": notExecutable,
		"never answers":  p.script(t, "shim", ""),
	} {
		if err := tc.SetTool(ctx, store.ToolCopilot, path, "alice"); !errors.Is(err, ErrUnusable) {
			t.Errorf("%s path %s: %v, want ErrUnusable", name, path, err)
		}
	}
	if saved, _ := p.store.ToolPaths(); len(saved) != 0 || len(applied) != 0 {
		t.Fatalf("a refused path was kept %v or applied %v", saved, applied)
	}

	chosen := p.script(t, "copilot-2", "2.0.0")
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
	chosen := p.script(t, "codeql-next", `{"semanticVersion":"2.28.0"}`)
	if err := tc.SetTool(context.Background(), store.ToolCodeQL, chosen, "alice"); err != nil {
		t.Fatal(err)
	}
	c := byKey(tc.Checks(context.Background(), false))[KeyCodeQL]
	if c.Path != inUse || c.Saved != chosen || !strings.Contains(c.Remedy, "restart") {
		t.Errorf("a CodeQL set while serving reads %+v", c)
	}
	if err := tc.SetTool(context.Background(), store.ToolCodeQL, p.script(t, "liar", "not json"), "alice"); !errors.Is(err, ErrUnusable) {
		t.Errorf("a program that is not CodeQL was accepted: %v", err)
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
