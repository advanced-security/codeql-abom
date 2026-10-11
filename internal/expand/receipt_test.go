package expand

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

func TestAReplayedClaimUsesItsReceiptAndRefusesChangedEvidence(t *testing.T) {
	s, err := store.OpenBackend(filepath.Join(t.TempDir(), "receipt.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	lease, held, err := s.AcquireLease("discovery", "receipt-test", time.Minute)
	if err != nil || !held {
		t.Fatal("acquiring lease", err)
	}
	fenced, err := s.WithLease(lease)
	if err != nil {
		t.Fatal(err)
	}
	e := &Expander{Store: fenced}
	path := filepath.Join(t.TempDir(), "opaque-evidence")
	if err := os.WriteFile(path, []byte("first recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := batch{owner: "acme", repo: "action", ref: "v1",
		items: []frontier.Item{{NodeKey: "acme/action", Ref: "v1", ClaimID: "durable-attempt"}}}
	prepared := work.Prepared{Commit: "commit", ResultPath: path}
	finish, recorded, err := e.beginReceipt(b, prepared, Options{QueryID: "abom/edges"}, tierPlan{})
	if err != nil || recorded || finish == nil {
		t.Fatalf("new attempt: recorded=%v, %v", recorded, err)
	}
	if err := finish(true); err != nil {
		t.Fatal(err)
	}
	finish, recorded, err = e.beginReceipt(b, prepared, Options{QueryID: "abom/edges"}, tierPlan{})
	if err != nil || !recorded || finish != nil {
		t.Fatalf("replayed attempt was not recognized: recorded=%v, %v", recorded, err)
	}
	if err := os.WriteFile(path, []byte("different recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.beginReceipt(b, prepared, Options{QueryID: "abom/edges"}, tierPlan{}); err == nil {
		t.Fatal("the same durable attempt accepted different evidence")
	}
}
