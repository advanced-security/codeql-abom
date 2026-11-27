package execution

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

// AttemptKeySecret is the key an attempt's own signing key travels under in Launch.Secrets, and the
// environment variable its worker reads it from.
const AttemptKeySecret = "ABOM_ATTEMPT_KEY"

// AttemptKey is the key one attempt signs with beside its pool's identity. Every worker of a pool
// holds the pool's identity, and only this attempt's pod holds this key, which its grant names: a
// report or result signed by both is this attempt's, and one signed by the pool alone is any of them.
type AttemptKey struct {
	key    *ecdsa.PrivateKey
	id     string
	public string
}

// NewAttemptKey makes a key for one attempt, answering it and its private half as PEM, which is what
// the attempt's worker is lent.
func NewAttemptKey() (*AttemptKey, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, "", err
	}
	k, err := attemptKeyOf(key)
	if err != nil {
		return nil, "", err
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
}

// ParseAttemptKey reads the private half of an attempt's key, as NewAttemptKey wrote it.
func ParseAttemptKey(text string) (*AttemptKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("the attempt's key is not a PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the attempt's key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("the attempt's key is not an ECDSA key")
	}
	return attemptKeyOf(key)
}

func attemptKeyOf(key *ecdsa.PrivateKey) (*AttemptKey, error) {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &AttemptKey{key: key, id: "attempt:" + digest(der),
		public: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))}, nil
}

func (k *AttemptKey) KeyID() string { return k.id }

func (k *AttemptKey) Sign(_ context.Context, message []byte) ([]byte, error) {
	sum := sha256.Sum256(message)
	return ecdsa.SignASN1(rand.Reader, k.key, sum[:])
}

// PublicPEM is the key's public half, as a grant names it.
func (k *AttemptKey) PublicPEM() string { return k.public }

// attemptPublicKey reads the public half of an attempt's key as a grant names it.
func attemptPublicKey(text string) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("the grant names no attempt key")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("reading the attempt key the grant names: %w", err)
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("the attempt key the grant names is not an ECDSA key")
	}
	return key, nil
}
