package authz

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/kms/types"
)

// KMSKeyEnv names the AWS KMS key decisions are signed with; unset signs with the local key.
const KMSKeyEnv = "ABOM_AUTHZ_KMS_KEY"

// KMSClient is the part of AWS KMS a signer uses.
type KMSClient interface {
	GetPublicKey(ctx context.Context, in *kms.GetPublicKeyInput, opts ...func(*kms.Options)) (*kms.GetPublicKeyOutput, error)
	Sign(ctx context.Context, in *kms.SignInput, opts ...func(*kms.Options)) (*kms.SignOutput, error)
}

// KMSSigner signs with an ECDSA P-256 key that never leaves AWS KMS.
type KMSSigner struct {
	client KMSClient
	key    string
	id     string
}

func (s *KMSSigner) KeyID() string { return s.id }

// Sign has KMS sign the message's SHA-256, which answers ASN.1 DER as a local key does.
func (s *KMSSigner) Sign(ctx context.Context, message []byte) ([]byte, error) {
	digest := sha256.Sum256(message)
	out, err := s.client.Sign(ctx, &kms.SignInput{KeyId: aws.String(s.key), Message: digest[:],
		MessageType: types.MessageTypeDigest, SigningAlgorithm: types.SigningAlgorithmSpecEcdsaSha256})
	if err != nil {
		return nil, fmt.Errorf("authz: signing with %s: %w", s.key, err)
	}
	return out.Signature, nil
}

// OpenKMS opens a KMS key for signing decisions and names it in the bundle in dir. Every other key
// still deciding is retired at the log's head and the KMS key checkpoints it, and the local key file
// is deleted, so nothing left on a disk keeps deciding beside the service. A KMS key once retired
// is refused rather than taken back.
func OpenKMS(ctx context.Context, client KMSClient, key, dir string, ledger Ledger, now time.Time) (*KMSSigner, error) {
	out, err := client.GetPublicKey(ctx, &kms.GetPublicKeyInput{KeyId: aws.String(key)})
	if err != nil {
		return nil, fmt.Errorf("authz: reading the public key of %s: %w", key, err)
	}
	if out.KeySpec != types.KeySpecEccNistP256 || out.KeyUsage != types.KeyUsageTypeSignVerify {
		return nil, fmt.Errorf("authz: %s is a %s key for %s; decisions need an ECC_NIST_P256 key for SIGN_VERIFY",
			key, out.KeySpec, out.KeyUsage)
	}
	parsed, err := x509.ParsePKIXPublicKey(out.PublicKey)
	public, ok := parsed.(*ecdsa.PublicKey)
	if err != nil || !ok || public.Curve != elliptic.P256() {
		return nil, fmt.Errorf("authz: %s did not answer an ECDSA P-256 public key", key)
	}
	id, err := keyID(public)
	if err != nil {
		return nil, err
	}
	b, err := bundleIn(dir)
	if err != nil {
		return nil, err
	}
	signer := &KMSSigner{client: client, key: key, id: id}
	at := now.UTC().Format(time.RFC3339Nano)
	head, err := headSeq(ledger)
	if err != nil {
		return nil, err
	}
	named, retired := false, false
	for i := range b.Keys {
		switch {
		case b.Keys[i].KeyID == id && b.Keys[i].NotAfter != "":
			return nil, fmt.Errorf("authz: %s was retired at %s; name a key that has not been", key, b.Keys[i].NotAfter)
		case b.Keys[i].KeyID == id:
			named = true
		case b.Keys[i].NotAfter == "":
			b.Keys[i].NotAfter, b.Keys[i].LastSeq, retired = at, head, true
		}
	}
	if !named {
		b.Keys = append(b.Keys, TrustedKey{KeyID: id, Origin: originKMSPrefix + key, PublicKey: publicPEM(out.PublicKey),
			NotBefore: at})
	}
	if !named || retired {
		if err := writeBundle(dir, b); err != nil {
			return nil, err
		}
	}
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("authz: deleting the retired local key: %w", err)
	}
	if retired {
		if err := pin(signer, ledger, now); err != nil {
			return nil, err
		}
	}
	return signer, nil
}

// OpenSigner opens the key decisions are signed with: the KMS key KMSKeyEnv names, read with the
// AWS SDK's own credential chain, or else the local key in dir.
func OpenSigner(ctx context.Context, dir string, ledger Ledger, now time.Time) (Signer, error) {
	key := os.Getenv(KMSKeyEnv)
	if key == "" {
		return OpenLocal(dir, now)
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("authz: configuring AWS for %s: %w", key, err)
	}
	return OpenKMS(ctx, kms.NewFromConfig(cfg), key, dir, ledger, now)
}
