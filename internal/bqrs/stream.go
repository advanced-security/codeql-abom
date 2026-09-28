package bqrs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
		count, err = streamResultSetField(dec, key, &set, count, fn)
		if err != nil {
			return count, err
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return count, err
	}
	return count, nil
}

func streamResultSetField(
	dec *json.Decoder, key string, set *ResultSet, count int, fn RowFunc,
) (int, error) {
	switch key {
	case "columns":
		if err := dec.Decode(&set.Columns); err != nil {
			return count, fmt.Errorf("decoding columns: %w", err)
		}
		return count, nil
	case "tuples":
		return streamTuples(dec, *set, count, fn)
	default:
		if err := skipValue(dec); err != nil {
			return count, err
		}
		return count, nil
	}
}

func streamTuples(dec *json.Decoder, set ResultSet, count int, fn RowFunc) (int, error) {
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
