package main

import (
	"bytes"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/frontier"
)

// A server start is not somebody asking for a crawl. It once put every unobserved commit back at
// once, 2,456 rows on the crawled organization; re-observing them belongs to a running cycle, one
// claim's worth at a time.
func TestAServerStartPutsNothingBackToReobserve(t *testing.T) {
	s := unobservedClosure(t)
	if n, err := s.UnobservedCommitCount(); err != nil || n != 2 {
		t.Fatalf("the fixture has %d unobserved commits, %v; want vendor/comp's 2", n, err)
	}
	queue := &frontier.Queue{Store: s, MaxDepth: frontier.DefaultMaxDepth}
	var out, errs bytes.Buffer

	prepareQueue(s, queue, &out, &errs)
	reclaimAbandonedWork(s, queue, &out, &errs)

	var moved int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier
		WHERE node_key = 'vendor/comp' AND status <> 'done'`).Scan(&moved); err != nil {
		t.Fatal(err)
	}
	if moved != 0 {
		t.Errorf("a server start put %d of vendor/comp's rows back:\n%s%s", moved, out.String(), errs.String())
	}
}
