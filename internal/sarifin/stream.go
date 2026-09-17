// Package sarifin streams CodeQL SARIF into the flat rows ABOM stores.
//
// Everything here is incremental by construction. At 20k repositories a whole-document
// unmarshal is not viable: individual SARIF files reach hundreds of megabytes, and buffering
// every result before writing (as a collector-style design does) makes peak memory scale with
// the corpus rather than with the largest single result.
package sarifin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Rule is the subset of a SARIF reportingDescriptor that ABOM stores.
type Rule struct {
	ID               string
	Name             string
	Description      string
	Kind             string
	Severity         string
	SecuritySeverity string
	Tags             []string
}

// Region is a source span. Zero values mean the SARIF omitted the field.
type Region struct {
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// Result is one finding, flattened to what the ABOM graph needs to attribute it to a node.
type Result struct {
	RuleID   string
	Message  string
	Level    string
	FilePath string
	Region   Region
	Snippet  string

	// Populated only when the result carries a code flow.
	SourceSnippet string
	SinkSnippet   string
	StepCount     int

	Fingerprints map[string]string
}

// Tool identifies the analyzer that wrote a run.
type Tool struct {
	Name            string
	SemanticVersion string
}

// Handler receives streamed SARIF content. Rules arrive before any result of that run.
type Handler struct {
	OnTool   func(Tool) error
	OnRule   func(Rule) error
	OnResult func(Result) error
}

// ErrNoRuns reports SARIF containing no runs, which usually means a truncated download.
var ErrNoRuns = errors.New("sarif contains no runs")

// Stream walks every run in the document, emitting rules and then results.
//
// Results are dispatched as they are decoded and never accumulated, so peak memory tracks the
// largest single result rather than the file size.
func Stream(r io.Reader, h Handler) error {
	dec := json.NewDecoder(r)
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}

	runs := 0
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		if key != "runs" {
			if err := skipValue(dec); err != nil {
				return err
			}
			continue
		}
		if err := expectDelim(dec, '['); err != nil {
			return err
		}
		for dec.More() {
			if err := streamRun(dec, h); err != nil {
				return err
			}
			runs++
		}
		if err := expectDelim(dec, ']'); err != nil {
			return err
		}
	}
	if runs == 0 {
		return ErrNoRuns
	}
	return nil
}

func streamRun(dec *json.Decoder, h Handler) error {
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "tool":
			// Rule metadata is bounded by the query pack size, so buffering it is safe.
			if err := streamTool(dec, h); err != nil {
				return err
			}
		case "results":
			if err := streamResults(dec, h); err != nil {
				return err
			}
		default:
			if err := skipValue(dec); err != nil {
				return err
			}
		}
	}
	return expectDelim(dec, '}')
}

func streamTool(dec *json.Decoder, h Handler) error {
	var tool struct {
		Driver struct {
			Name            string    `json:"name"`
			SemanticVersion string    `json:"semanticVersion"`
			Rules           []rawRule `json:"rules"`
		} `json:"driver"`
		Extensions []struct {
			Rules []rawRule `json:"rules"`
		} `json:"extensions"`
	}
	if err := dec.Decode(&tool); err != nil {
		return fmt.Errorf("decoding tool: %w", err)
	}
	if h.OnTool != nil {
		if err := h.OnTool(Tool{
			Name: tool.Driver.Name, SemanticVersion: tool.Driver.SemanticVersion,
		}); err != nil {
			return err
		}
	}
	if h.OnRule == nil {
		return nil
	}
	for _, r := range tool.Driver.Rules {
		if err := h.OnRule(r.toRule()); err != nil {
			return err
		}
	}
	// `--sarif-group-rules-by-pack` moves rules out of the driver and into extensions.
	for _, ext := range tool.Extensions {
		for _, r := range ext.Rules {
			if err := h.OnRule(r.toRule()); err != nil {
				return err
			}
		}
	}
	return nil
}

