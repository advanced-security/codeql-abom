package authz

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decideOn appends one allowed decision to the ledger with the signer given, at the time given.
func decideOn(t *testing.T, s Signer, l Ledger, at time.Time) {
	t.Helper()
	p := &Point{Signer: s, Ledger: l, Now: func() time.Time { return at }}
	if _, err := p.Decide(context.Background(), declare); err != nil {
		t.Fatal(err)
	}
}

// A key that retires is trusted for what the log held when it retired, by position rather than by
// the time an entry claims: whoever can write the database chooses that time, and an entry signed
// with a retired key and dated before its retirement must not pass for one it signed then.
func TestARetiredKeyVerifiesOnlyTheEntriesTheLogHeldWhenItRetired(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authz")
	start := t0
	local, err := OpenLocal(dir, start)
	if err != nil {
		t.Fatal(err)
	}
	stolen, err := readKey(filepath.Join(dir, keyFile))
	if err != nil {
		t.Fatal(err)
	}
	ledger := &memLedger{}
	decideOn(t, local, ledger, start.Add(time.Minute))
	decideOn(t, local, ledger, start.Add(2*time.Minute))

	moved := start.Add(time.Hour)
	if _, err := OpenKMS(context.Background(), newFakeKMS(t), kmsKey, dir, ledger, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, keyFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the local key is still on disk after decisions moved to KMS: %v", err)
	}
	b := bundle(t, dir)
	if r := VerifyLog(ledger.entries, b); r.Failure != nil || r.Verified != 3 || r.Checkpoints != 1 {
		t.Fatalf("after the move the log verifies as %+v; want both decisions and the new key's checkpoint over them", r)
	}

	forger, err := signerOf(stolen)
	if err != nil {
		t.Fatal(err)
	}
	// Dated after the move, so only its position can refuse it; dating it earlier is refused too, as
	// a time running backwards (TestALogWhoseRecordedTimesRunBackwardsFails).
	decideOn(t, forger, ledger, moved.Add(time.Minute))
	r := VerifyLog(ledger.entries, b)
	if r.Failure == nil || r.Failure.Seq != 4 || !strings.Contains(r.Failure.Why, "retired") {
		t.Errorf("an entry the retired key signed after retiring verifies as %+v", r)
	}
	if _, err := b.DecisionOf(ledger.entries[3]); err == nil {
		t.Error("the forged decision opens as a logged one")
	}
	if _, err := b.DecisionOf(ledger.entries[0]); err != nil {
		t.Errorf("what the local key decided before retiring no longer verifies: %v", err)
	}
}

// Times recorded in the log only move forward. One that runs backwards was written by something
// other than the decision point.
func TestALogWhoseRecordedTimesRunBackwardsFails(t *testing.T) {
	s, dir := signer(t)
	ledger := &memLedger{}
	decideOn(t, s, ledger, t0.Add(time.Hour))
	decideOn(t, s, ledger, t0.Add(time.Minute))
	r := VerifyLog(ledger.entries, bundle(t, dir))
	if r.Failure == nil || r.Failure.Seq != 2 {
		t.Errorf("a log whose second entry is recorded before its first verifies as %+v", r)
	}
}

// Every time a KMS key is opened it retires whatever else still decides, so two keys never sign side
// by side; and a key once retired is not taken back by naming it again.
func TestEachKMSKeyRetiresTheOthersAndARetiredOneStaysRetired(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authz")
	ledger := &memLedger{}
	first, second := newFakeKMS(t), newFakeKMS(t)
	a, err := OpenKMS(context.Background(), first, kmsKey, dir, ledger, t0)
	if err != nil {
		t.Fatal(err)
	}
	decideOn(t, a, ledger, t0.Add(time.Minute))
	if _, err := OpenKMS(context.Background(), second, kmsKey+"-2", dir, ledger, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	b := bundle(t, dir)
	for _, k := range b.Keys {
		if k.KeyID == a.KeyID() && (k.NotAfter == "" || k.LastSeq != 1) {
			t.Errorf("the first KMS key is %+v; want it retired after entry 1", k)
		}
		if !strings.HasPrefix(k.Origin, "kms:") {
			t.Errorf("a KMS key is recorded as from %q", k.Origin)
		}
	}
	if _, err := OpenKMS(context.Background(), first, kmsKey, dir, ledger, t0.Add(2*time.Hour)); err == nil ||
		!strings.Contains(err.Error(), "retired") {
		t.Errorf("a retired KMS key opened again: %v", err)
	}
}

// While a KMS key decides, no local key is made beside it: a process started without the KMS
// variable would otherwise sign with a new key every verifier trusts.
func TestNoLocalKeyIsMadeWhileAKMSKeyDecides(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authz")
	if _, err := OpenKMS(context.Background(), newFakeKMS(t), kmsKey, dir, &memLedger{}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLocal(dir, t0.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), KMSKeyEnv) {
		t.Errorf("a local key was opened beside a deciding KMS key: %v", err)
	}
	if keys := bundle(t, dir).Keys; len(keys) != 1 {
		t.Errorf("the bundle holds %d keys, want only the KMS key", len(keys))
	}
}

// A rotation retires the old key at the log's head and the new key checkpoints that head, so what
// the old key signed afterwards is refused and what it signed before cannot be rewritten unseen.
func TestARotationPinsTheHeadTheOldKeyRetiredAt(t *testing.T) {
	old, dir := signer(t)
	ledger := &memLedger{}
	decideOn(t, old, ledger, t0.Add(time.Minute))
	fresh, err := RotateLocal(dir, ledger, time.Hour, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := ledger.Head()
	if head.Seq != 2 || head.PredicateType != PredicateCheckpoint {
		t.Fatalf("after rotating, the head is %+v; want the new key's checkpoint", head)
	}
	var cp Envelope
	if err := json.Unmarshal(head.Envelope, &cp); err != nil {
		t.Fatal(err)
	}
	if cp.Signatures[0].KeyID != fresh.KeyID() {
		t.Errorf("the rotation's checkpoint is signed by %s, not the new key", cp.Signatures[0].KeyID)
	}
	decideOn(t, old, ledger, t0.Add(2*time.Minute))
	if r := VerifyLog(ledger.entries, bundle(t, dir)); r.Failure == nil || r.Failure.Seq != 3 {
		t.Errorf("the old key signing after the rotation verifies as %+v", r)
	}
}
