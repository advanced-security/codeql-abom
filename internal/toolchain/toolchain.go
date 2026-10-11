// Package toolchain reports whether this server has what an authoring session needs, and lets an
// operator name the CodeQL and Copilot CLIs from the interface.
package toolchain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Check keys, one per thing a session needs.
const (
	KeyPack    = "pack"
	KeyCodeQL  = store.ToolCodeQL
	KeyCopilot = store.ToolCopilot
	KeyQLMCP   = "qlmcp"
	KeyToken   = "token"
)

// Check states.
const (
	Ready   = "ready"
	Problem = "problem"
)

// SourceSettings names a path an operator set in the interface.
const SourceSettings = "Settings"

// DefaultTimeout bounds a version probe. A VS Code shim answers no flag and waits for input.
const DefaultTimeout = 15 * time.Second

// ErrUnusable refuses a path no session could run.
var ErrUnusable = errors.New("not a usable executable")

// toolFile is the one file name Settings may point each tool at, in an absolute path. Settings
// chooses which copy of a tool the server runs as itself, never which program.
var toolFile = map[string]*regexp.Regexp{
	store.ToolCodeQL:  regexp.MustCompile(`^(?:/[^/]+)*/codeql$`),
	store.ToolCopilot: regexp.MustCompile(`^(?:/[^/]+)*/copilot$`),
}

// Check is one thing a session needs, and whether this server has it.
type Check struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	State   string `json:"state"`
	Path    string `json:"path"`
	Source  string `json:"source"`
	Version string `json:"version"`
	Reason  string `json:"reason"`
	Remedy  string `json:"remedy"`
	// Saved is a path set in Settings that this process is not running yet.
	Saved    string `json:"saved"`
	Settable bool   `json:"settable"`
}

// Tool is the executable a tool runs and where that choice came from.
type Tool struct {
	Path   string
	Source string
}

// Toolchain checks and changes what sessions run on.
type Toolchain struct {
	Store store.Backend
	// Pack is the operator's query pack; empty means none was configured.
	Pack string
	// CodeQL with an empty Path runs codeql from PATH.
	CodeQL  Tool
	Copilot Tool
	QLMCP   []string
	Token   func(context.Context) error
	// ApplyCopilot hands a newly chosen Copilot CLI to the sessions; nil leaves it for the next start.
	ApplyCopilot func(path string)
	// Timeout bounds each version probe; zero takes DefaultTimeout.
	Timeout time.Duration

	mu       sync.Mutex
	defaults map[string]Tool
	versions map[string]probed
}

type probed struct {
	stamp   string
	version string
	err     error
}

// Load puts a path saved in Settings ahead of the flag or environment that chose the default.
func (t *Toolchain) Load() error {
	saved, err := t.Store.ToolPaths()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rememberDefaults()
	if p, ok := saved[store.ToolCodeQL]; ok {
		t.CodeQL = Tool{Path: p.Path, Source: SourceSettings}
	}
	if p, ok := saved[store.ToolCopilot]; ok {
		t.Copilot = Tool{Path: p.Path, Source: SourceSettings}
	}
	return nil
}

func (t *Toolchain) rememberDefaults() {
	if t.defaults == nil {
		t.defaults = map[string]Tool{store.ToolCodeQL: t.CodeQL, store.ToolCopilot: t.Copilot}
	}
}

// InUse reports what each tool runs now.
func (t *Toolchain) InUse() (codeql, copilot Tool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.CodeQL, t.Copilot
}

// Checks reports every dependency of a session. fresh probes versions again even where the
// executable has not changed.
func (t *Toolchain) Checks(ctx context.Context, fresh bool) []Check {
	saved, err := t.Store.ToolPaths()
	if err != nil {
		saved = map[string]store.ToolPath{}
	}
	codeql, copilot := t.InUse()
	jobs := []func() Check{
		t.packCheck,
		func() Check { return t.codeqlCheck(ctx, codeql, saved, fresh) },
		func() Check { return t.copilotCheck(ctx, copilot, saved, fresh) },
		t.qlmcpCheck,
		func() Check { return t.tokenCheck(ctx) },
	}
	checks := make([]Check, len(jobs))
	var wg sync.WaitGroup
	for i, job := range jobs {
		wg.Go(func() { checks[i] = job() })
	}
	wg.Wait()
	return checks
}

func (t *Toolchain) packCheck() Check {
	c := Check{Key: KeyPack, Name: "Query pack", Path: t.Pack, Source: "--custom-ql-dir"}
	remedy := "start the server with --custom-ql-dir <directory>; make start passes .abom/ql/custom"
	if t.Pack == "" {
		return c.problem("no query pack is configured, so a session has nowhere to write a query", remedy)
	}
	info, err := os.Stat(t.Pack)
	if err != nil || !info.IsDir() {
		return c.problem(t.Pack+" is not a directory", remedy)
	}
	if !exists(filepath.Join(t.Pack, "qlpack.yml")) && !exists(filepath.Join(t.Pack, "codeql-pack.yml")) {
		return c.problem(t.Pack+" holds no qlpack.yml", "restart the server; it scaffolds an empty pack")
	}
	probe, err := os.CreateTemp(t.Pack, ".abom-probe-*")
	if err != nil {
		return c.problem("a session cannot write in "+t.Pack+": "+err.Error(),
			"make the directory writable by the user running the server")
	}
	_ = probe.Close()
	_ = os.Remove(probe.Name())
	return c.ready()
}

