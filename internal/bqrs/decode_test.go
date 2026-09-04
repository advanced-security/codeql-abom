package bqrs

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T) ResultSet {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	decoded, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rs, err := decoded.Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	return rs
}

func TestParseColumns(t *testing.T) {
	rs := loadFixture(t)

	want := []Column{
		{"edgeKind", "String"},
		{"srcKind", "String"},
		{"srcPath", "String"},
		{"srcJobId", "String"},
		{"srcStepIndex", "Integer"},
		{"srcStepId", "String"},
		{"usesRaw", "String"},
		{"pinComment", "String"},
		{"startLine", "Integer"},
		{"startColumn", "Integer"},
		{"endLine", "Integer"},
		{"endColumn", "Integer"},
	}

	if len(rs.Columns) != len(want) {
		t.Fatalf("got %d columns, want %d", len(rs.Columns), len(want))
	}
	for i, c := range want {
		if rs.Columns[i] != c {
			t.Errorf("column %d = %+v, want %+v", i, rs.Columns[i], c)
		}
	}
}

func TestParseTupleCount(t *testing.T) {
	if got := len(loadFixture(t).Tuples); got != 13 {
		t.Errorf("got %d tuples, want 13", got)
	}
}

func TestRowAccessorsByColumnName(t *testing.T) {
	rows := loadFixture(t).Rows()

	var found bool
	for _, r := range rows {
		uses, err := r.String("usesRaw")
		if err != nil {
			t.Fatalf("String(usesRaw): %v", err)
		}
		if uses != "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683" {
			continue
		}
		found = true

		pin, err := r.String("pinComment")
		if err != nil {
			t.Fatalf("String(pinComment): %v", err)
		}
		if pin != "v4.2.2" {
			t.Errorf("pinComment = %q, want %q", pin, "v4.2.2")
		}

		idx, err := r.Int("srcStepIndex")
		if err != nil {
			t.Fatalf("Int(srcStepIndex): %v", err)
		}
		if idx != 0 {
			t.Errorf("srcStepIndex = %d, want 0", idx)
		}

		line, err := r.Int("startLine")
		if err != nil {
			t.Fatalf("Int(startLine): %v", err)
		}
		if line != 16 {
			t.Errorf("startLine = %d, want 16", line)
		}
	}
	if !found {
		t.Fatal("pinned checkout row not present in fixture")
	}
}

// Job-level reusable workflow calls have no enclosing step and encode that as -1.
func TestNegativeStepIndexDecodes(t *testing.T) {
	rows := loadFixture(t).Rows()

	var seen int
	for _, r := range rows {
		kind, err := r.String("edgeKind")
		if err != nil {
			t.Fatalf("String(edgeKind): %v", err)
		}
		if kind != "job-uses-reusable-workflow" {
			continue
		}
		seen++
		idx, err := r.Int("srcStepIndex")
		if err != nil {
			t.Fatalf("Int(srcStepIndex): %v", err)
		}
		if idx != -1 {
			t.Errorf("srcStepIndex = %d, want -1", idx)
		}
	}
	if seen != 2 {
		t.Errorf("got %d reusable-workflow edges, want 2", seen)
	}
}

func TestMissingColumnIsAnError(t *testing.T) {
	rows := loadFixture(t).Rows()
	if _, err := rows[0].String("nope"); err == nil {
		t.Error("expected an error for an unknown column")
	}
}

func TestSelectMissingIsAnError(t *testing.T) {
	decoded, err := Parse([]byte(`{"other":{"columns":[],"tuples":[]}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := decoded.Select(); err == nil {
		t.Error("expected an error when #select is absent")
	}
}

// Entity columns decode as objects, not bare strings; String must fall back to the label.
func TestStringFallsBackToEntityLabel(t *testing.T) {
	decoded, err := Parse([]byte(
		`{"#select":{"columns":[{"name":"e","kind":"Entity"}],` +
			`"tuples":[[{"label":"myLabel","url":{"uri":"file:///x"}}]]}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rs, err := decoded.Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	got, err := rs.Rows()[0].String("e")
	if err != nil {
		t.Fatalf("String(e): %v", err)
	}
	if got != "myLabel" {
		t.Errorf("got %q, want %q", got, "myLabel")
	}
}
