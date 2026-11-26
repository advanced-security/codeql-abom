package authz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

// The envelope is DSSE's, so the bytes signed are the spec's pre-authentication encoding and a
// verifier written against the spec, not against this package, reads what ABOM signs.
func TestTheSignedBytesAreDSSEsPreAuthenticationEncoding(t *testing.T) {
	got := string(PAE("http://example.com/HelloWorld", []byte("hello world")))
	if want := "DSSEv1 29 http://example.com/HelloWorld 11 hello world"; got != want {
		t.Errorf("PAE = %q, want the spec's %q", got, want)
	}
}

func signer(t *testing.T) (*LocalSigner, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "authz")
	s, err := OpenLocal(dir, t0)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func bundle(t *testing.T, dir string) Bundle {
	t.Helper()
	b, err := ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The key is made once, beside the database, readable by its owner alone, and the bundle that
// verifies with it names it; a second start uses the same key rather than minting another.
func TestTheLocalKeyIsMadeOnceAndOnlyItsOwnerCanReadIt(t *testing.T) {
	s, dir := signer(t)
	info, err := os.Stat(filepath.Join(dir, "decision.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the decision key is %v, want 0600", info.Mode().Perm())
	}
	again, err := OpenLocal(dir, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.KeyID() != s.KeyID() || !strings.HasPrefix(s.KeyID(), "sha256:") {
		t.Errorf("key ids %s and %s, want one sha256: id across starts", s.KeyID(), again.KeyID())
	}
	if keys := bundle(t, dir).Keys; len(keys) != 1 || keys[0].KeyID != s.KeyID() {
		t.Errorf("the bundle holds %+v, want the one decision key", keys)
	}
}

func statement(t *testing.T) Statement {
	t.Helper()
	predicate, err := json.Marshal(map[string]string{"verdict": "allow"})
	if err != nil {
		t.Fatal(err)
	}
	return Statement{Subject: []Subject{{Name: "octo", Digest: map[string]string{"sha256": strings.Repeat("a", 64)}}},
		PredicateType: PredicateDecision, Predicate: predicate}
}

// What was signed opens only with a key the bundle trusts, at a time that key was trusted, and only
// as it was signed: a changed payload, an unknown key or a key outside its window is refused.
func TestAnEnvelopeOpensOnlyAsSignedByAKeyTrustedAtTheTime(t *testing.T) {
	s, dir := signer(t)
	env, err := Seal(context.Background(), s, statement(t))
	if err != nil {
		t.Fatal(err)
	}
	b := bundle(t, dir)

	got, err := b.Open(env, t0.Add(time.Minute))
	if err != nil || got.PredicateType != PredicateDecision || got.Type != StatementType {
		t.Fatalf("opening what was just signed: %+v, %v", got, err)
	}

	tampered := env
	payload, _ := base64.StdEncoding.DecodeString(env.Payload)
	tampered.Payload = base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(payload), "allow", "refuse", 1)))
	if _, err := b.Open(tampered, t0.Add(time.Minute)); err == nil {
		t.Error("a changed payload opened")
	}
	if _, err := b.Open(env, t0.Add(-time.Minute)); err == nil {
		t.Error("an envelope opened at a time before its key was trusted")
	}
	other, _ := signer(t)
	foreign, err := Seal(context.Background(), other, statement(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(foreign, t0.Add(time.Minute)); err == nil {
		t.Error("an envelope signed by a key the bundle does not name opened")
	}
}

// A rotation names a new key and keeps the old one trusted for a window, so what it signed before
// still verifies while the window lasts and nothing it signs afterwards does.
func TestARotatedKeyVerifiesOnlyInsideItsWindow(t *testing.T) {
	old, dir := signer(t)
	before, err := Seal(context.Background(), old, statement(t))
	if err != nil {
		t.Fatal(err)
	}
	rotated := t0.Add(24 * time.Hour)
	fresh, err := RotateLocal(dir, &memLedger{}, time.Hour, rotated)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.KeyID() == old.KeyID() {
		t.Fatal("rotation kept the same key")
	}
	b := bundle(t, dir)
	if _, err := b.Open(before, rotated.Add(30*time.Minute)); err != nil {
		t.Errorf("the old key's envelope inside the window: %v", err)
	}
	if _, err := b.Open(before, rotated.Add(2*time.Hour)); err == nil {
		t.Error("the old key's envelope opened after its window closed")
	}
	after, err := Seal(context.Background(), fresh, statement(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(after, rotated.Add(2*time.Hour)); err != nil {
		t.Errorf("the new key's envelope: %v", err)
	}
	reopened, err := OpenLocal(dir, rotated.Add(3*time.Hour))
	if err != nil || reopened.KeyID() != fresh.KeyID() {
		t.Errorf("a start after rotation signs with %v, %v; want the new key", reopened, err)
	}
}
