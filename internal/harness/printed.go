package harness

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// printers are the tool queries whose output is kept: how the fixtures read to CodeQL.
var printers = []string{"PrintAST", "PrintCFG"}

// savedTo is how ql-mcp says where it wrote a query's interpreted output.
var savedTo = regexp.MustCompile(`Interpreted output saved to: (\S+)`)

// maxPrinted bounds a kept printout; the tree of a test's fixtures is far smaller.
const maxPrinted = 1 << 20

// Printed is what a tool query printed over the target's test database.
type Printed struct {
	At          time.Time
	Query       string
	Database    string
	SourceFiles string
	Body        string
	Truncated   bool
}

// KeepPrinted keeps what ql-mcp prints into scratch, which is scrubbed with the session.
func (g *Gate) KeepPrinted(scratch string, keep func(Printed) (int64, error)) error {
	dir, err := realDir(scratch)
	if err != nil {
		return fmt.Errorf("the session's scratch: %w", err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.scratch, g.keepPrinted = dir, keep
	return nil
}

// print keeps a printout a tool query call produced, says on the timeline whether it could, and
// returns what to tell the agent about where the tree is.
func (g *Gate) print(tool string, args any, result string) string {
	fields, _ := args.(map[string]any)
	name, _ := fields["queryName"].(string)
	if tool != Server+"-codeql_query_run" || !slices.Contains(printers, name) {
		return ""
	}
	g.mu.Lock()
	scratch, keep := g.scratch, g.keepPrinted
	g.mu.Unlock()
	if keep == nil {
		return ""
	}
	b := g.Bounds()
	e := Event{Hook: HookArtifact, Tool: tool, Subject: b.subject(tool, args), Decision: DecisionNoted,
		Step: StepAST}
	body, files, truncated, err := readPrinted(scratch, result)
	var context string
	if err == nil {
		sourceFiles, _ := fields["sourceFiles"].(string)
		var id int64
		id, err = keep(Printed{At: g.Now(), Query: name, Database: b.rel(b.database()),
			SourceFiles: sourceFiles, Body: body, Truncated: truncated})
		if err == nil {
			e.Detail = fmt.Sprintf("kept %d bytes of what %s printed, as printout %d", len(body), name, id)
			context = fmt.Sprintf("ABOM kept what %s printed. Read the tree in %s.", name,
				strings.Join(files, " and "))
		}
	}
	if err != nil {
		e.Reason = fmt.Sprintf("what %s printed was not kept: %v", name, err)
	}
	_ = g.keep(e, Verdict{})
	return context
}

// readPrinted reads what ql-mcp says it wrote, a file or the directory graphtext writes, only from
// the session's scratch: the answer naming it is text the agent's own call shaped.
func readPrinted(scratch, result string) (string, []string, bool, error) {
	root, err := printedRoot(scratch, result)
	if err != nil {
		return "", nil, false, err
	}
	files, err := printedFiles(scratch, root, result)
	if err != nil {
		return "", nil, false, err
	}
	body, truncated, err := readPrintedFiles(root, files)
	if err != nil {
		return "", nil, false, err
	}
	return body, files, truncated, nil
}

func printedRoot(scratch, result string) (string, error) {
	m := savedTo.FindStringSubmatch(result)
	if m == nil {
		return "", errors.New("ql-mcp did not say where it wrote the output")
	}
	root, err := filepath.EvalSymlinks(m[1])
	if err != nil {
		return "", err
	}
	if scratch == "" || !inside(scratch, root) {
		return "", fmt.Errorf("%s is outside the session's scratch", m[1])
	}
	return root, nil
}

func printedFiles(scratch, root, result string) ([]string, error) {
	m := savedTo.FindStringSubmatch(result)
	var files []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// A link is followed only to where it resolves, and kept only if that is in the scratch too.
		if real, err := filepath.EvalSymlinks(p); err == nil && inside(scratch, real) {
			if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() {
				files = append(files, real)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s holds no file", m[1])
	}
	sort.Strings(files)
	return files, nil
}

func readPrintedFiles(root string, files []string) (string, bool, error) {
	var buf []byte
	for _, file := range files {
		if len(files) > 1 {
			rel, _ := filepath.Rel(root, file)
			buf = append(buf, "== "+filepath.ToSlash(rel)+" ==\n"...)
		}
		f, err := os.Open(file)
		if err != nil {
			return "", false, err
		}
		part, err := io.ReadAll(io.LimitReader(f, int64(maxPrinted+1-len(buf))))
		f.Close()
		if err != nil {
			return "", false, err
		}
		if buf = append(buf, part...); len(buf) > maxPrinted {
			break
		}
	}
	if len(buf) <= maxPrinted {
		return string(buf), false, nil
	}
	cut := maxPrinted
	for cut > 0 && !utf8.RuneStart(buf[cut]) {
		cut--
	}
	return string(buf[:cut]), true, nil
}
