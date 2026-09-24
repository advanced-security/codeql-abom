package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// toolsPackName is the pack ql-mcp runs its actions tool queries from.
const toolsPackName = "advanced-security/ql-mcp-actions-tools-src"

// ToolsPack is that pack, pinned, so a tree ABOM prints and one a session printed come from the
// same query.
const ToolsPack = toolsPackName + "@2.27.1"

// printASTSources names the data extension that tells PrintAST which fixtures to print.
const printASTSources = "abom/print-ast-sources"

// maxPrinted bounds a kept tree; a fixture's tree is kilobytes.
const maxPrinted = 1 << 20

// PrintedTree is what PrintAST printed over a test database.
type PrintedTree struct {
	Body      string
	Truncated bool
}

// PrintAST prints the tree of the named fixtures in a test database, through the query, data
// extension and output format ql-mcp uses.
func (r *Runner) PrintAST(ctx context.Context, database string, sourceFiles []string) (PrintedTree, error) {
	if database == "" || len(sourceFiles) == 0 {
		return PrintedTree{}, fmt.Errorf("runner: printing a tree needs a database and the fixtures to print")
	}
	release, err := r.Lock.Acquire(ctx)
	if err != nil {
		return PrintedTree{}, err
	}
	defer release()

	work, err := os.MkdirTemp("", "abom-print-ast-")
	if err != nil {
		return PrintedTree{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	sources := filepath.Join(work, "sources")
	if err := writeSources(sources, sourceFiles); err != nil {
		return PrintedTree{}, err
	}
	query, err := r.toolsQuery(ctx, "PrintAST/PrintAST.ql")
	if err != nil {
		return PrintedTree{}, err
	}

	bqrs := filepath.Join(work, "results.bqrs")
	args := []string{"query", "run", "--database=" + database, "--output=" + bqrs,
		"--additional-packs=" + sources, "--model-packs=" + printASTSources + "@0.0.0"}
	if r.Threads > 0 {
		args = append(args, fmt.Sprintf("--threads=%d", r.Threads))
	}
	if r.RAMMB > 0 {
		args = append(args, fmt.Sprintf("--ram=%d", r.RAMMB))
	}
	if out, err := exec.CommandContext(ctx, r.codeql(), append(args, query)...).CombinedOutput(); err != nil {
		return PrintedTree{}, fmt.Errorf("codeql query run PrintAST over %s: %w: %s", database, err,
			strings.TrimSpace(string(out)))
	}
	printed := filepath.Join(work, "printed")
	if out, err := exec.CommandContext(ctx, r.codeql(), "bqrs", "interpret", "--format=graphtext",
		"-t=kind=graph", "-t=id=actions/tools/print-ast", "--output="+printed, bqrs).CombinedOutput(); err != nil {
		return PrintedTree{}, fmt.Errorf("codeql bqrs interpret of PrintAST: %w: %s", err,
			strings.TrimSpace(string(out)))
	}
	return readTree(printed)
}

// toolsQuery resolves a query in the pinned tools pack, fetching the pack the first time.
func (r *Runner) toolsQuery(ctx context.Context, path string) (string, error) {
	spec := ToolsPack + ":" + path
	resolve := func() (string, error) {
		out, err := exec.CommandContext(ctx, r.codeql(), "resolve", "queries", spec, "--format=json").Output()
		if err != nil {
			return "", fmt.Errorf("codeql resolve queries %s: %w", spec, err)
		}
		var paths []string
		if err := json.Unmarshal(out, &paths); err != nil || len(paths) != 1 {
			return "", fmt.Errorf("codeql resolve queries %s answered %s", spec, strings.TrimSpace(string(out)))
		}
		return paths[0], nil
	}
	if query, err := resolve(); err == nil {
		return query, nil
	}
	if out, err := exec.CommandContext(ctx, r.codeql(), "pack", "download", ToolsPack).CombinedOutput(); err != nil {
		return "", fmt.Errorf("codeql pack download %s: %w: %s", ToolsPack, err, strings.TrimSpace(string(out)))
	}
	return resolve()
}

// writeSources lays out the data extension that fills PrintAST's selectedSourceFiles.
func writeSources(dir string, files []string) error {
	if err := os.MkdirAll(filepath.Join(dir, "ext"), 0o755); err != nil {
		return err
	}
	pack := "library: true\nname: " + printASTSources + "\nversion: 0.0.0\nextensionTargets:\n  " +
		toolsPackName + ": \"*\"\ndataExtensions:\n  - \"ext/*.model.yml\"\n"
	var rows strings.Builder
	for _, f := range files {
		fmt.Fprintf(&rows, "      - [%s]\n", strconv.Quote(f))
	}
	model := "extensions:\n  - addsTo:\n      pack: " + toolsPackName + "\n      extensible: selectedSourceFiles\n" +
		"    data:\n" + rows.String()
	if err := os.WriteFile(filepath.Join(dir, "codeql-pack.yml"), []byte(pack), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "ext", "sources.model.yml"), []byte(model), 0o644)
}

// readTree reads what the interpreter wrote, file by file, cut at maxPrinted on a rune boundary.
func readTree(dir string) (PrintedTree, error) {
	files := []string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		return PrintedTree{}, fmt.Errorf("reading what PrintAST printed: %w", err)
	}
	if len(files) == 0 {
		return PrintedTree{}, fmt.Errorf("PrintAST printed no file")
	}
	sort.Strings(files)
	var out strings.Builder
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			return PrintedTree{}, err
		}
		if len(files) > 1 {
			rel, _ := filepath.Rel(dir, f)
			fmt.Fprintf(&out, "== %s ==\n", filepath.ToSlash(rel))
		}
		out.Write(body)
	}
	body := out.String()
	if len(body) <= maxPrinted {
		return PrintedTree{Body: body}, nil
	}
	cut := maxPrinted
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return PrintedTree{Body: body[:cut], Truncated: true}, nil
}
