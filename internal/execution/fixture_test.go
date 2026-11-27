package execution

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
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

func (d DirStore) Get(_ context.Context, key string, limit int64) ([]byte, error) {
	p, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading %s: %w", key, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readBounded(key, f, limit)
}

// InProcess launches each attempt's worker as a goroutine of this process, with an attestor holding the
// worker's secrets across a socket as one does in a pod: the worker it makes keeps none of them.
type InProcess struct {
	// Worker makes the worker for one attempt from the secrets lent to it.
	Worker func(secrets map[string]string) (*Worker, error)
	// After runs once the worker has finished, as a test's hook to tamper with what it wrote.
	After func(objects ObjectStore, attempt string)

	mu   sync.Mutex
	done map[string]chan error
	stop map[string]context.CancelFunc
}

func (p *InProcess) Start(ctx context.Context, l Launch) error {
	w, err := p.Worker(l.Secrets)
	if err != nil {
		return err
	}
	ch := make(chan error, 1)
	ctx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	if p.done == nil {
		p.done, p.stop = map[string]chan error{}, map[string]context.CancelFunc{}
	}
	p.done[l.Attempt], p.stop[l.Attempt] = ch, cancel
	p.mu.Unlock()
	objects := w.Objects
	worker, closeAttestor, err := attestedApart(w, l.Grant)
	if err != nil {
		cancel()
		return err
	}
	go func() {
		err := worker.Run(ctx, authz.Envelope{})
		closeAttestor()
		if p.After != nil {
			p.After(objects, l.Attempt)
		}
		ch <- err
	}()
	return nil
}

// attestedApart is w with its secrets handed to an attestor holding them for the grant in env, served
// on a socket of its own, and how to stop it. Its context is not the worker's, as an attestor outlives
// its pod's worker being stopped.
func attestedApart(w *Worker, env authz.Envelope) (*Worker, func(), error) {
	held := Hold(env, Secrets{Bundle: w.Bundle, Identity: w.Identity, Renew: w.Renew, AttemptKey: w.AttemptKey,
		Objects: w.Objects, Now: w.Now})
	dir, err := os.MkdirTemp("", "abom-attest")
	if err != nil {
		return nil, nil, err
	}
	socket := filepath.Join(dir, "attestor.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, nil, err
	}
	serving, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeAttestor(serving, ln, held) }()
	apart := *w
	apart.Bundle, apart.Identity, apart.Renew, apart.AttemptKey, apart.Objects = authz.Bundle{}, nil, nil, nil, nil
	apart.Attestor = DialAttestor(socket)
	return &apart, func() {
		stop()
		<-done
		_ = os.RemoveAll(dir)
	}, nil
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

// Stop cancels the worker's context, as a pod's worker is sent SIGTERM.
func (p *InProcess) Stop(_ context.Context, attempt string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if stop := p.stop[attempt]; stop != nil {
		stop()
	}
	return nil
}

func (p *InProcess) Cleanup(_ context.Context, attempt string) {
	p.mu.Lock()
	delete(p.done, attempt)
	p.mu.Unlock()
}

// NewLocalCA is newLocalCA, for this package's tests that drive it from outside it.
func NewLocalCA(trustDomain string) (*localCA, error) { return newLocalCA(trustDomain) }

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