func streamResults(dec *json.Decoder, h Handler) error {
	if err := expectDelim(dec, '['); err != nil {
		return err
	}
	for dec.More() {
		var raw rawResult
		if err := dec.Decode(&raw); err != nil {
			return fmt.Errorf("decoding result: %w", err)
		}
		if h.OnResult == nil {
			continue
		}
		if err := h.OnResult(raw.toResult()); err != nil {
			return err
		}
	}
	return expectDelim(dec, ']')
}

type rawRule struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	ShortDescription struct {
		Text string `json:"text"`
	} `json:"shortDescription"`
	FullDescription struct {
		Text string `json:"text"`
	} `json:"fullDescription"`
	Properties struct {
		Kind             string   `json:"kind"`
		Description      string   `json:"description"`
		Severity         string   `json:"problem.severity"`
		SecuritySeverity string   `json:"security-severity"`
		Tags             []string `json:"tags"`
	} `json:"properties"`
}

func (r rawRule) toRule() Rule {
	description := r.Properties.Description
	if description == "" {
		description = r.FullDescription.Text
	}
	if description == "" {
		description = r.ShortDescription.Text
	}
	name := r.Name
	if name == "" {
		name = r.ShortDescription.Text
	}
	if name == "" {
		name = r.ID
	}
	return Rule{
		ID:               r.ID,
		Name:             name,
		Description:      description,
		Kind:             r.Properties.Kind,
		Severity:         r.Properties.Severity,
		SecuritySeverity: r.Properties.SecuritySeverity,
		Tags:             r.Properties.Tags,
	}
}

type rawLocation struct {
	PhysicalLocation struct {
		ArtifactLocation struct {
			URI string `json:"uri"`
		} `json:"artifactLocation"`
		Region        rawRegion `json:"region"`
		ContextRegion rawRegion `json:"contextRegion"`
	} `json:"physicalLocation"`
}

type rawRegion struct {
	StartLine   int `json:"startLine"`
	StartColumn int `json:"startColumn"`
	EndLine     int `json:"endLine"`
	EndColumn   int `json:"endColumn"`
	Snippet     struct {
		Text string `json:"text"`
	} `json:"snippet"`
}

type rawResult struct {
	RuleID  string `json:"ruleId"`
	Level   string `json:"level"`
	Message struct {
		Text string `json:"text"`
	} `json:"message"`
	Locations []rawLocation `json:"locations"`
	CodeFlows []struct {
		ThreadFlows []struct {
			Locations []struct {
				Location rawLocation `json:"location"`
			} `json:"locations"`
		} `json:"threadFlows"`
	} `json:"codeFlows"`
	PartialFingerprints map[string]string `json:"partialFingerprints"`
}

func (r rawResult) toResult() Result {
	out := Result{
		RuleID:       r.RuleID,
		Level:        r.Level,
		Message:      r.Message.Text,
		Fingerprints: r.PartialFingerprints,
	}

	if len(r.Locations) > 0 {
		phys := r.Locations[0].PhysicalLocation
		out.FilePath = phys.ArtifactLocation.URI
		out.Region = Region{
			StartLine:   phys.Region.StartLine,
			StartColumn: phys.Region.StartColumn,
			EndLine:     phys.Region.EndLine,
			EndColumn:   phys.Region.EndColumn,
		}
		out.Snippet = strings.TrimRight(phys.Region.Snippet.Text, "\n")
		if out.Snippet == "" {
			out.Snippet = strings.TrimRight(phys.ContextRegion.Snippet.Text, "\n")
		}
	}

	// Source and sink are the endpoints of the first thread flow; sarif-sql left these NULL.
	if len(r.CodeFlows) > 0 && len(r.CodeFlows[0].ThreadFlows) > 0 {
		steps := r.CodeFlows[0].ThreadFlows[0].Locations
		out.StepCount = len(steps)
		if len(steps) > 0 {
			out.SourceSnippet = strings.TrimRight(
				steps[0].Location.PhysicalLocation.Region.Snippet.Text, "\n")
			out.SinkSnippet = strings.TrimRight(
				steps[len(steps)-1].Location.PhysicalLocation.Region.Snippet.Text, "\n")
		}
	}
	return out
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
