package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSanitizationKeepsValidBoundedJSONWithoutSourceOrCredentials(t *testing.T) {
	body := Detail(map[string]any{
		"args": map[string]any{"token": "credential-canary", "url": "https://user:password@example.com/a?token=url-canary",
			"file_text": "source-canary", "path": "/Users/operator/private/query.ql"},
		"result": strings.Repeat("字", 20000),
	})
	if !json.Valid([]byte(body)) || len(body) > MaxDetail {
		t.Fatalf("invalid or oversized detail: %d", len(body))
	}
	for _, secret := range []string{"credential-canary", "url-canary", "source-canary", "password", "/Users/operator"} {
		if strings.Contains(body, secret) {
			t.Errorf("leaked %s", secret)
		}
	}
	if !strings.Contains(body, "omitted") || !strings.Contains(body, "truncated") {
		t.Fatalf("missing coverage markers: %s", body)
	}
}
