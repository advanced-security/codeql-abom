package store

import "testing"

func TestConfigurationRecoveryRequeuesOnlyPolicySkippedSources(t *testing.T) {
	s := newTestStore(t)
	for _, name := range []string{"blocked", "missing", "completed", "other"} {
		tierRow(t, s, name, 1, "unavailable")
	}
	if _, err := s.db.Exec(`UPDATE abom_frontier SET owner = 'acme', repo = 'repo',
		reason = 'source policy: acme/repo: scanning disabled' WHERE node_key IN ('blocked', 'completed')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE abom_frontier SET status = 'done' WHERE node_key = 'completed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE abom_frontier SET owner = 'acme', repo = 'repo',
		reason = 'repository missing' WHERE node_key = 'missing'`); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SourcePolicySkipped("ACME/REPO"); !got || err != nil {
		t.Fatalf("policy skipped=%v, %v", got, err)
	}
	if n, err := s.RetrySourcePolicy("acme/repo"); n != 1 || err != nil {
		t.Fatalf("requeued=%d, %v", n, err)
	}
	for name, want := range map[string]string{
		"blocked": "pending", "missing": "unavailable", "completed": "done", "other": "unavailable",
	} {
		if got := statusOfKey(t, s, name); got != want {
			t.Errorf("%s = %s, want %s", name, got, want)
		}
	}
	if got, err := s.SourcePolicySkipped("acme/repo"); got || err != nil {
		t.Fatalf("policy skipped after retry=%v, %v", got, err)
	}
}
