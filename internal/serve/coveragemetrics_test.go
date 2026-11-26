package serve

import (
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// An alert is only as good as the series it reads. Every tier and state is a series, zero included,
// so "more than zero stuck" can fire and clear; a time nobody measured is no series at all, never a
// zero that reads as 1970; and an unavailable row says whether access or a gone reference is why.
func TestMetricsCountTheQueueByTierStateAndCauseAndSayWhichAccountsAreUnread(t *testing.T) {
	srv, s := newServer(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{{Owner: "octo", Kind: store.AccountOrganization, Enabled: true}},
		store.DeclaredByInterface); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`
		INSERT INTO abom_frontier (node_key, owner, repo, ref, depth, status, score, enqueued_at, attempts, completed_at, reason)
		VALUES ('a/stuck','a','stuck','v1',1,'pending',1,'2026-01-01',?,NULL,'exit status 32'),
		       ('b/private','b','private','v1',2,'unavailable',1,'2026-01-01',0,'2026-02-03T04:05:06Z',
		        'dependency unavailable: cloning https://github.com/b/private.git at "v1": remote: Repository not found.'),
		       ('b/later','b','later','v1',2,'unavailable',1,'2026-01-01',0,'2026-02-04T00:00:00Z',
		        'dependency unavailable: cloning https://github.com/b/later.git at "v1": remote: Repository not found.'),
		       ('b/empty','b','empty','v1',2,'unavailable',1,'2026-01-01',0,NULL,
		        'dependency unavailable: empty has no workflows or action manifest')`,
		store.DefaultStuckAfter); err != nil {
		t.Fatal(err)
	}

	rec := get(t, srv, "/metrics")
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want Prometheus text exposition", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`abom_frontier_rows{tier="direct",state="stuck"} 1`,
		`abom_frontier_rows{tier="direct",state="retrying"} 0`,
		`abom_frontier_rows{tier="owned",state="unavailable"} 0`,
		`abom_frontier_rows{tier="transitive",state="unavailable"} 3`,
		`abom_frontier_unavailable_rows{tier="transitive",cause="access"} 2`,
		`abom_frontier_unavailable_rows{tier="transitive",cause="other"} 1`,
		`abom_frontier_unavailable_rows{tier="transitive",cause="absent"} 0`,
		`abom_frontier_unavailable_oldest_timestamp_seconds{tier="transitive",cause="access"} 1770091506`,
		`abom_account_unread{account="octo"} 1`,
		"# TYPE abom_frontier_rows gauge",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("/metrics lacks %q:\n%s", want, body)
		}
	}
	for _, never := range []string{
		`abom_frontier_unavailable_oldest_timestamp_seconds{tier="transitive",cause="other"}`,
		`abom_frontier_unavailable_oldest_timestamp_seconds{tier="transitive",cause="absent"}`,
		`abom_account_last_read_timestamp_seconds{account="octo"}`,
	} {
		if strings.Contains(body, never) {
			t.Errorf("/metrics carries %s, which nothing measured:\n%s", never, body)
		}
	}
}
