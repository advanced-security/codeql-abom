package publish

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/querypack"
)

// Component is one file an export sends, with the digest its decision was made over.
type Component struct {
	Path   string `json:"path"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
}

// Evidence is what an export is decided over: the query version, its pack and bundle, where it goes,
// and every file it sends.
type Evidence struct {
	Query       string      `json:"query"`
	Version     string      `json:"version"`
	Pack        string      `json:"pack"`
	PackVersion string      `json:"packVersion"`
	Bundle      string      `json:"bundleSha256"`
	Target      string      `json:"target"`
	Components  []Component `json:"components"`
	// Route is the decision that admitted the request to export, which this one cites.
	Route string `json:"route,omitempty"`
}

// Record is the evidence and the decision made over it, written beside the query it exports.
type Record struct {
	Evidence
	Decision  string `json:"decision"`
	DecidedBy string `json:"decidedBy"`
}

// Tests is what ABOM recorded of a query's tests at the version exported.
type Tests struct {
	// Unread is why the record could not be read, when it could not; nothing else is then known.
	Unread        error
	Ran           bool
	Passed, Total int
	CLIVersion    string
}

// EvidenceOf is what exporting files of m to target is decided over.
func EvidenceOf(m querypack.Manifest, target Target, files []querypack.File) Evidence {
	e := Evidence{Query: m.Query, Version: m.Version, Pack: m.Pack, PackVersion: m.PackVersion,
		Bundle: m.BundleSHA256, Target: target.String()}
	for _, f := range files {
		e.Components = append(e.Components, Component{f.Path, f.Role, f.SHA256})
	}
	return e
}

// Compose is the pull request exporting files of m, decided as decision by by: the files, a record
// of the decision beside them, and a body citing both.
func Compose(m querypack.Manifest, e Evidence, files []querypack.File, decision, by string, tests Tests) (Export, error) {
	record, err := json.MarshalIndent(Record{e, decision, by}, "", "  ")
	if err != nil {
		return Export{}, err
	}
	out := Export{
		Title:  "Add " + m.Query + " (" + m.Version + ")",
		Branch: "abom/export/" + m.ExportName(),
		Body:   body(m, e, decision, by, tests),
	}
	for _, f := range files {
		out.Files = append(out.Files, File{Path: f.Path, Content: f.Content})
	}
	out.Files = append(out.Files, File{Path: "abom-exports/" + m.ExportName() + ".json", Content: append(record, '\n')})
	return out, nil
}

func body(m querypack.Manifest, e Evidence, decision, by string, tests Tests) string {
	var b strings.Builder
	fmt.Fprintf(&b, "ABOM proposes **%s** at version `%s`, from pack `%s` %s.\n\n", m.Query, m.Version, m.Pack, m.PackVersion)
	fmt.Fprintf(&b, "- Bundle: `sha256:%s`\n", e.Bundle)
	fmt.Fprintf(&b, "- Decision: `%s`, made by %s and logged in ABOM's authorization log\n", decision, by)
	b.WriteString("- Tests: ")
	switch {
	case tests.Unread != nil:
		fmt.Fprintf(&b, "ABOM's record of them could not be read (%v); this repository's own workflow runs them\n", tests.Unread)
	case tests.Ran:
		fmt.Fprintf(&b, "%d of %d passed under CodeQL %s\n", tests.Passed, tests.Total, tests.CLIVersion)
	default:
		b.WriteString("not run at this version; this repository's own workflow runs them\n")
	}
	b.WriteString("\n| Component | Role | sha256 |\n| --- | --- | --- |\n")
	for _, c := range e.Components {
		fmt.Fprintf(&b, "| `%s` | %s | `%s` |\n", c.Path, c.Role, c.SHA256[:16])
	}
	b.WriteString("\nMerging is yours to decide: ABOM opens this pull request and never merges or publishes.\n")
	return b.String()
}
