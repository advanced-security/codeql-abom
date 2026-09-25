package ingest

import (
	"io"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// PredictionsFromSARIF reads what a document reports without storing any of it: not the findings,
// and not the repository they would be stored against.
func PredictionsFromSARIF(r io.Reader, opts Options) ([]store.PredictedResult, error) {
	out := []store.PredictedResult{}
	err := readFindings(r, opts, func(f store.Finding) error {
		out = append(out, store.PredictedResult{
			RuleID: f.RuleID, FilePath: f.FilePath,
			StartLine: f.StartLine, StartColumn: f.StartColumn,
			EndLine: f.EndLine, EndColumn: f.EndColumn,
			Message: f.Message, Severity: f.Severity,
			Fingerprint: f.Fingerprint, ResultHash: f.ResultHash,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
