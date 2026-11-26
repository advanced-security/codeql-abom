package execution

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Identity is a worker's SPIFFE X.509 identity: the ID its certificate names, the chain that vouches
// for it, and the key that signs what it returns.
type Identity struct {
	ID    string
	chain []byte
	key   *ecdsa.PrivateKey
	keyID string
}

// The files csi-driver-spiffe writes into a pod.
const (
	svidCert = "tls.crt"
	svidKey  = "tls.key"
)

// LoadSVID reads the identity csi-driver-spiffe mounted in dir.
func LoadSVID(dir string) (*Identity, error) {
	chain, err := os.ReadFile(filepath.Join(dir, svidCert))
	if err != nil {
		return nil, fmt.Errorf("reading the workload certificate: %w", err)
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, svidKey))
	if err != nil {
		return nil, fmt.Errorf("reading the workload key: %w", err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return nil, errors.New("the workload key is not PEM")
	}
	var key *ecdsa.PrivateKey
	if parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key, _ = parsed.(*ecdsa.PrivateKey)
	} else if ec, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		key = ec
	}
	if key == nil {
		return nil, errors.New("the workload key is not an ECDSA key")
	}
	leaf, err := leafOf(chain)
	if err != nil {
		return nil, err
	}
	id, err := spiffeID(leaf)
	if err != nil {
		return nil, err
	}
	return newIdentity(id, chain, key)
}

func newIdentity(id string, chain []byte, key *ecdsa.PrivateKey) (*Identity, error) {
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &Identity{ID: id, chain: chain, key: key, keyID: "x509:" + digest(der)}, nil
}

func (i *Identity) KeyID() string { return i.keyID }

func (i *Identity) Sign(_ context.Context, message []byte) ([]byte, error) {
	digest := sha256.Sum256(message)
	return ecdsa.SignASN1(rand.Reader, i.key, digest[:])
}

// ChainPEM is the certificate chain, leaf first.
func (i *Identity) ChainPEM() string { return string(i.chain) }

func leafOf(chainPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(chainPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("the certificate chain holds no certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func spiffeID(cert *x509.Certificate) (string, error) {
	for _, u := range cert.URIs {
		if u.Scheme == "spiffe" {
			return u.String(), nil
		}
	}
	return "", errors.New("the certificate names no SPIFFE ID")
}

// VerifyChain checks a chain against roots at a time and returns the SPIFFE ID and key it vouches for.
func VerifyChain(chainPEM string, roots *x509.CertPool, at time.Time) (string, *ecdsa.PublicKey, error) {
	rest := []byte(chainPEM)
	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", nil, err
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return "", nil, errors.New("no certificate")
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: at,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return "", nil, fmt.Errorf("the signer's certificate does not chain to a trusted root: %w", err)
	}
	id, err := spiffeID(certs[0])
	if err != nil {
		return "", nil, err
	}
	key, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return "", nil, errors.New("the signer's key is not ECDSA")
	}
	return id, key, nil
}