func (t *Toolchain) codeqlCheck(ctx context.Context, tool Tool, saved map[string]store.ToolPath, fresh bool) Check {
	c := Check{Key: KeyCodeQL, Name: "CodeQL CLI", Path: tool.Path, Source: tool.Source, Settable: true}
	if p, ok := saved[store.ToolCodeQL]; ok && p.Path != tool.Path {
		c.Saved = p.Path
	}
	remedy := "set its path in Settings, or start the server with --codeql or with codeql on PATH"
	path := tool.Path
	if path == "" {
		c.Source = "PATH"
		found, err := exec.LookPath("codeql")
		if err != nil {
			return c.withSaved().problem("codeql is not on this server's PATH", remedy)
		}
		path, c.Path = found, found
	}
	version, err := t.version(ctx, path, []string{"version", "--format=json"}, codeqlVersion, fresh)
	if err != nil {
		return c.withSaved().problem(err.Error(), remedy)
	}
	c.Version = version
	return c.withSaved().ready()
}

func (t *Toolchain) copilotCheck(ctx context.Context, tool Tool, saved map[string]store.ToolPath, fresh bool) Check {
	c := Check{Key: KeyCopilot, Name: "Copilot CLI", Path: tool.Path, Source: tool.Source, Settable: true}
	if p, ok := saved[store.ToolCopilot]; ok && p.Path != tool.Path {
		c.Saved = p.Path
	}
	remedy := "set its path in Settings, or start the server with --copilot-cli or COPILOT_CLI_PATH"
	if found := vscodeCopilot(); found != "" && found != tool.Path {
		remedy += "; VS Code installed one at " + found
	}
	if tool.Path == "" {
		return c.withSaved().problem("no Copilot CLI is named, so no session can start", remedy)
	}
	version, err := t.version(ctx, tool.Path, []string{"--version"}, firstLine, fresh)
	if err != nil {
		return c.withSaved().problem(err.Error(), remedy)
	}
	c.Version = version
	return c.withSaved().ready()
}

func (t *Toolchain) qlmcpCheck() Check {
	c := Check{Key: KeyQLMCP, Name: "ql-mcp", Source: "--ql-mcp", Path: strings.Join(t.QLMCP, " ")}
	if len(t.QLMCP) == 0 {
		return c.problem("no command starts ql-mcp", "start the server with --ql-mcp")
	}
	if _, err := exec.LookPath(t.QLMCP[0]); err != nil {
		return c.problem(t.QLMCP[0]+" is not on this server's PATH, so ql-mcp cannot start",
			"install Node.js, which provides npx, or point --ql-mcp at a launcher that exists")
	}
	for _, arg := range t.QLMCP[1:] {
		if at := strings.LastIndex(arg, "@"); at > 0 {
			c.Version = arg[at+1:]
		}
	}
	return c.ready()
}

func (t *Toolchain) tokenCheck(ctx context.Context) Check {
	c := Check{Key: KeyToken, Name: "GitHub sign-in", Source: "gh auth token"}
	if t.Token == nil {
		return c.problem("this server has no way to read a GitHub token", "run gh auth login")
	}
	if err := t.Token(ctx); err != nil {
		return c.problem("gh gave no token: "+err.Error(), "run gh auth login as the user running the server")
	}
	return c.ready()
}

// SetTool validates and keeps the executable a tool runs; an empty path returns it to its default.
// A Copilot CLI takes effect for the next session; a CodeQL CLI at the next start, because the
// crawl's runner holds it.
func (t *Toolchain) SetTool(ctx context.Context, tool, path, by string) error {
	if tool != store.ToolCodeQL && tool != store.ToolCopilot {
		return fmt.Errorf("%w: no tool %q", ErrUnusable, tool)
	}
	t.mu.Lock()
	t.rememberDefaults()
	fallback := t.defaults[tool]
	t.mu.Unlock()
	if path != "" {
		if !toolFile[tool].MatchString(path) {
			return fmt.Errorf("%w: %s is not an absolute path to a file named %s; name any other program "+
				"with the server's flag or environment variable instead", ErrUnusable, path, tool)
		}
		if clean := filepath.Clean(path); clean != path {
			return fmt.Errorf("%w: name %s as %s", ErrUnusable, path, clean)
		}
		if err := t.usable(ctx, tool, path); err != nil {
			return err
		}
	}
	if err := t.Store.SetToolPath(tool, path, by); err != nil {
		return err
	}
	if tool != store.ToolCopilot || t.ApplyCopilot == nil {
		return nil
	}
	next := fallback
	if path != "" {
		next = Tool{Path: path, Source: SourceSettings}
	}
	t.mu.Lock()
	t.Copilot = next
	t.mu.Unlock()
	t.ApplyCopilot(next.Path)
	return nil
}

