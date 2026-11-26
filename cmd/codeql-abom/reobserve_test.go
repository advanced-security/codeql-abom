package main

import (
	"bytes"
	"strings"
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
	if err := reclaimAbandonedWork(s, queue, &out, &errs); err != nil {
		t.Fatal(err)
	}

	var moved int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM abom_frontier
		WHERE node_key = 'vendor/comp' AND status <> 'done'`).Scan(&moved); err != nil {
		t.Fatal(err)
	}
	if moved != 0 {
		t.Errorf("a server start put %d of vendor/comp's rows back:\n%s%s", moved, out.String(), errs.String())
	}
}

// A server start puts back what was settled unavailable for a reason no longer terminal, once, and
// says how many: a transient clone failure recorded before failures were classified would otherwise
// stay out of coverage for good. What still holds is left alone, so a second start moves nothing.
func TestAServerStartPutsBackWhatAnOutageSettledUnavailable(t *testing.T) {
	s := openStore(t)
	for key, reason := range map[string]string{
		"octo/lib":  "dependency unavailable: cloning https://github.com/octo/lib.git at \"v1\": fatal: unable to access 'https://github.com/octo/lib.git/': Could not resolve host: github.com",
		"octo/gone": "dependency unavailable: cloning https://github.com/octo/gone.git at \"v1\": fatal: Remote branch v1 not found in upstream origin",
	} {
		owner, repo, _ := strings.Cut(key, "/")
		if _, err := s.DB().Exec(`INSERT INTO abom_frontier (node_key, owner, repo, depth, status, score, enqueued_at, reason)
			VALUES (?, ?, ?, 1, 'unavailable', 1, '2026-01-01', ?)`, key, owner, repo, reason); err != nil {
			t.Fatal(err)
		}
	}
	queue := &frontier.Queue{Store: s, MaxDepth: frontier.DefaultMaxDepth}

	var out, errs bytes.Buffer
	prepareQueue(s, queue, &out, &errs)
	if !strings.Contains(out.String(), "1 dependencies settled unavailable") {
		t.Errorf("a start did not say what it put back:\n%s%s", out.String(), errs.String())
	}
	var status string
	for key, want := range map[string]string{"octo/lib": "pending", "octo/gone": "unavailable"} {
		if err := s.DB().QueryRow(`SELECT status FROM abom_frontier WHERE node_key = ?`, key).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != want {
			t.Errorf("%s = %q after a start, want %q", key, status, want)
		}
	}
	out.Reset()
	prepareQueue(s, queue, &out, &errs)
	if strings.Contains(out.String(), "settled unavailable") {
		t.Errorf("a second start put something back again:\n%s", out.String())
	}
}
