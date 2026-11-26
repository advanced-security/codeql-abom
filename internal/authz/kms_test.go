package authz

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// fakeKMS holds one key and signs as KMS does: a digest it is told is one, returning ASN.1 DER.
type fakeKMS struct {
	key   *ecdsa.PrivateKey
	spec  types.KeySpec
	usage types.KeyUsageType
	signs int
}

func newFakeKMS(t *testing.T) *fakeKMS {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeKMS{key: key, spec: types.KeySpecEccNistP256, usage: types.KeyUsageTypeSignVerify}
}

func (f *fakeKMS) GetPublicKey(_ context.Context, in *kms.GetPublicKeyInput, _ ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error) {
	der, err := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &kms.GetPublicKeyOutput{KeyId: in.KeyId, PublicKey: der, KeySpec: f.spec, KeyUsage: f.usage}, nil
}

func (f *fakeKMS) Sign(_ context.Context, in *kms.SignInput, _ ...func(*kms.Options)) (*kms.SignOutput, error) {
	if in.MessageType != types.MessageTypeDigest || in.SigningAlgorithm != types.SigningAlgorithmSpecEcdsaSha256 ||
		len(in.Message) != sha256.Size {
		return nil, errors.New("asked to sign something other than a SHA-256 digest with ECDSA_SHA_256")
	}
	f.signs++
	sig, err := ecdsa.SignASN1(rand.Reader, f.key, in.Message)
	return &kms.SignOutput{KeyId: in.KeyId, Signature: sig, SigningAlgorithm: in.SigningAlgorithm}, err
}

const kmsKey = "arn:aws:kms:us-east-1:111122223333:key/abom-decisions"

// A decision signed in KMS verifies through the same bundle a local one does: nothing that
// verifies can tell which signed it, and the key never leaves the service.
func TestAKMSKeySignsWhatTheBundleVerifies(t *testing.T) {
	dir := t.TempDir()
	fake := newFakeKMS(t)
	now := time.Now()

	signer, err := OpenKMS(context.Background(), fake, kmsKey, dir, &memLedger{}, now)
	if err != nil {
		t.Fatal(err)
	}
	env, err := Seal(context.Background(), signer, Statement{Type: StatementType, PredicateType: "test", Predicate: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Open(env, now.Add(time.Second)); err != nil || fake.signs != 1 {
		t.Errorf("a KMS signature does not verify through the bundle (%v), or KMS signed %d times", err, fake.signs)
	}
}

// Only an ECDSA P-256 signing key is used: any other would sign what no verifier here accepts.
func TestAKMSKeyThatIsNotAP256SigningKeyIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*fakeKMS){
		"an RSA key":        func(f *fakeKMS) { f.spec = types.KeySpecRsa2048 },
		"an encryption key": func(f *fakeKMS) { f.usage = types.KeyUsageTypeEncryptDecrypt },
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeKMS(t)
			mutate(fake)
			if _, err := OpenKMS(context.Background(), fake, kmsKey, t.TempDir(), &memLedger{}, time.Now()); err == nil {
				t.Error("opened a key that is not an ECDSA P-256 signing key")
			}
		})
	}
}

// Moving decisions into KMS retires the key that signed before: what it signed still verifies, and
// a copy of it left on a disk can sign nothing new.
func TestMovingToAKMSKeyRetiresTheLocalOne(t *testing.T) {
	dir := t.TempDir()
	start := time.Now().Add(-time.Hour)
	local, err := OpenLocal(dir, start)
	if err != nil {
		t.Fatal(err)
	}
	st := Statement{Type: StatementType, PredicateType: "test", Predicate: []byte(`{}`)}
	before, err := Seal(context.Background(), local, st)
	if err != nil {
		t.Fatal(err)
	}
	moved := start.Add(30 * time.Minute)
	if _, err := OpenKMS(context.Background(), newFakeKMS(t), kmsKey, dir, &memLedger{}, moved); err != nil {
		t.Fatal(err)
	}
	bundle, err := ReadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Open(before, start.Add(time.Minute)); err != nil {
		t.Errorf("what the local key signed before the move no longer verifies: %v", err)
	}
	after, err := Seal(context.Background(), local, st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Open(after, moved.Add(time.Minute)); err == nil {
		t.Error("the local key still signs after decisions moved to KMS")
	}
	if _, err := OpenLocal(dir, moved.Add(time.Minute)); err == nil {
		t.Error("the retired local key opened for signing")
	}
}
