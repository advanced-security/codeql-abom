package ingest

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/sarifin"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// findingBatch bounds how many findings are buffered before a write, keeping peak memory tied
// to the batch rather than to the size of the SARIF file.
const findingBatch = 500

// FindingsFromSARIF streams query results into the findings table, returning the count stored.
//
// Findings are recorded exactly as the queries reported them, keyed by rule id. ABOM adds no
// interpretation: "unpinned" means a row exists for actions/unpinned-tag, and changing that
// definition means changing the query.
func (i *Ingester) FindingsFromSARIF(r io.Reader, opts Options) (int, error) {
	if opts.Repository == "" {
		return 0, errors.New("ingest: Options.Repository is required")
	}

	repoID, err := i.store.UpsertRepository(opts.Repository, opts.RepositoryURL)
	if err != nil {
		return 0, err
	}

	// Rule metadata carries security-severity and arrives before the results it describes, so
	// it is held here and joined by rule id as each result streams past.
	rules := make(map[string]sarifin.Rule)

	var batch []store.Finding
	stored := 0

	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		n, err := i.store.AddFindings(batch)
		if err != nil {
			return err
		}
		stored += n
		batch = batch[:0]
		return nil
	}

	err = sarifin.Stream(r, sarifin.Handler{
		OnRule: func(rule sarifin.Rule) error {
			rules[rule.ID] = rule
			return nil
		},
		OnResult: func(res sarifin.Result) error {
			rule := rules[res.RuleID]

			severity := res.Level
			if severity == "" {
				severity = rule.Severity
			}

			batch = append(batch, store.Finding{
				QueryRunID:       opts.QueryRunID,
				RuleID:           res.RuleID,
				RepositoryID:     repoID,
				FilePath:         res.FilePath,
				StartLine:        res.Region.StartLine,
				StartColumn:      res.Region.StartColumn,
				EndLine:          res.Region.EndLine,
				EndColumn:        res.Region.EndColumn,
				Message:          res.Message,
				Severity:         severity,
				SecuritySeverity: parseSeverity(rule.SecuritySeverity),
				Fingerprint:      res.Fingerprints["primaryLocationLineHash"],
			})

			if len(batch) >= findingBatch {
				return flush()
			}
			return nil
		},
	})
	if err != nil {
		return stored, fmt.Errorf("streaming sarif for %s: %w", opts.Repository, err)
	}
	if err := flush(); err != nil {
		return stored, err
	}
	return stored, nil
}

// parseSeverity converts the security-severity string CodeQL puts in rule properties, or 0
// when the rule carries none.
func parseSeverity(s string) float64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
