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
	if _, err := b.DecisionOf(ledger.entries[3], ledger, nil); err == nil {
		t.Error("the forged decision opens as a logged one")
	}
	// A row can be inserted at a position of its own choosing, before the log's first; the
	// append-only triggers refuse only changes to rows already there.
	early := ledger.entries[3]
	for _, seq := range []int64{0, -1} {
		early.Seq = seq
		if _, err := b.DecisionOf(early, ledger, nil); err == nil {
			t.Errorf("the forged decision opens as a logged one placed at %d, before the log begins", seq)
		}
	}
	if _, err := b.DecisionOf(ledger.entries[0], ledger, nil); err != nil {
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

// A key is made for a database nothing has decided yet. One made in an empty directory beside a log
// another key already signed would sign what no bundle trusting the log verifies, and the log
// cannot be rewritten, so a directory that is not the log's own is refused.
func TestNoKeyIsMadeBesideALogAnotherKeySigned(t *testing.T) {
	ledger := &memLedger{}
	if _, err := OpenSigner(context.Background(), filepath.Join(t.TempDir(), "authz"), ledger, t0); err != nil {
		t.Fatalf("no key was made for a database nothing has decided: %v", err)
	}
	old, _ := signer(t)
	decideOn(t, old, ledger, t0.Add(time.Minute))

	elsewhere := filepath.Join(t.TempDir(), "authz")
	if _, err := OpenSigner(context.Background(), elsewhere, ledger, t0.Add(time.Hour)); err == nil {
		t.Fatal("a new key was made beside a log another key signed")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, keyFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the refused directory holds a key anyway: %v", err)
	}
}

// countingLog counts the reads that verifying an entry's place in the log makes.
type countingLog struct {
	*memLedger
	reads int
}

func (c *countingLog) Assertions(after int64, limit int) ([]Entry, error) {
	c.reads++
	return c.memLedger.Assertions(after, limit)
}

// rotatedLog is three decisions the first key signed, the rotation's checkpoint the key replacing it
// signed, and a decision of the new key's; with the first key, as a copy of it would sign.
func rotatedLog(t *testing.T) (*memLedger, Bundle, Signer) {
	t.Helper()
	old, dir := signer(t)
	ledger := &memLedger{}
	for i := 1; i <= 3; i++ {
		decideOn(t, old, ledger, t0.Add(time.Duration(i)*time.Minute))
	}
	fresh, err := RotateLocal(dir, ledger, 7*24*time.Hour, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	decideOn(t, fresh, ledger, t0.Add(2*time.Hour))
	return ledger, bundle(t, dir), old
}

// A copy of a retired key cannot rewrite what the key signed while it decided. Its signature
// verifies at every position the key was trusted for, so only the chain the replacing key
// checkpointed tells a rewritten row from the original, and a gate acting on the row checks it
// there rather than leaving it to `audit verify`.
func TestARowRewrittenWhereARetiredKeyWasTrustedIsRefusedByTheChain(t *testing.T) {
	ledger, b, stolen := rotatedLog(t)
	for _, e := range ledger.entries[:3] {
		if _, err := b.DecisionOf(e, ledger, nil); err != nil {
			t.Fatalf("entry %d, which the retired key signed while it decided, does not verify: %v", e.Seq, err)
		}
	}
	scratch := &memLedger{}
	forger := &Point{Signer: stolen, Ledger: scratch, Now: func() time.Time { return t0.Add(2 * time.Minute) }}
	widened := declare
	widened.Subject = "octo,evil"
	if _, err := forger.Decide(context.Background(), widened); err != nil {
		t.Fatal(err)
	}

	// The row alone, with the append-only triggers out of the way: the chain after it is what the
	// checkpoint pins.
	ledger.entries[1].Digest, ledger.entries[1].Envelope = scratch.entries[0].Digest, scratch.entries[0].Envelope
	if _, err := b.DecisionOf(ledger.entries[1], ledger, nil); err == nil || !strings.Contains(err.Error(), "chain") {
		t.Errorf("a decision rewritten where the retired key was trusted opens: %v", err)
	}

	// Recomputing the chain from the row moves every value after it off the one the checkpoint names.
	prev := ledger.entries[0].Chain
	for i := 1; i < 3; i++ {
		ledger.entries[i].Chain = NextChain(prev, ledger.entries[i].Digest)
		prev = ledger.entries[i].Chain
	}
	if _, err := b.DecisionOf(ledger.entries[1], ledger, nil); err == nil {
		t.Error("a decision rewritten where the retired key was trusted, the chain recomputed after it, opens")
	}
}

// A retired key's entry no checkpoint by a key still deciding covers is not honored: until one
// names a head at or after it, nothing still trusted vouches for the chain through it.
func TestARetiredKeysEntryNoCheckpointCoversIsRefused(t *testing.T) {
	old, dir := signer(t)
	ledger := &memLedger{}
	decideOn(t, old, ledger, t0.Add(time.Minute))
	b := bundle(t, dir)
	b.Keys[0].NotAfter, b.Keys[0].LastSeq = t0.Add(time.Hour).Format(time.RFC3339Nano), 1
	if _, err := b.DecisionOf(ledger.entries[0], ledger, nil); err == nil || !strings.Contains(err.Error(), "checkpoint") {
		t.Errorf("a retired key's entry no checkpoint covers opens: %v", err)
	}
	if _, err := b.DecisionOf(ledger.entries[0], nil, nil); err == nil {
		t.Error("a retired key's entry opens with no log to read the chain from")
	}
}

// The scope gate verifies the declaration on every ownership read, so the chain from an entry a
// retired key signed is walked once, and what the key still deciding signed reads nothing more.
func TestTheChainFromARetiredKeysEntryIsWalkedOnce(t *testing.T) {
	ledger, b, _ := rotatedLog(t)
	log := &countingLog{memLedger: ledger}
	var pins Pins
	if _, err := b.DecisionOf(ledger.entries[4], log, &pins); err != nil || log.reads != 0 {
		t.Errorf("a decision of the key still deciding read the log %d times: %v", log.reads, err)
	}
	if _, err := b.DecisionOf(ledger.entries[1], log, &pins); err != nil {
		t.Fatal(err)
	}
	walked := log.reads
	if walked == 0 {
		t.Fatal("a decision the retired key signed opened without its chain being read")
	}
	for range 3 {
		if _, err := b.DecisionOf(ledger.entries[1], log, &pins); err != nil {
			t.Fatal(err)
		}
	}
	if log.reads != walked {
		t.Errorf("opening the same decision three more times read the log %d more times, want none", log.reads-walked)
	}
}

// A checkpoint vouches for the chain only when a key still deciding signed it. Copies of two
// retired keys could otherwise rewrite a decision the first signed and checkpoint the result with
// the second, which was trusted at that position too.
func TestOnlyACheckpointByAKeyStillDecidingVouchesForTheChain(t *testing.T) {
	first, dir := signer(t)
	ledger := &memLedger{}
	decideOn(t, first, ledger, t0.Add(time.Minute))
	decideOn(t, first, ledger, t0.Add(2*time.Minute))
	second, err := RotateLocal(dir, ledger, 7*24*time.Hour, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	decideOn(t, second, ledger, t0.Add(2*time.Hour))
	if _, err := RotateLocal(dir, ledger, 7*24*time.Hour, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	b := bundle(t, dir)
	if _, err := b.DecisionOf(ledger.entries[1], ledger, nil); err != nil {
		t.Fatalf("the first key's decision, under two rotations, does not verify: %v", err)
	}

	scratch := &memLedger{}
	widened := declare
	widened.Subject = "octo,evil"
	forger := &Point{Signer: first, Ledger: scratch, Now: func() time.Time { return t0.Add(2 * time.Minute) }}
	if _, err := forger.Decide(context.Background(), widened); err != nil {
		t.Fatal(err)
	}
	ledger.entries[1].Digest, ledger.entries[1].Envelope = scratch.entries[0].Digest, scratch.entries[0].Envelope
	ledger.entries[1].Chain = NextChain(ledger.entries[0].Chain, ledger.entries[1].Digest)
	vouching := &Point{Signer: second, Ledger: scratch, Now: func() time.Time { return t0.Add(time.Hour) }}
	if err := vouching.checkpoint(context.Background(), ledger.entries[1], t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	forged := scratch.entries[len(scratch.entries)-1]
	ledger.entries[2].Digest, ledger.entries[2].Envelope = forged.Digest, forged.Envelope
	ledger.entries[2].Chain = NextChain(ledger.entries[1].Chain, forged.Digest)
	if _, err := b.DecisionOf(ledger.entries[1], ledger, nil); err == nil {
		t.Error("a decision rewritten with one retired key and checkpointed with another opens")
	}
}