func (t *Toolchain) usable(ctx context.Context, tool, path string) error {
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %s does not exist", ErrUnusable, path)
	case info.IsDir():
		return fmt.Errorf("%w: %s is a directory", ErrUnusable, path)
	case info.Mode()&0o111 == 0:
		return fmt.Errorf("%w: %s is not executable", ErrUnusable, path)
	}
	if err := ownedHere(path); err != nil {
		return err
	}
	args, parse := []string{"--version"}, firstLine
	if tool == store.ToolCodeQL {
		args, parse = []string{"version", "--format=json"}, codeqlVersion
	}
	if _, err := t.version(ctx, path, args, parse, true); err != nil {
		return fmt.Errorf("%w: %v", ErrUnusable, err)
	}
	return nil
}

// ownedHere refuses an executable another account could change after it is checked, since the
// server runs it as itself: the file a link leads to, and the directories both are named in.
func ownedHere(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("%w: %s cannot be followed: %v", ErrUnusable, path, err)
	}
	for _, p := range []string{filepath.Dir(path), filepath.Dir(resolved), resolved} {
		info, err := os.Stat(p)
		if err != nil {
			return fmt.Errorf("%w: %s cannot be read: %v", ErrUnusable, p, err)
		}
		if info.Mode().Perm()&0o002 != 0 {
			return fmt.Errorf("%w: anyone can write %s, so anyone could change what the server runs", ErrUnusable, p)
		}
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || (int(st.Uid) != os.Getuid() && st.Uid != 0) {
			return fmt.Errorf("%w: %s belongs to another account, which could change what the server runs",
				ErrUnusable, p)
		}
	}
	return nil
}

// version runs path once per change to the file, because a page load must not start a CLI each time.
func (t *Toolchain) version(ctx context.Context, path string, args []string,
	parse func([]byte) (string, error), fresh bool) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%s does not exist", path)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not an executable file", path)
	}
	stamp := info.ModTime().String() + "/" + fmt.Sprint(info.Size())
	t.mu.Lock()
	if p, ok := t.versions[path]; ok && p.stamp == stamp && !fresh {
		t.mu.Unlock()
		return p.version, p.err
	}
	t.mu.Unlock()

	v, err := t.probe(ctx, path, args, parse)
	t.mu.Lock()
	if t.versions == nil {
		t.versions = map[string]probed{}
	}
	t.versions[path] = probed{stamp: stamp, version: v, err: err}
	t.mu.Unlock()
	return v, err
}

func (t *Toolchain) probe(ctx context.Context, path string, args []string, parse func([]byte) (string, error)) (string, error) {
	timeout := t.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	// A killed shell can leave a child holding stdout open.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	asked := strings.Join(args, " ")
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("%s did not answer %s within %v; an editor's shim waits for input "+
			"instead, so name the CLI itself", path, asked, timeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%s %s failed: %s", path, asked, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("%s %s failed: %v", path, asked, err)
	}
	v, err := parse(out)
	if err != nil {
		return "", fmt.Errorf("%s %s: %v", path, asked, err)
	}
	return v, nil
}

func codeqlVersion(out []byte) (string, error) {
	var v struct {
		SemanticVersion string `json:"semanticVersion"`
		Version         string `json:"version"`
	}
	if err := json.Unmarshal(out, &v); err != nil || (v.SemanticVersion == "" && v.Version == "") {
		return "", errors.New("answered something other than CodeQL's version, so this is not CodeQL")
	}
	if v.SemanticVersion != "" {
		return v.SemanticVersion, nil
	}
	return v.Version, nil
}

func firstLine(out []byte) (string, error) {
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimSpace(line); line != "" {
			return line, nil
		}
	}
	return "", errors.New("printed no version")
}

// vscodeCopilot is where the Copilot Chat extension installs the CLI, if it did. It is read through a
// root at the configuration directory, which the environment names, so a link cannot lead out of it.
func vscodeCopilot() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	rel := filepath.Join("Code", "User", "globalStorage", "github.copilot-chat", "copilotCli", "copilot")
	if info, err := root.Stat(rel); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return ""
	}
	return filepath.Join(dir, rel)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (c Check) ready() Check {
	c.State = Ready
	return c
}

func (c Check) problem(reason, remedy string) Check {
	c.State, c.Reason = Problem, reason
	if c.Remedy != "" {
		remedy = c.Remedy + "; " + remedy
	}
	c.Remedy = remedy
	return c
}

// withSaved says a path set in Settings waits for a restart.
func (c Check) withSaved() Check {
	if c.Saved != "" {
		c.Remedy = "restart the server to run " + c.Saved + ", saved in Settings"
	}
	return c
}
