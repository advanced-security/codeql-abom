package store

import "testing"

func TestEachClaimPersistsANewAttemptIdentity(t *testing.T) {
	s := newTestStore(t)
	q := s.Frontier(FrontierOptions{})
	if _, err := q.Enqueue([]Item{{NodeKey: "vendor/action", Owner: "vendor", Repo: "action", Ref: "v1", Depth: 1}}); err != nil {
		t.Fatal(err)
	}
	prior := ""
	for i := 0; i < 2; i++ {
		items, err := q.Claim(1)
		if err != nil || len(items) != 1 {
			t.Fatalf("claim %d: %+v %v", i, items, err)
		}
		if items[0].ClaimID == "" || items[0].ClaimID == prior {
			t.Fatalf("claim %d did not get a fresh identity: %+v", i, items[0])
		}
		var stored string
		if err := s.db.QueryRow(`SELECT claim_id FROM abom_frontier WHERE node_key = ? AND ref = ?`,
			items[0].NodeKey, items[0].Ref).Scan(&stored); err != nil || stored != items[0].ClaimID {
			t.Fatalf("claim identity was not committed: %q %v", stored, err)
		}
		prior = stored
		if _, err := s.db.Exec(`UPDATE abom_frontier SET status = 'pending' WHERE node_key = 'vendor/action'`); err != nil {
			t.Fatal(err)
		}
	}
}
