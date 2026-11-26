package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// A tool that answers is given long enough to be scheduled on a loaded machine; only a check that a
// silent tool is a problem rather than a hang needs the short bound, and sets it itself.
const (
	answers = 10 * time.Second
	hangs   = 500 * time.Millisecond
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
		Timeout: answers,
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
	tc.Timeout = hangs
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
	tc.Timeout = hangs
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

	tc.Timeout = answers
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
	if _, err := tc.Load(); err != nil {
		t.Fatal(err)
	}
	if tc.Copilot.Path != saved || tc.Copilot.Source != SourceSettings {
		t.Errorf("after loading, Copilot is %+v", tc.Copilot)
	}
	if tc.CodeQL.Source != "--codeql" {
		t.Errorf("a tool nobody set in Settings changed: %+v", tc.CodeQL)
	}
}

// An editor that updates its Copilot extension moves the CLI it installed, so a path saved in
// Settings can stop existing while the flag or environment still names one that runs. Nobody has
// to repair that: the saved path is forgotten, the default is used, and the server says so.
func TestASavedPathThatNoLongerExistsGivesWayToTheDefault(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	fallback := tc.Copilot
	gone := filepath.Join(p.dir, "gone", "copilot")
	if err := p.store.SetToolPath(store.ToolCopilot, gone, "alice"); err != nil {
		t.Fatal(err)
	}
	said, err := tc.Load()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Copilot != fallback {
		t.Errorf("after loading a path that is gone, Copilot is %+v, want %+v", tc.Copilot, fallback)
	}
	if saved, _ := p.store.ToolPaths(); saved[store.ToolCopilot].Path != "" {
		t.Errorf("the path that is gone is still saved: %+v", saved[store.ToolCopilot])
	}
	if len(said) != 1 || !strings.Contains(said[0], gone) || !strings.Contains(said[0], fallback.Path) {
		t.Errorf("loading said %q, want the path that is gone and the one used instead", said)
	}
	if c := byKey(tc.Checks(context.Background(), true))[KeyCopilot]; c.State != Ready || c.Saved != "" {
		t.Errorf("readiness after the fallback is %+v, want ready with nothing pending", c)
	}
}

// With no Copilot CLI named anywhere, the one VS Code installed is used rather than offered as advice.
func TestWithNoCopilotCLINamedTheOneVSCodeInstalledIsUsed(t *testing.T) {
	p := newPlace(t)
	home := filepath.Join(p.dir, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	installed := p.at(t, filepath.Join(strings.TrimPrefix(config, p.dir+string(filepath.Separator)),
		"Code", "User", "globalStorage", "github.copilot-chat", "copilotCli"), "copilot", "1.0.88")
	tc := p.chain(t)
	tc.Copilot = Tool{}

	said, err := tc.Load()
	if err != nil {
		t.Fatal(err)
	}
	if tc.Copilot.Path != installed || tc.Copilot.Source != SourceVSCode {
		t.Errorf("with nothing named, Copilot is %+v, want %s from VS Code", tc.Copilot, installed)
	}
	if len(said) != 1 || !strings.Contains(said[0], installed) {
		t.Errorf("loading said %q, want the CLI it chose", said)
	}
	if c := byKey(tc.Checks(context.Background(), true))[KeyCopilot]; c.State != Ready {
		t.Errorf("readiness with VS Code's CLI is %+v", c)
	}
}

// The CLI VS Code installed runs as the server, so it is held to what a path the operator names is
// held to: nothing another account could change. It is found in a directory the environment names,
// and a link there is followed only as far as that directory goes.
func TestVSCodesCLIIsUsedOnlyWhereNoOtherAccountCouldChangeIt(t *testing.T) {
	link := func(t *testing.T, cli, target string) {
		t.Helper()
		if err := os.Remove(cli); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, cli); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		arrange func(t *testing.T, p place, cli string)
		used    bool
	}{
		"as installed": {func(*testing.T, place, string) {}, true},
		"in a directory anyone can write": {func(t *testing.T, _ place, cli string) {
			if err := os.Chmod(filepath.Dir(cli), 0o777); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a file anyone can write": {func(t *testing.T, _ place, cli string) {
			if err := os.Chmod(cli, 0o777); err != nil {
				t.Fatal(err)
			}
		}, false},
		"a link to a file beside it": {func(t *testing.T, _ place, cli string) {
			if err := os.WriteFile(filepath.Join(filepath.Dir(cli), "copilot-1.0.88"),
				[]byte("#!/bin/sh\nprintf '1.0.88\\n'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			link(t, cli, "copilot-1.0.88")
		}, true},
		"a link to a file anyone can write": {func(t *testing.T, _ place, cli string) {
			loose := filepath.Join(filepath.Dir(cli), "copilot-1.0.88")
			if err := os.WriteFile(loose, []byte("#!/bin/sh\nprintf '1.0.88\\n'\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(loose, 0o777); err != nil {
				t.Fatal(err)
			}
			link(t, cli, "copilot-1.0.88")
		}, false},
		"a link out of the configuration directory": {func(t *testing.T, p place, cli string) {
			elsewhere := p.at(t, "elsewhere", "copilot", "1.0.88")
			rel, err := filepath.Rel(filepath.Dir(cli), elsewhere)
			if err != nil {
				t.Fatal(err)
			}
			link(t, cli, rel)
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			p := newPlace(t)
			home := filepath.Join(p.dir, "home")
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			config, err := os.UserConfigDir()
			if err != nil {
				t.Fatal(err)
			}
			cli := p.at(t, filepath.Join(strings.TrimPrefix(config, p.dir+string(filepath.Separator)),
				"Code", "User", "globalStorage", "github.copilot-chat", "copilotCli"), "copilot", "1.0.88")
			c.arrange(t, p, cli)
			tc := p.chain(t)
			tc.Copilot = Tool{}

			if _, err := tc.Load(); err != nil {
				t.Fatal(err)
			}
			if used := tc.Copilot.Source == SourceVSCode; used != c.used {
				t.Errorf("VS Code's CLI used: %v, want %v (Copilot is %+v)", used, c.used, tc.Copilot)
			}
		})
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

// Readiness says whether sessions will authenticate with an installation token or with gh, and
// why one cannot be had, without ever printing the token itself.
func TestReadinessNamesTheCredentialSessionsUse(t *testing.T) {
	p := newPlace(t)
	tc := p.chain(t)
	if c := byKey(tc.Checks(context.Background(), false))[KeyToken]; c.Credential != CredentialGH || c.State != Ready {
		t.Errorf("without an App the credential check reads %+v; want gh, ready", c)
	}

	tc.Credential = Credential{Kind: CredentialInstallation, Name: "Copilot credential",
		Source: "GitHub App installation token for has-ghas", Remedy: "an owner of has-ghas approves Copilot requests"}
	tc.Token = func(context.Context) error {
		return errors.New("the App's installation on has-ghas has not granted organization_copilot_requests: read")
	}
	c := byKey(tc.Checks(context.Background(), false))[KeyToken]
	if c.Credential != CredentialInstallation || c.State != Problem ||
		!strings.Contains(c.Reason, "organization_copilot_requests") || !strings.Contains(c.Source, "has-ghas") {
		t.Errorf("with an App the credential check reads %+v; want the installation token and the missing grant", c)
	}
	raw, _ := json.Marshal(c)
	if !strings.Contains(string(raw), `"credential":"installation"`) {
		t.Errorf("the check's JSON %s does not name the credential", raw)
	}
}
