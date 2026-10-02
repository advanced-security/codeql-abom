package ingest

import (
	"io"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// HeldFromSARIF reads what a document reports as the findings it would be, storing none of them
// and naming no repository row or evaluation: a held run's results wait for somebody to convert
// them.
func HeldFromSARIF(r io.Reader, opts Options) ([]store.Finding, error) {
	out := []store.Finding{}
	if err := readFindings(r, opts, func(f store.Finding, _ string) error {
		out = append(out, f)
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
