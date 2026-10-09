package expand

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Why code scanning's analysis did not answer for part of a findings lane, beside the database's reasons.
const (
	refusedNoAnalysis = "no analysis"
	refusedConfig     = "another configuration"
	refusedNoRules    = "no rules in common"
)

// defaultSetupKey begins the analysis key of every analysis default setup makes.
const defaultSetupKey = catalog.DefaultSetupKeyPrefix

// takenAnalysis is code scanning's document for a commit, holding only the lane rules it answers, and that analysis.
type takenAnalysis struct {
	path, from string
}

// evaluateFindings writes the findings lane: code scanning's results for the lane rules its analysis of
// the commit ran, and ABOM's evaluation of the rest at the path it returns, empty where nothing was left.
func (e *Expander) evaluateFindings(
	ctx context.Context, repository, commit, dbDir string, planned tierPlan, sarifPath string,
) (string, takenAnalysis, error) {
	if e.answerable(repository, planned) {
		own, taken, ok, err := e.answerFromCodeScanning(ctx, repository, commit, dbDir, planned, sarifPath)
		if ok || err != nil {
			return own, taken, err
		}
	}
	return sarifPath, takenAnalysis{}, e.Analyzer.AnalyzeQueries(ctx, dbDir, planned.findings, sarifPath)
}

// answerFromCodeScanning takes the analysis's results for the rules it ran and evaluates the rest,
// or reports false, counting why, where the analysis cannot answer.
func (e *Expander) answerFromCodeScanning(
	ctx context.Context, repository, commit, dbDir string, planned tierPlan, sarifPath string,
) (string, takenAnalysis, bool, error) {
	theirs := filepath.Join(filepath.Dir(sarifPath), "code-scanning.sarif")
	from, answered, reason := e.takeAnalysis(ctx, repository, commit, planned, theirs)
	if reason != "" {
		_ = os.Remove(theirs)
		if ctx.Err() == nil {
			e.reuse.count("analysis", reason)
		}
		return "", takenAnalysis{}, false, nil
	}
	own, err := e.evaluateRest(ctx, dbDir, planned, answered, sarifPath)
	if err == nil {
		err = keepResults(theirs, func(rule string) bool { return answered[rule] })
	}
	if err != nil {
		return "", takenAnalysis{}, false, err
	}
	e.reuse.count("analysis", "")
	return own, takenAnalysis{path: theirs, from: from}, true, nil
}

// answerable reports whether an analysis could answer for the lane: an owned repository's, read from
// the files ABOM's own build indexes.
func (e *Expander) answerable(repository string, planned tierPlan) bool {
	if e.CodeScanning == nil || indexFiltersInherited() || len(planned.findingsRules) == 0 ||
		len(planned.findingsPaths) != len(planned.findingsRules) {
		return false
	}
	// Analyses need code scanning read, which the App holds only where it is installed.
	owned, err := e.Store.RepositoryIsOwned(repository)
	return err == nil && owned
}

// takeAnalysis downloads default setup's newest analysis of commit to path, with the lane rules it answers or why none.
func (e *Expander) takeAnalysis(
	ctx context.Context, repository, commit string, planned tierPlan, path string,
) (string, map[string]bool, string) {
	analyses, err := e.CodeScanning.ActionsAnalyses(ctx, repository, e.defaultRef(repository))
	if errors.Is(err, catalog.ErrNoAnalysis) {
		return "", nil, refusedNoAnalysis
	}
	if err != nil {
		return "", nil, refusedUnreadable
	}
	var ofCommit []catalog.Analysis
	for _, a := range analyses {
		if commit != "" && strings.EqualFold(a.CommitSHA, commit) {
			ofCommit = append(ofCommit, a)
		}
	}
	if len(ofCommit) == 0 {
		return "", nil, refusedNoAnalysis
	}
	// The document does not say which files it read: default setup reads the extractor's defaults,
	// where a workflow of the repository's own, or a configuration file, may narrow them. Any threat
	// model only adds sources.
	i := slices.IndexFunc(ofCommit, func(a catalog.Analysis) bool { return strings.HasPrefix(a.AnalysisKey, defaultSetupKey) })
	if i < 0 {
		return "", nil, refusedConfig
	}
	if refusal := e.configFileRefusal(ctx, repository, commit); refusal != "" {
		return "", nil, refusal
	}
	analysis := ofCommit[i]
	if err := e.CodeScanning.DownloadAnalysis(ctx, repository, analysis.ID, path); err != nil {
		return "", nil, refusedUnreadable
	}
	answered, reason := answeredRules(path, planned)
	taken := map[string]bool{}
	for rule := range answered {
		taken[rule] = true
	}
	return fmt.Sprintf("github-code-scanning:%d", analysis.ID), taken, reason
}

// defaultRef is the default branch to read analyses at, or empty, which reads every branch.
func (e *Expander) defaultRef(repository string) string {
	branch, known, err := e.Store.DefaultBranch(repository)
	if err != nil || !known {
		return ""
	}
	return "refs/heads/" + branch
}

