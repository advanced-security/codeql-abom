// Package bqrs decodes CodeQL Binary Query Result Set files into typed Go rows.
//
// Decoding shells out to `codeql bqrs decode` rather than reading the binary format directly:
// BQRS is an internal format with no compatibility guarantee, whereas the CLI's JSON output is
// stable and versioned alongside the CLI we already require.
package bqrs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
)

// Column is one column of a result set. Kind uses the long spellings emitted by
// `bqrs decode --format=json` ("String", "Integer", "Float", "Boolean", "Entity"), which differ
// from the single-letter codes reported by `bqrs info --format=json`.
type Column struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ResultSet is a single named result set: its column headers and its raw tuples.
type ResultSet struct {
	Columns []Column            `json:"columns"`
	Tuples  [][]json.RawMessage `json:"tuples"`
}

// Decoded maps result-set name to result set. The default set produced by a `select` clause is
// named "#select".
type Decoded map[string]ResultSet

// SelectSetName is the name CodeQL gives the result set of a query's `select` clause.
const SelectSetName = "#select"

// Parse reads the JSON produced by `codeql bqrs decode --format=json`.
func Parse(data []byte) (Decoded, error) {
	var decoded Decoded
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("parsing bqrs json: %w", err)
	}
	return decoded, nil
}

// Select returns the `#select` result set.
func (d Decoded) Select() (ResultSet, error) {
	rs, ok := d[SelectSetName]
	if !ok {
		return ResultSet{}, fmt.Errorf("bqrs has no %q result set", SelectSetName)
	}
	return rs, nil
}

// ColumnIndex returns the position of the named column.
func (rs ResultSet) ColumnIndex(name string) (int, error) {
	for i, c := range rs.Columns {
		if c.Name == name {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no column named %q", name)
}

// Row provides positional, type-checked access to one tuple.
type Row struct {
	set    ResultSet
	values []json.RawMessage
}

// Rows returns each tuple wrapped for column-name access.
func (rs ResultSet) Rows() []Row {
	rows := make([]Row, 0, len(rs.Tuples))
	for _, t := range rs.Tuples {
		rows = append(rows, Row{set: rs, values: t})
	}
	return rows
}

// String returns the named column as a string.
func (r Row) String(column string) (string, error) {
	raw, err := r.raw(column)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		// Entity columns decode as {"label":...,"url":...} rather than a bare string.
		var entity struct {
			Label string `json:"label"`
		}
		if json.Unmarshal(raw, &entity) == nil && entity.Label != "" {
			return entity.Label, nil
		}
		return "", fmt.Errorf("column %q is not a string: %w", column, err)
	}
	return s, nil
}

// Int returns the named column as an int, accepting the quoted integers CodeQL emits for
// values outside the range JSON numbers represent exactly.
func (r Row) Int(column string) (int, error) {
	raw, err := r.raw(column)
	if err != nil {
		return 0, err
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, convErr := strconv.Atoi(s); convErr == nil {
			return n, nil
		}
	}
	return 0, fmt.Errorf("column %q is not an integer", column)
}

func (r Row) raw(column string) (json.RawMessage, error) {
	i, err := r.set.ColumnIndex(column)
	if err != nil {
		return nil, err
	}
	if i >= len(r.values) {
		return nil, fmt.Errorf("row has %d values, want column %d (%q)", len(r.values), i, column)
	}
	return r.values[i], nil
}

// DecodeFile runs `codeql bqrs decode` on path and parses the result.
func DecodeFile(ctx context.Context, codeqlPath, path string) (Decoded, error) {
	if codeqlPath == "" {
		codeqlPath = "codeql"
	}
	cmd := exec.CommandContext(ctx, codeqlPath, "bqrs", "decode", "--format=json", "--entities=all", path)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("codeql bqrs decode %s: %w: %s", path, err, exitErr.Stderr)
		}
		return nil, fmt.Errorf("codeql bqrs decode %s: %w", path, err)
	}
	return Parse(out)
}
