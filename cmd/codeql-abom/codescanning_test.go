package main

import (
	"context"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// The crawl asks code scanning through the response cache, and falls back to the build rather than wait out a limit.
func TestTheCrawlAsksCodeScanningThroughTheResponseCacheWithoutWaiting(t *testing.T) {
	loop := &supervisor.Supervisor{Expander: &expand.Expander{}}

	wireCodeScanning(context.Background(), loop, newTestStore(t), "https://api.example.test", nil)

	client, ok := loop.Expander.CodeScanning.(*catalog.Client)
	if !ok || client == nil {
		t.Fatalf("the crawl asks code scanning nothing: %#v", loop.Expander.CodeScanning)
	}
	if client.Cache == nil {
		t.Error("code scanning is asked without the response cache, so every unchanged answer costs quota")
	}
	if client.MaxRetries != 0 {
		t.Errorf("a code scanning read retries %d times; it should fall back to the build", client.MaxRetries)
	}
}
