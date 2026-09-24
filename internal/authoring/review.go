package authoring

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Actual is what one of a query's tests last reported, beside what it is expected to report.
type Actual struct {
	// Test is the .qlref, relative to the pack.
	Test string `json:"test"`
	// Actual is nil where the test has reported nothing to compare.
	Actual   *string `json:"actual"`
	Expected *string `json:"expected"`
	// Stale says why the report describes a query or fixtures that have since changed.
	Stale string `json:"stale"`
}

// Actuals reads what each of a query's tests last reported and is expected to report.
func Actuals(root string, q Query) ([]Actual, error) {
	source, err := os.Stat(q.Path)
	if err != nil {
		return nil, err
	}
	out := make([]Actual, 0, len(q.Tests))
	for _, test := range q.Tests {
		base := strings.TrimSuffix(test, ".qlref")
		a := Actual{Test: relTo(root, test)}
		if a.Expected, err = readIfThere(base + ".expected"); err != nil {
			return nil, err
		}
		info, err := os.Stat(base + ".actual")
		switch {
		case errors.Is(err, fs.ErrNotExist):
			out = append(out, a)
			continue
		case err != nil:
			return nil, err
		}
		if a.Actual, err = readIfThere(base + ".actual"); err != nil {
			return nil, err
		}
		fixtures, err := newestSource(filepath.Dir(test))
		if err != nil {
			return nil, err
		}
		switch {
		case source.ModTime().After(info.ModTime()):
			a.Stale = "the query changed after this was reported; run its tests again"
		case fixtures.After(info.ModTime()):
			a.Stale = "the fixtures changed after this was reported; run the tests again"
		}
		out = append(out, a)
	}
	return out, nil
}

// DigestActuals names the report a reviewer read, so approving one that has since changed is refused.
func DigestActuals(actuals []Actual) string {
	h := sha256.New()
	for _, a := range actuals {
		if a.Actual == nil {
			fmt.Fprintf(h, "%s\x00-\x00", a.Test)
			continue
		}
		fmt.Fprintf(h, "%s\x00%d\x00%s", a.Test, len(*a.Actual), *a.Actual)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func readIfThere(path string) (*string, error) {
	body, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	}
	text := string(body)
	return &text, nil
}
