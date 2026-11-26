package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/coordination"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// signedLog makes three decisions with the key beside a fresh database and returns its path.
func signedLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	signer, err := authz.OpenLocal(filepath.Join(filepath.Dir(path), "authz"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	p := &authz.Point{Signer: signer, Ledger: s, CheckpointEvery: 2}
	for _, subject := range []string{"octo", "acme", "has-ghas"} {
		if _, err := p.Decide(context.Background(), authz.Request{Operation: authz.OpDeclareScope,
			Principal: authz.Principal{Kind: authz.PrincipalOperator, ID: "octocat"}, Subject: subject,
			Allow: subject != "acme", Reason: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// The log verifies offline, from the database and the trust bundle beside it, and says how much it
// verified and the head it reached.
func TestAuditVerifyWalksTheWholeLogAndNamesItsHead(t *testing.T) {
	path := signedLog(t)

	var out, errs bytes.Buffer
	if err := run(context.Background(), []string{"audit", "verify", "--db", path}, &out, &errs); err != nil {
		t.Fatalf("audit verify: %v\n%s", err, errs.String())
	}
	if said := out.String(); !strings.Contains(said, "4 assertions verified, 1 checkpoint") || !strings.Contains(said, "head ") {
		t.Errorf("audit verify said %q", said)
	}
}

// Somebody with the database can get past its trigger; the verifier still finds the entry they
// changed, says which, and fails rather than reporting what came after it as verified.
func TestAuditVerifyFailsAtAnEntryChangedUnderneathTheTrigger(t *testing.T) {
	path := signedLog(t)
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TRIGGER abom_authz_log_no_update`,
		`UPDATE abom_authz_log SET envelope = (SELECT envelope FROM abom_authz_log WHERE seq = 1) WHERE seq = 2`,
	} {
		if _, err := s.DB().Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	var out, errs bytes.Buffer
	err = run(context.Background(), []string{"audit", "verify", "--db", path}, &out, &errs)
	if err == nil || !strings.Contains(err.Error()+out.String(), "entry 2") {
		t.Errorf("audit verify over an altered log: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "4 assertions verified") {
		t.Errorf("an altered log was reported verified: %s", out.String())
	}
}

// The coordinator makes its decision key beside the database the first time it starts, readable by
// its owner alone, and the same key on every start after; what it decided before it stopped is
// covered by a checkpoint signed as it stops.
func TestAServerKeepsOneDecisionKeyBesideItsDatabaseAndCheckpointsAsItStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	first, err := openDecisionPoint(context.Background(), store.DefaultAuthzDir(path), s, &out)
	if err != nil || first == nil {
		t.Fatalf("first start: %v, %v", first, err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(path), "authz", "decision.key"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the key beside the database: %v, %v", info, err)
	}
	if _, err := first.Decide(context.Background(), authz.Request{Operation: authz.OpDeclareScope, Subject: "octo", Allow: true}); err != nil {
		t.Fatal(err)
	}
	if err := first.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, err := openDecisionPoint(context.Background(), store.DefaultAuthzDir(path), s, &out)
	if err != nil || second.Signer.KeyID() != first.Signer.KeyID() {
		t.Fatalf("a second start signs with %v, %v; want the first key", second, err)
	}
	head, ok, err := s.Head()
	if err != nil || !ok || head.PredicateType != authz.PredicateCheckpoint || head.Seq != 2 {
		t.Errorf("the head after stopping = %+v, %v, %v; want a checkpoint over the decision", head, ok, err)
	}
	if !strings.Contains(out.String(), "decisions are signed with "+first.Signer.KeyID()) {
		t.Errorf("a start did not say which key signs: %s", out.String())
	}
	if p, err := openDecisionPoint(context.Background(), "", s, &out); err != nil || p != nil {
		t.Errorf("with no directory: %v, %v; want no decision point and no error", p, err)
	}
}

// A rotation signs with the new key from now on, and the log the old one signed still verifies.
func TestAuditRotateKeyKeepsTheOldKeyTrustedForItsWindow(t *testing.T) {
	path := signedLog(t)
	dir := store.DefaultAuthzDir(path)
	before, err := authz.ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	if err := run(context.Background(), []string{"audit", "rotate-key", "--db", path, "--overlap", "48h"}, &out, &errs); err != nil {
		t.Fatalf("rotate-key: %v\n%s", err, errs.String())
	}
	after, err := authz.ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Keys) != 2 || after.Keys[0].KeyID != before.Keys[0].KeyID || after.Keys[0].NotAfter == "" ||
		after.Keys[1].NotAfter != "" {
		t.Fatalf("bundle after rotation = %+v", after.Keys)
	}
	if !strings.Contains(out.String(), after.Keys[1].KeyID) || !strings.Contains(out.String(), "still verifies") {
		t.Errorf("rotate-key said %q", out.String())
	}
	out.Reset()
	if err := run(context.Background(), []string{"audit", "verify", "--db", path}, &out, &errs); err != nil {
		t.Errorf("the log signed before rotation no longer verifies: %v\n%s", err, out.String())
	}
}

// A KMS key is rotated by naming another, so rotating the local key beside it would make a key
// nothing signs with and retire nothing that does.
func TestAuditRotateKeyRefusesWhileDecisionsAreSignedInKMS(t *testing.T) {
	path := signedLog(t)
	before, err := authz.ReadBundle(store.DefaultAuthzDir(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(authz.KMSKeyEnv, "arn:aws:kms:us-east-1:111122223333:key/abom-decisions")

	var out, errs bytes.Buffer
	err = run(context.Background(), []string{"audit", "rotate-key", "--db", path}, &out, &errs)
	if err == nil || !strings.Contains(err.Error(), authz.KMSKeyEnv) {
		t.Errorf("rotate-key = %v; want it refused naming %s", err, authz.KMSKeyEnv)
	}
	if after, _ := authz.ReadBundle(store.DefaultAuthzDir(path)); len(after.Keys) != len(before.Keys) {
		t.Errorf("the bundle changed from %d keys to %d", len(before.Keys), len(after.Keys))
	}
}

// A server signing with the current key while it retires could log an entry the old key signed
// after its cut-off, which would fail the log for good, so a rotation waits for no server to run.
func TestAuditRotateKeyRefusesWhileAServerRuns(t *testing.T) {
	path := signedLog(t)
	before, err := authz.ReadBundle(store.DefaultAuthzDir(path))
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(path, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, _, release, err := coordination.Hold(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = release() }()

	var out, errs bytes.Buffer
	err = run(context.Background(), []string{"audit", "rotate-key", "--db", path}, &out, &errs)
	if err == nil || !strings.Contains(err.Error(), "stop it") {
		t.Errorf("rotate-key while a server runs = %v", err)
	}
	if after, _ := authz.ReadBundle(store.DefaultAuthzDir(path)); len(after.Keys) != len(before.Keys) {
		t.Errorf("the bundle changed from %d keys to %d", len(before.Keys), len(after.Keys))
	}
}
