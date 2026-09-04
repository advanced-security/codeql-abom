package bqrs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStreamSelectMatchesParse(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var streamed []string
	n, err := StreamSelect(strings.NewReader(string(data)), func(r Row) error {
		uses, err := r.String("usesRaw")
		if err != nil {
			return err
		}
		streamed = append(streamed, uses)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamSelect: %v", err)
	}
	if n != 13 {
		t.Errorf("streamed %d rows, want 13", n)
	}

	decoded, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	rs, err := decoded.Select()
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(streamed) != len(rs.Tuples) {
		t.Fatalf("streamed %d rows, buffered %d", len(streamed), len(rs.Tuples))
	}
	for i, row := range rs.Rows() {
		want, err := row.String("usesRaw")
		if err != nil {
			t.Fatalf("String(usesRaw): %v", err)
		}
		if streamed[i] != want {
			t.Errorf("row %d: streamed %q, buffered %q", i, streamed[i], want)
		}
	}
}

// Rows must reach the handler as they are decoded, not after the whole document is read.
func TestStreamSelectDeliversRowsIncrementally(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "abom-edges.json"))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	stop := errors.New("stop")
	seen := 0
	n, err := StreamSelect(strings.NewReader(string(data)), func(Row) error {
		seen++
		if seen == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("got error %v, want the handler's sentinel", err)
	}
	if seen != 3 {
		t.Errorf("handler ran %d times, want 3", seen)
	}
	if n != 2 {
		t.Errorf("reported %d completed rows, want 2", n)
	}
}

func TestStreamSelectIgnoresOtherResultSets(t *testing.T) {
	const doc = `{"other":{"columns":[{"name":"x","kind":"String"}],"tuples":[["ignored"]]},` +
		`"#select":{"columns":[{"name":"a","kind":"String"}],"tuples":[["kept"]]}}`

	var got []string
	n, err := StreamSelect(strings.NewReader(doc), func(r Row) error {
		v, err := r.String("a")
		if err != nil {
			return err
		}
		got = append(got, v)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamSelect: %v", err)
	}
	if n != 1 || len(got) != 1 || got[0] != "kept" {
		t.Errorf("got %v (n=%d), want [kept] (n=1)", got, n)
	}
}

func TestStreamSelectMissingSelectIsAnError(t *testing.T) {
	const doc = `{"other":{"columns":[],"tuples":[]}}`
	if _, err := StreamSelect(strings.NewReader(doc), func(Row) error { return nil }); err == nil {
		t.Error("expected an error when #select is absent")
	}
}

// Column headers are required to interpret tuples, so the inverted order must be rejected
// loudly rather than silently yielding rows with no addressable columns.
func TestStreamSelectRejectsTuplesBeforeColumns(t *testing.T) {
	const doc = `{"#select":{"tuples":[["a"]],"columns":[{"name":"a","kind":"String"}]}}`
	_, err := StreamSelect(strings.NewReader(doc), func(Row) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "before") {
		t.Errorf("got %v, want an ordering error", err)
	}
}

func TestStreamSelectTruncatedInputIsAnError(t *testing.T) {
	const doc = `{"#select":{"columns":[{"name":"a","kind":"String"}],"tuples":[["a"],`
	if _, err := StreamSelect(strings.NewReader(doc), func(Row) error { return nil }); err == nil {
		t.Error("expected an error for truncated input")
	}
}
