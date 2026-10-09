package main

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/supervisor"
)

// The crawl asks code scanning through the response cache, and falls back to the build rather than wait out a limit.
func TestTheCrawlAsksCodeScanningThroughTheResponseCacheWithoutWaiting(t *testing.T) {
	loop := &supervisor.Supervisor{Expander: &expand.Expander{}}

	wireCodeScanning(context.Background(), loop, newTestStore(t), serveConfig{
		apiURL: "https://api.example.test", discovery: discoveryOptions{codeScanningReuse: true}})

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

// Reuse is on unless the operator turns it off, and a server that does is the one place that can
// say so: the crawl then asks code scanning nothing, which would otherwise read as refusal.
func TestReuseIsOnUnlessTurnedOffAndThenTheCrawlSaysWhy(t *testing.T) {
	db := filepath.Join(t.TempDir(), "abom.db")
	on, err := parseServeArgs([]string{"--db", db}, io.Discard)
	if err != nil || !on.discovery.codeScanningReuse {
		t.Fatalf("serve parses reuse as %v (%v), want it on by default", on.discovery.codeScanningReuse, err)
	}
	off, err := parseServeArgs([]string{"--db", db, "--code-scanning-reuse=false"}, io.Discard)
	if err != nil || off.discovery.codeScanningReuse {
		t.Fatalf("serve parses --code-scanning-reuse=false as %v (%v)", off.discovery.codeScanningReuse, err)
	}
	if on.discovery.codeScanningWait != 10*time.Minute {
		t.Errorf("a push waits %s for code scanning by default, want 10m", on.discovery.codeScanningWait)
	}

	s, err := store.Open(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, c := range []struct {
		cfg    serveConfig
		reads  bool
		saying string
	}{{on, true, ""}, {off, false, "--code-scanning-reuse=false"}} {
		loop := &supervisor.Supervisor{Expander: &expand.Expander{}}
		wireCodeScanning(context.Background(), loop, s, c.cfg)
		if got := loop.Expander.CodeScanning != nil; got != c.reads {
			t.Errorf("with reuse %v the crawl reads code scanning: %v", c.cfg.discovery.codeScanningReuse, got)
		}
		if got := loop.Expander.CodeScanningUnasked(); (c.saying == "") != (got == "") ||
			!strings.Contains(got, c.saying) {
			t.Errorf("with reuse %v the crawl says %q, want %q", c.cfg.discovery.codeScanningReuse, got, c.saying)
		}
	}
}
