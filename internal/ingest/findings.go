package ingest

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/advanced-security/codeql-abom/internal/normalize"
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

	err = readFindings(r, opts, func(f store.Finding) error {
		f.QueryRunID = opts.QueryRunID
		f.RepositoryID = repoID
		batch = append(batch, f)
		if len(batch) >= findingBatch {
			return flush()
		}
		return nil
	})
	if err != nil {
		return stored, err
	}
	if err := flush(); err != nil {
		return stored, err
	}
	return stored, nil
}

// readFindings hands each result in a document to found as the finding it would be stored as, with
// no repository and no evaluation named.
//
// One reading for storing and for holding, so converting a held result writes the finding adopting
// the query would have stored.
func readFindings(r io.Reader, opts Options, found func(store.Finding) error) error {
	// Rule metadata carries security-severity and arrives before the results it describes, so
	// it is held here and joined by rule id as each result streams past.
	rules := make(map[string]sarifin.Rule)
	// The producer, as the document spells it. Empty until a tool is seen, and left empty if the
	// document names none — substituting CodeQL there would invent the provenance.
	var producer string

	err := sarifin.Stream(r, sarifin.Handler{
		OnTool: func(tool sarifin.Tool) error {
			producer = tool.Name
			if opts.CLIVersion == "" || tool.SemanticVersion == opts.CLIVersion {
				return nil
			}
			return fmt.Errorf(
				"%s: the ledger records CodeQL %s but the SARIF was written by %q",
				opts.Repository, opts.CLIVersion, tool.SemanticVersion)
		},
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

			return found(store.Finding{
				Provider:         producer,
				RuleID:           res.RuleID,
				FilePath:         res.FilePath,
				StartLine:        res.Region.StartLine,
				StartColumn:      res.Region.StartColumn,
				EndLine:          res.Region.EndLine,
				EndColumn:        res.Region.EndColumn,
				Message:          res.Message,
				Severity:         severity,
				SecuritySeverity: parseSeverity(rule.SecuritySeverity),
				Fingerprint:      res.Fingerprints["primaryLocationLineHash"],
				ResultHash: normalize.ResultHash(
					res.RuleID, res.FilePath, res.Fingerprints["primaryLocationLineHash"],
					normalize.Region{
						StartLine: res.Region.StartLine, StartColumn: res.Region.StartColumn,
						EndLine: res.Region.EndLine, EndColumn: res.Region.EndColumn,
					}),
			})
		},
	})
	if err != nil {
		return fmt.Errorf("streaming sarif for %s: %w", opts.Repository, err)
	}
	// Searching and finding nothing is a result; searching for nothing is not, and the two are
	// the same row once stored. CodeQL lists every rule it ran whether or not it matched, so an
	// empty rule set means the analysis covered no query at all.
	if len(rules) == 0 {
		return fmt.Errorf(
			"%s: the analysis named no rules, so nothing was searched for", opts.Repository)
	}
	return nil
}

// parseSeverity reads the security-severity string CodeQL puts in rule properties, nil when the
// rule carries none or one that is not a number.
func parseSeverity(s string) *float64 {
	if s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &v
}
