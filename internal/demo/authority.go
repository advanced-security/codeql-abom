package demo

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"sync"
	"time"
)

// Authority signs the certificates the egress proxy presents for the hosts it reads. It is made
// fresh each time the proxy starts, its key never leaves the process, and its name constraints let
// it sign only those hosts, so the certificate a child trusts vouches for nothing else.
type Authority struct {
	hosts []string
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	pem   []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// NewAuthority makes an authority for hosts, valid for a day.
func NewAuthority(hosts []string) (*Authority, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: "ABOM Demo egress, " + strings.Join(hosts, ", ")},
		NotBefore:                   now.Add(-time.Minute),
		NotAfter:                    now.Add(24 * time.Hour),
		IsCA:                        true,
		BasicConstraintsValid:       true,
		MaxPathLenZero:              true,
		KeyUsage:                    x509.KeyUsageCertSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         slices.Clone(hosts),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("creating the egress authority: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &Authority{
		hosts: slices.Clone(hosts), cert: cert, key: key,
		pem:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		leaves: map[string]*tls.Certificate{},
	}, nil
}

// CertPEM is the authority's certificate, which Demo's children are told to trust.
func (a *Authority) CertPEM() []byte { return slices.Clone(a.pem) }

func (a *Authority) leaf(host string) (*tls.Certificate, error) {
	if !slices.Contains(a.hosts, host) {
		return nil, fmt.Errorf("the egress authority does not sign %s", host)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.leaves[host]; ok {
		return c, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    a.cert.NotBefore,
		NotAfter:     a.cert.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	c := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	a.leaves[host] = c
	return c, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}
