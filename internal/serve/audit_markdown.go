package serve

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/audit"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// The guide is a presentation of the same fixed-cutoff evidence as JSONL. It reads
// no referenced file, follows no artifact URL and derives no progress verdict.
type auditGuide struct {
	w   io.Writer
	err error
}

func (g *auditGuide) printf(format string, args ...any) {
	if g.err == nil {
		_, g.err = fmt.Fprintf(g.w, format, args...)
	}
}

func guideCode(value string) string {
	if value == "" {
		return "Not recorded"
	}
	// Markdown inside an HTML element is still Markdown, so the value is escaped as any recorded
	// text is; the element only sets it in monospace.
	return "<code>" + md(value) + "</code>"
}

func guideCredits(p *audit.Policy) string {
	if !p.CreditsKnown {
		return "AI credit cap not recorded"
	}
	if p.MaxAICredits == nil {
		return "No AI credit cap (Copilot plan limits still apply)"
	}
	return fmt.Sprintf("Capped at %g AI credits, enforced by Copilot", *p.MaxAICredits)
}

func guideCondition(p *audit.Policy) string {
	if p.Bounds.QueryHeld == "" {
		return "No query hold recorded in this policy."
	}
	return p.Bounds.QueryHeld
}

func guideBoundaryList(values []string) string {
	if len(values) == 0 {
		return "No entries recorded"
	}
	return strings.Join(values, "; ")
}

func (g *auditGuide) policy(p *audit.Policy) {
	g.printf("| Limit / boundary | Recorded value |\n| --- | --- |\n")
	timeout := "Not recorded"
	if p.TimeoutMs > 0 {
		timeout = fmt.Sprintf("%g min (%s)", float64(p.TimeoutMs)/60000, p.TimeoutSource)
	}
	mcpTimeout := "Not configured in this policy"
	if p.MCPTimeoutMs > 0 {
		mcpTimeout = fmt.Sprintf("%g min", float64(p.MCPTimeoutMs)/60000)
	}
	for _, row := range [][2]string{
		{"Session timeout", timeout}, {"AI credits", guideCredits(p)}, {"Deadline", p.Deadline},
		{"MCP call timeout", mcpTimeout}, {"Target", p.Bounds.Target},
		{"Conditions", guideCondition(p)}, {"Read-only", guideBoundaryList(p.Bounds.ReadOnly)},
		{"Denied scope", guideBoundaryList(p.Bounds.Denied)}, {"Excluded from edits", guideBoundaryList(p.Bounds.ExcludedNames)},
	} {
		g.printf("| %s | %s |\n", row[0], guideCode(row[1]))
	}
	g.printf("\n**Available tools** (still subject to bounds): ")
	for i, tool := range p.Tools {
		if i > 0 {
			g.printf(", ")
		}
		g.printf("%s", guideCode(tool))
	}
	g.printf("\n\n**Editable paths:**\n\n")
	if len(p.Bounds.Writable) == 0 {
		g.printf("No editable files in this policy.\n\n")
	}
	for _, path := range sortedAuditKeys(p.Bounds.Writable) {
		role := p.Bounds.Writable[path]
		if role == "" {
			role = "query, subject to the recorded conditions"
		}
		g.printf("- %s — %s\n", guideCode(path), guideCode(role))
	}
	g.printf("\nFixture root: %s. Extensions: %s.\n\n", guideCode(p.Bounds.FixtureRoot), guideCode(strings.Join(p.Bounds.FixtureExtensions, ", ")))
	g.printf("**Runtime identities:** model %s", guideCode(p.Model))
	for _, key := range sortedAuditKeys(p.Runtime) {
		g.printf("; %s: %s", md(key), guideCode(p.Runtime[key]))
	}
	g.printf(". Other versions were not recorded.\n\n")
}

type guideInput struct{ name, value string }

func guideInputs(e store.AuthoringEvent) []guideInput {
	var values []guideInput
	for _, key := range sortedAuditKeys(e.Capture.Inputs) {
		values = append(values, guideInput{key, e.Capture.Inputs[key]})
	}
	if test := e.Capture.Test; test != nil {
		values = append(values, guideInput{"Query version", test.QueryVersion}, guideInput{"Test input digest", test.TestDigest}, guideInput{"Fixture digest", test.FixturesDigest})
	}
	return values
}

func sortedAuditKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

type auditGuideIndex struct {
	seen           map[string]bool
	additional     bool
	iterations     []store.AuthoringEvent
	moreIterations bool
}

func (index *auditGuideIndex) observe(g *auditGuide, e store.AuthoringEvent) error {
	if e.Capture.Test != nil {
		if len(index.iterations) < 48 {
			index.iterations = append(index.iterations, e)
		} else {
			index.moreIterations = true
		}
	}
	for _, input := range guideInputs(e) {
		key := input.name + "\x00" + input.value
		if index.seen[key] {
			continue
		}
		if len(index.seen) >= 48 {
			index.additional = true
			continue
		}
		index.seen[key] = true
		g.printf("| %s | %s | [Event #%d](/codeql?tab=audit&session=%d&event=%d) |\n", md(input.name), guideCode(input.value), e.RowID, e.SessionID, e.RowID)
	}
	return g.err
}

