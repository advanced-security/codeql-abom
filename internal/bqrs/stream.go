package bqrs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// RowFunc receives one decoded row. Returning an error aborts the stream.
type RowFunc func(Row) error

// StreamSelect walks the "#select" result set without materializing its tuples, invoking fn
// once per row and returning the number of rows delivered.
//
// Peak memory is proportional to the widest single row rather than to the result count, which
// is what makes a monorepo emitting hundreds of thousands of edges survivable.
func StreamSelect(r io.Reader, fn RowFunc) (int, error) {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return 0, err
	}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return 0, err
		}
		if key != SelectSetName {
			if err := skipValue(dec); err != nil {
				return 0, err
			}
			continue
		}
		return streamResultSet(dec, fn)
	}
	return 0, fmt.Errorf("bqrs has no %q result set", SelectSetName)
}

func streamResultSet(dec *json.Decoder, fn RowFunc) (int, error) {
	if err := expectDelim(dec, '{'); err != nil {
		return 0, err
	}
	var set ResultSet
	count := 0
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return count, err
		}
		switch key {
		case "columns":
			if err := dec.Decode(&set.Columns); err != nil {
				return count, fmt.Errorf("decoding columns: %w", err)
			}
		case "tuples":
			if len(set.Columns) == 0 {
				return count, errors.New(`bqrs "tuples" appeared before "columns"`)
			}
			if err := expectDelim(dec, '['); err != nil {
				return count, err
			}
			for dec.More() {
				var values []json.RawMessage
				if err := dec.Decode(&values); err != nil {
					return count, fmt.Errorf("decoding tuple %d: %w", count, err)
				}
				if err := fn(Row{set: set, values: values}); err != nil {
					return count, err
				}
				count++
			}
			if err := expectDelim(dec, ']'); err != nil {
				return count, err
			}
		default:
			if err := skipValue(dec); err != nil {
				return count, err
			}
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return count, err
	}
	return count, nil
}

func readKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected an object key, got %v", tok)
	}
	return key, nil
}

func expectDelim(dec *json.Decoder, want rune) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	got, ok := tok.(json.Delim)
	if !ok || rune(got) != want {
		return fmt.Errorf("expected %q, got %v", want, tok)
	}
	return nil
}

func skipValue(dec *json.Decoder) error {
	var discard json.RawMessage
	return dec.Decode(&discard)
}

// StreamFile pipes `codeql bqrs decode` straight into the row handler.
//
// The CLI's stdout is consumed as it is produced rather than buffered, so a result set larger
// than available memory still decodes.
func StreamFile(ctx context.Context, codeqlPath, path string, fn RowFunc) (int, error) {
	if codeqlPath == "" {
		codeqlPath = "codeql"
	}
	cmd := exec.CommandContext(ctx, codeqlPath, "bqrs", "decode", "--format=json", "--entities=all", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("piping codeql stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting codeql bqrs decode: %w", err)
	}

	count, streamErr := StreamSelect(stdout, fn)

	// Drain any remainder so the child never blocks on a full pipe when fn aborted early.
	_, _ = io.Copy(io.Discard, stdout)
	if waitErr := cmd.Wait(); waitErr != nil && streamErr == nil {
		return count, fmt.Errorf("codeql bqrs decode %s: %w", path, waitErr)
	}
	if streamErr != nil {
		return count, fmt.Errorf("decoding %s: %w", path, streamErr)
	}
	return count, nil
}
