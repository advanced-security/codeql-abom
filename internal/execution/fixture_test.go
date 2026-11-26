package execution

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DirStore is an object store in a directory.
type DirStore string

func (d DirStore) path(key string) (string, error) {
	clean := path.Clean(key)
	if clean != key || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return "", fmt.Errorf("object key %q is not a plain relative key", key)
	}
	return filepath.Join(string(d), filepath.FromSlash(clean)), nil
}

func (d DirStore) Put(_ context.Context, key string, body []byte) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, body, 0o644)
}

func (d DirStore) Get(_ context.Context, key string) ([]byte, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// InProcess launches each attempt's worker as a goroutine of this process.
type InProcess struct {
	// Worker makes the worker for one attempt from the secrets lent to it.
	Worker func(secrets map[string]string) (*Worker, error)
	// After runs once the worker has finished, as a test's hook to tamper with what it wrote.
	After func(objects ObjectStore, attempt string)

	mu   sync.Mutex
	done map[string]chan error
}

func (p *InProcess) Start(ctx context.Context, l Launch) error {
	w, err := p.Worker(l.Secrets)
	if err != nil {
		return err
	}
	ch := make(chan error, 1)
	p.mu.Lock()
	if p.done == nil {
		p.done = map[string]chan error{}
	}
	p.done[l.Attempt] = ch
	p.mu.Unlock()
	go func() {
		err := w.Run(ctx, l.Grant)
		if p.After != nil {
			p.After(w.Objects, l.Attempt)
		}
		ch <- err
	}()
	return nil
}

func (p *InProcess) Wait(ctx context.Context, attempt string) error {
	p.mu.Lock()
	ch := p.done[attempt]
	p.mu.Unlock()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *InProcess) Cleanup(_ context.Context, attempt string) {
	p.mu.Lock()
	delete(p.done, attempt)
	p.mu.Unlock()
}

// localCA issues SPIFFE identities as a cluster's issuer would, so a worker's result is verified the
// one way it is in production.
type localCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// newLocalCA makes a CA for a trust domain.
func newLocalCA(trustDomain string) (*localCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: trustDomain + " workload CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &localCA{cert: cert, key: key}, nil
}

// Pool is the CA as verification roots.
func (ca *localCA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// Issue makes an identity naming id, valid for ttl.
func (ca *localCA) Issue(id string, ttl time.Duration) (*Identity, error) {
	u, err := url.Parse(id)
	if err != nil || u.Scheme != "spiffe" {
		return nil, fmt.Errorf("%q is not a SPIFFE ID", id)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{SerialNumber: serial(), URIs: []*url.URL{u},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(ttl),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return newIdentity(id, chain, key)
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}
