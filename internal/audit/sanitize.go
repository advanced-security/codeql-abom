// Package audit defines the safe, bounded evidence shared by capture and readers.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const Version = 1
const MaxDetail = 16 * 1024

var credentials = regexp.MustCompile(`(?i)(bearer\s+\S+|(?:gh[pousr]_|github_pat_)[a-zA-Z0-9_]+|(?:token|password|secret|authorization|api[_-]?key)\s*[:=]\s*["']?[^"'\s,;]+)`)
var urls = regexp.MustCompile(`https?://[^\s"'<>]+`)
var absolute = regexp.MustCompile(`(^|[\s"'(=])((?:/[A-Za-z0-9_.~-]+){2,}|[A-Za-z]:\\[^\s"'<>]+)`)

// Text is a conservative public-text projection, not a claim that arbitrary prose is secret-free.
func Text(s string) string {
	s = credentials.ReplaceAllString(s, "[redacted credential]")
	s = urls.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[omitted URL]"
		}
		u.User, u.RawQuery, u.Fragment = nil, "", ""
		return u.String()
	})
	// URL paths are also reduced: this trail does not retain private checkout locations.
	s = absolute.ReplaceAllString(s, "${1}[path]")
	return bounded(s, 4096)
}

func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("[truncated]")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "[truncated]"
}

func Digest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func secretKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "token") || strings.Contains(k, "secret") ||
		strings.Contains(k, "password") || strings.Contains(k, "authorization") ||
		strings.Contains(k, "credential") || k == "env" || k == "environment" || k == "api_key"
}

func sourceKey(k string) bool {
	switch strings.ToLower(k) {
	case "file_text", "old_str", "new_str", "ql_code", "source", "patch", "prompt", "systemmessage", "compliant", "noncompliant", "non_compliant":
		return true
	}
	return false
}

func safe(v any, depth int) any {
	if depth > 8 {
		return "[omitted: nesting limit]"
	}
	switch x := v.(type) {
	case nil, bool, float64, int, int64:
		return v
	case string:
		return Text(x)
	case []any:
		out := make([]any, 0, min(len(x), 65))
		for i, item := range x {
			if i == 64 {
				out = append(out, "[truncated: array]")
				break
			}
			out = append(out, safe(item, depth+1))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i == 64 {
				out["_truncated"] = "object fields"
				break
			}
			name := bounded(Text(k), 128)
			switch {
			case secretKey(k):
				out[name] = "[redacted]"
			case sourceKey(k):
				body, _ := json.Marshal(x[k])
				out[name] = map[string]any{"omitted": "source or prompt body", "bytes": len(body), "sha256": Digest(body)}
			default:
				out[name] = safe(x[k], depth+1)
			}
		}
		return out
	default:
		return "[omitted: unsupported body]"
	}
}

func Detail(v any) string {
	b, err := json.Marshal(map[string]any{"schema": Version, "value": safe(v, 0)})
	if err != nil {
		return `{"schema":1,"omitted":"unsupported body"}`
	}
	if len(b) > MaxDetail {
		return fmt.Sprintf(`{"schema":1,"truncated":true,"omitted":"detail exceeds 16 KiB","sanitizedBytes":%d}`, len(b))
	}
	return string(b)
}

func ProjectDetail(s string) string {
	if s == "" {
		return ""
	}
	var v any
	if json.Unmarshal([]byte(s), &v) == nil {
		if m, ok := v.(map[string]any); ok && m["schema"] == float64(Version) {
			body, err := json.Marshal(safe(v, 0))
			if err == nil && len(body) <= MaxDetail {
				return string(body)
			}
		}
		return Detail(v)
	}
	return Detail(map[string]any{"message": s})
}

// Policy is normative: it is never passed through lossy detail truncation.
type Policy struct {
	Version       int               `json:"version"`
	Purpose       string            `json:"purpose"`
	TimeoutMs     int64             `json:"timeoutMs"`
	TimeoutSource string            `json:"timeoutSource"`
	Deadline      string            `json:"deadline"`
	CreditsKnown  bool              `json:"creditsKnown"`
	MaxAICredits  *float64          `json:"maxAiCredits"`
	Model         string            `json:"model"`
	Runtime       map[string]string `json:"runtime"`
	Tools         []string          `json:"tools"`
	MCPTimeoutMs  int               `json:"mcpTimeoutMs"`
	Bounds        Bounds            `json:"bounds"`
	Hooks         []string          `json:"hooks"`
	Enforcement   string            `json:"enforcement"`
}

type Bounds struct {
	Target            string              `json:"target"`
	Writable          map[string]string   `json:"writable"`
	FixtureRoot       string              `json:"fixtureRoot"`
	FixtureExtensions []string            `json:"fixtureExtensions"`
	ExcludedNames     []string            `json:"excludedNames"`
	ReadOnly          []string            `json:"readOnly"`
	Denied            []string            `json:"denied"`
	QueryHeld         string              `json:"queryHeld"`
	ToolArguments     map[string][]string `json:"toolArguments"`
}

func (p Policy) JSON() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	if len(b) > MaxDetail {
		return "", fmt.Errorf("the complete effective policy exceeds 16 KiB; refusing execution")
	}
	return string(b), nil
}
