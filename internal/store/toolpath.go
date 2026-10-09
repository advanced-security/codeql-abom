package store

import (
	"fmt"
	"time"
)

// Tools a path can be set for from the interface. Anything else would let a control request
// name a program this server then executes for a purpose nobody declared.
const (
	ToolCodeQL  = "codeql"
	ToolCopilot = "copilot"
)

// toolPathDDL keeps the operator's choice of executable per tool. No row means the start-up
// default applies, which is different from a path that was set and then found missing.
const toolPathDDL = `
CREATE TABLE IF NOT EXISTS abom_tool_path (
  tool   TEXT PRIMARY KEY CHECK (tool IN ('codeql', 'copilot')),
  path   TEXT NOT NULL CHECK (path <> ''),
  set_by TEXT NOT NULL,
  set_at TEXT NOT NULL
);`

// ToolPath is an executable an operator chose for a tool.
type ToolPath struct {
	Path  string `json:"path"`
	SetBy string `json:"setBy"`
	SetAt string `json:"setAt"`
}

// SetToolPath records the executable for a tool; an empty path returns it to the default.
func (s *Store) SetToolPath(tool, path, by string) error {
	if tool != ToolCodeQL && tool != ToolCopilot {
		return fmt.Errorf("no tool %q: a path can be set for %s or %s", tool, ToolCodeQL, ToolCopilot)
	}
	if path == "" {
		_, err := s.db.Exec(`DELETE FROM abom_tool_path WHERE tool = ?`, tool)
		return err
	}
	_, err := s.db.Exec(`
INSERT INTO abom_tool_path (tool, path, set_by, set_at) VALUES (?, ?, ?, ?)
ON CONFLICT(tool) DO UPDATE SET path = excluded.path, set_by = excluded.set_by, set_at = excluded.set_at`,
		tool, path, by, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ToolPaths reports every tool an operator set a path for.
func (s *Store) ToolPaths() (map[string]ToolPath, error) {
	rows, err := s.db.Query(`SELECT tool, path, set_by, set_at FROM abom_tool_path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := map[string]ToolPath{}
	for rows.Next() {
		var tool string
		var p ToolPath
		if err := rows.Scan(&tool, &p.Path, &p.SetBy, &p.SetAt); err != nil {
			return nil, err
		}
		paths[tool] = p
	}
	return paths, rows.Err()
}