func (s *Server) writeAuditGuide(w http.ResponseWriter, r *http.Request, meta store.AuditSession, f store.AuditFilter, page store.AuditPage) {
	g := &auditGuide{w: w}
	g.printf("# Session %d — recorded reproduction evidence\n\n", meta.RowID)
	g.printf("Fixed cutoff: event #%d; %d matching events. %s\n\n", page.Through, page.Total, reproductionLimits)
	g.printf("## Manual reproduction checklist\n\n" +
		"1. Read the originating request and the policy applicable to each recorded event.\n" +
		"2. Obtain the recorded query, pack dependencies, help, marked fixtures and expected results independently. Compare the identities below; source and scratch are not archived.\n" +
		"3. Use the recorded CodeQL CLI and tool arguments to prepare an equivalent manual operation. Inspect refusals first; this guide neither authorizes nor executes those operations.\n" +
		"4. Compare each measured test result, failure stage and missing/unexpected count. A tool return or model message is not proof of a passing test.\n\n")
	g.printf("## Session and limits\n\nPurpose: %s. Current recorded state: %s. Started by: %s.\n\n", md(meta.Purpose), md(meta.State), md(meta.StartedBy))
	var latest *store.AuthoringEvent
	for i := range meta.Policies {
		if meta.Policies[i].Capture.Policy != nil && (latest == nil || meta.Policies[i].RowID > latest.RowID) {
			latest = &meta.Policies[i]
		}
	}
	if latest == nil {
		g.printf("Limits and permissions were not recorded. Current settings cannot describe this historical session.\n\n")
	} else {
		g.printf("Latest policy at this cutoff: [event #%d](/codeql?tab=audit&session=%d&event=%d). Earlier steps retain their own policy references.\n\n", latest.RowID, meta.RowID, latest.RowID)
		g.policy(latest.Capture.Policy)
	}
	for _, relation := range meta.Relations {
		g.printf("- %s:", md(relation.Kind))
		if relation.RequestID > 0 {
			g.printf(" [request #%d](/codeql?tab=requests&request=%d)", relation.RequestID, relation.RequestID)
		}
		if relation.RunID > 0 {
			g.printf(" [run #%d](/codeql?tab=runs&run=%d)", relation.RunID, relation.RunID)
		}
		if relation.QueryID != "" {
			g.printf(" %s; version %s", guideCode(relation.QueryID), guideCode(relation.Version))
		}
		g.printf("\n")
	}
	g.printf("\n## Required inputs\n\nThese are identities or references, not recovered source. Availability and missing-input notes are retained verbatim. Pack dependencies, help and expected results without recorded identities must be obtained and verified independently.\n\n" +
		"| Input / reference | Recorded digest or value | Evidence |\n| --- | --- | --- |\n")
	// A compact index is bounded independently of the number of events. Every
	// step below still carries its own full input observations.
	index := auditGuideIndex{seen: map[string]bool{}}
	if page.Through > 0 {
		err := s.Store.WalkAudit(f, func(e store.AuthoringEvent) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			return index.observe(g, e)
		})
		if err != nil {
			return
		}
	}
	if len(index.seen) == 0 {
		g.printf("| Required query, fixtures, expectations and dependencies | No input identities recorded | See the original request |\n")
	}
	if index.additional {
		g.printf("\nThe compact index shows 48 distinct references. Additional input observations remain with their steps below; they are not omitted from this guide.\n")
	}
	g.printf("\n## Measured test iterations\n\n")
	if len(index.iterations) == 0 {
		g.printf("No measured test iteration matches these filters. Tool-return events and model messages are not substitutes.\n")
	} else {
		g.printf("| Evidence | Outcome | Compared tests | Query version | CodeQL CLI |\n| --- | --- | --- | --- | --- |\n")
		for _, e := range index.iterations {
			test := e.Capture.Test
			g.printf("| [Event #%d](/codeql?tab=audit&session=%d&event=%d) | %s | %d of %d passed | %s | %s |\n",
				e.RowID, e.SessionID, e.RowID, guideTestOutcome(test), test.Total-test.Failed, test.Total, guideCode(test.QueryVersion), guideCode(test.CLIVersion))
		}
	}
	if index.moreIterations {
		g.printf("\nThis compact index shows the first 48 matching iterations. Every iteration, including the remainder, is retained with its step below.\n")
	}
	g.printf("\n## Recorded steps and test iterations\n\nAll matching events follow the selected order. Append IDs establish sequence; ordinary hook observations are not guessed tool-call pairs. Refused operations are evidence to inspect, not instructions to repeat.\n\n")
	count := 0
	if page.Through > 0 {
		err := s.Store.WalkAudit(f, func(e store.AuthoringEvent) error {
			if err := r.Context().Err(); err != nil {
				return err
			}
			count++
			g.event(e, count)
			return g.err
		})
		if err != nil {
			return
		}
	}
	if g.err != nil || r.Context().Err() != nil || count != page.Total {
		return
	}
	terms := r.URL.Query()
	terms.Del("download")
	terms.Del("cursor")
	terms.Set("session", strconv.FormatInt(meta.RowID, 10))
	terms.Set("through", strconv.FormatInt(page.Through, 10))
	g.printf("## Technical evidence appendix\n\n[Download the full filtered JSONL snapshot](/api/authoring-events/export?%s). Each event link above retains access to the complete sanitized envelope and its policy. No new source archive is included.\n\n", terms.Encode())
	g.printf("<!-- audit-complete: through=%d events=%d -->\n", page.Through, count)
}