// sarifComponent is a SARIF tool component and the rules it lists, which are the rules that ran.
type sarifComponent struct {
	Name            string `json:"name"`
	SemanticVersion string `json:"semanticVersion"`
	Rules           []struct {
		ID string `json:"id"`
	} `json:"rules"`
}

// answeredRules reads which lane rules a document ran, and what ran each. A rule id names one query
// whatever pack or suite brought it, so neither has to be the lane's; the driver is no pack.
func answeredRules(path string, planned tierPlan) (map[string]store.QueryRunRule, string) {
	_, runs, err := readRuns(path, func(string) bool { return true })
	if err != nil || len(runs) == 0 {
		return nil, refusedUnreadable
	}
	lane := map[string]bool{}
	for _, r := range planned.findingsRules {
		lane[r.RuleID] = true
	}
	answered := map[string]store.QueryRunRule{}
	for _, raw := range runs {
		var run struct {
			Tool struct {
				Driver     sarifComponent   `json:"driver"`
				Extensions []sarifComponent `json:"extensions"`
			} `json:"tool"`
		}
		if err := json.Unmarshal(raw, &run); err != nil {
			return nil, refusedUnreadable
		}
		cli := run.Tool.Driver.SemanticVersion
		driver := run.Tool.Driver
		driver.Name, driver.SemanticVersion = "", ""
		for _, c := range append([]sarifComponent{driver}, run.Tool.Extensions...) {
			for _, r := range c.Rules {
				if lane[r.ID] {
					answered[r.ID] = store.QueryRunRule{
						RuleID: r.ID, Pack: c.Name, PackVersion: c.SemanticVersion, CLIVersion: cli}
				}
			}
		}
	}
	if len(answered) == 0 {
		return nil, refusedNoRules
	}
	return answered, ""
}

// evaluateRest evaluates the lane rules code scanning did not answer, returning where, or empty for none.
func (e *Expander) evaluateRest(
	ctx context.Context, dbDir string, planned tierPlan, answered map[string]bool, sarifPath string,
) (string, error) {
	var rest []string
	lane := map[string]bool{}
	for i, r := range planned.findingsRules {
		lane[r.RuleID] = true
		if !answered[r.RuleID] {
			rest = append(rest, planned.findingsPaths[i])
		}
	}
	if len(rest) == 0 {
		return "", nil
	}
	if err := e.Analyzer.AnalyzeQueries(ctx, dbDir, rest, sarifPath); err != nil {
		return "", err
	}
	return sarifPath, keepResults(sarifPath, func(rule string) bool { return lane[rule] && !answered[rule] })
}

// takenRules records each rule code scanning answered as what produced it, in place of what the lane would have run.
func takenRules(rules []store.QueryRunRule, answered map[string]store.QueryRunRule, from string) []store.QueryRunRule {
	out := slices.Clone(rules)
	for i, r := range out {
		if a, ok := answered[r.RuleID]; ok {
			a.TakenFrom = from
			out[i] = a
		}
	}
	return out
}

// keepResults rewrites a SARIF document keeping only the results whose rule keep admits, each run otherwise whole.
func keepResults(path string, keep func(rule string) bool) error {
	doc, runs, err := readRuns(path, keep)
	if err != nil {
		return err
	}
	if doc["runs"], err = rawJSON(runs); err != nil {
		return err
	}
	body, err := rawJSON(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o644)
}

// readRuns reads a SARIF document and its runs, each keeping only the results whose rule keep admits.
func readRuns(path string, keep func(rule string) bool) (map[string]json.RawMessage, []json.RawMessage, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc map[string]json.RawMessage
	var runs []map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := json.Unmarshal(doc["runs"], &runs); err != nil {
		return nil, nil, fmt.Errorf("reading the runs in %s: %w", path, err)
	}
	out := make([]json.RawMessage, 0, len(runs))
	for _, run := range runs {
		var results []json.RawMessage
		if raw, ok := run["results"]; ok {
			if err := json.Unmarshal(raw, &results); err != nil {
				return nil, nil, fmt.Errorf("reading the results in %s: %w", path, err)
			}
		}
		kept := []json.RawMessage{}
		for _, result := range results {
			var named struct {
				RuleID string `json:"ruleId"`
				Rule   struct {
					ID string `json:"id"`
				} `json:"rule"`
			}
			if err := json.Unmarshal(result, &named); err != nil {
				return nil, nil, fmt.Errorf("reading a result in %s: %w", path, err)
			}
			if keep(cmp.Or(named.RuleID, named.Rule.ID)) {
				kept = append(kept, result)
			}
		}
		if run["results"], err = rawJSON(kept); err != nil {
			return nil, nil, err
		}
		raw, err := rawJSON(run)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, raw)
	}
	return doc, out, nil
}

// rawJSON encodes v without escaping HTML, which would rewrite the snippets results carry.
func rawJSON(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}