func (g *auditGuide) event(e store.AuthoringEvent, number int) {
	g.printf("### %d. %s — %s\n\n[Event #%d](/codeql?tab=audit&session=%d&event=%d) · %s · policy %s\n\n", number, guideCode(e.Tool), md(e.Decision), e.RowID, e.SessionID, e.RowID, md(e.At), guidePolicyID(e.PolicyID))
	g.printf("Recorded observation: %s. Step: %s. Subject: %s.\n\n", guideCode(e.Hook), guideCode(e.Step), guideCode(e.Subject))
	if e.Reason != "" {
		g.printf("Recorded reason: %s\n\n", md(e.Reason))
	}
	if p := e.Capture.Policy; p != nil {
		g.printf("Policy snapshot: %g min timeout; %s. Conditions: %s\n\nRead the linked event for its complete exact tool and file restrictions.\n\n", float64(p.TimeoutMs)/60000, md(guideCredits(p)), md(guideCondition(p)))
	}
	if test := e.Capture.Test; test != nil {
		g.printf("**Measured test: %s — %d of %d passed.**\n\n", guideTestOutcome(test), test.Total-test.Failed, test.Total)
		g.printf("| Measurement | Recorded value |\n| --- | --- |\n")
		for _, row := range [][2]string{{"Query", test.QueryID}, {"Query version", test.QueryVersion}, {"CodeQL CLI", test.CLIVersion}, {"Failure stage", test.FailureStage}, {"Missing results", guideCount(test.Missing)}, {"Unexpected results", guideCount(test.Unexpected)}} {
			g.printf("| %s | %s |\n", row[0], guideCode(row[1]))
		}
		g.printf("\nRecorded diagnostic: %s\n\n", md(test.Detail))
	}
	inputs := guideInputs(e)
	if len(inputs) > 0 {
		g.printf("| Required input / reference | Recorded value |\n| --- | --- |\n")
		for _, input := range inputs {
			g.printf("| %s | %s |\n", md(input.name), guideCode(input.value))
		}
		g.printf("\n")
	}
	if e.Detail != "" {
		var detail any
		if json.Unmarshal([]byte(e.Detail), &detail) == nil {
			g.printf("Safe recorded arguments and details (not an executable command):\n\n| Field | Value |\n| --- | --- |\n")
			g.fields("", detail)
			g.printf("\n")
		} else {
			g.printf("Safe recorded detail: %s\n\n", md(e.Detail))
		}
	}
	if e.Capture.ArtifactID > 0 {
		g.printf("[Existing printed tree #%d](/api/authoring-asts/%d)\n\n", e.Capture.ArtifactID, e.Capture.ArtifactID)
	}
}

func guideTestOutcome(test *store.QueryTest) string {
	if test.Passed {
		return "Test passed"
	}
	switch strings.ToUpper(test.FailureStage) {
	case "RESULT":
		return "Result comparison failed"
	case "COMPILE", "COMPILATION":
		return "Compilation failed"
	case "EXTRACT", "EXTRACTION":
		return "Extraction failed"
	default:
		return "Test failed"
	}
}

func (g *auditGuide) fields(prefix string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range sortedAuditKeys(typed) {
			name := key
			if prefix != "" {
				name = prefix + "." + key
			}
			g.fields(name, typed[key])
		}
	case []any:
		for i, element := range typed {
			g.fields(fmt.Sprintf("%s[%d]", prefix, i), element)
		}
	default:
		g.printf("| %s | %s |\n", md(prefix), guideCode(fmt.Sprint(value)))
	}
}

func guideCount(value *int64) string {
	if value == nil {
		return "Not recorded"
	}
	return strconv.FormatInt(*value, 10)
}

func guidePolicyID(id int64) string {
	if id == 0 {
		return "not recorded"
	}
	return fmt.Sprintf("event #%d", id)
}
