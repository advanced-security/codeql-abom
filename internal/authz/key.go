package authz

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Signer signs with one key, named by KeyID. The local signer keeps a key beside the database; a
// KMS signer keeps it in the service, and nothing above this interface can tell the two apart.
type Signer interface {
	KeyID() string
	Sign(ctx context.Context, message []byte) ([]byte, error)
}

const (
	keyFile = "decision.key"
	// BundleFile is the trust bundle's name in a key directory, and in the ConfigMap workers read it from.
	BundleFile = "trust.json"
)

// LocalSigner signs with an ECDSA P-256 key held in a file only its owner can read.
type LocalSigner struct {
	key *ecdsa.PrivateKey
	id  string
}

func (s *LocalSigner) KeyID() string { return s.id }

func (s *LocalSigner) Sign(_ context.Context, message []byte) ([]byte, error) {
	digest := sha256.Sum256(message)
	return ecdsa.SignASN1(rand.Reader, s.key, digest[:])
}

// Where a key's private half lives.
const (
	OriginLocal     = "local"
	originKMSPrefix = "kms:"
)

// TrustedKey is a public key the bundle verifies with. An empty NotAfter is a key nothing has
// retired. A retired key verifies an envelope presented now only before NotAfter, and a logged
// entry only at or before LastSeq: the log's head when it retired, which the key that replaced it
// checkpointed. Position rather than time, because an entry's time is whatever wrote the row.
type TrustedKey struct {
	KeyID     string `json:"keyid"`
	PublicKey string `json:"publicKey"`
	Origin    string `json:"origin"`
	NotBefore string `json:"notBefore"`
	NotAfter  string `json:"notAfter,omitempty"`
	LastSeq   int64  `json:"lastSeq,omitempty"`
}

// Bundle is the set of keys a verifier trusts.
type Bundle struct {
	Keys []TrustedKey `json:"keys"`
}

// OpenLocal opens the decision key in dir, making it and naming it in the bundle on first use. A
// key the bundle does not name is refused: signing with it would make decisions nothing verifies.
// No key is made while a KMS key decides, or a process started without the KMS variable would sign
// with a new key every verifier trusts.
func OpenLocal(dir string, now time.Time) (*LocalSigner, error) {
	key, err := readKey(filepath.Join(dir, keyFile))
	if errors.Is(err, fs.ErrNotExist) {
		if b, err := ReadBundle(dir); err == nil {
			for _, k := range b.Keys {
				if k.NotAfter == "" && strings.HasPrefix(k.Origin, originKMSPrefix) {
					return nil, fmt.Errorf("authz: decisions are signed with %s; set %s to it rather than "+
						"making a local key beside it", strings.TrimPrefix(k.Origin, originKMSPrefix), KMSKeyEnv)
				}
			}
		}
		return newKey(dir, now)
	}
	if err != nil {
		return nil, err
	}
	s, err := signerOf(key)
	if err != nil {
		return nil, err
	}
	b, err := ReadBundle(dir)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(b.Keys, func(k TrustedKey) bool { return k.KeyID == s.id }) {
		return nil, fmt.Errorf("authz: %s holds key %s, which %s does not name", keyFile, s.id, BundleFile)
	}
	if _, ok := b.trusted(s.id, now); !ok {
		return nil, fmt.Errorf("authz: %s holds key %s, which %s has retired; decisions are signed with "+
			"another key, such as the KMS key %s names", keyFile, s.id, BundleFile, KMSKeyEnv)
	}
	return s, nil
}

// RotateLocal replaces the decision key with a new one. The old key stays trusted for overlap for
// what is presented now, such as a grant a worker already holds, and for the log up to its head;
// the new key checkpoints that head.
func RotateLocal(dir string, ledger Ledger, overlap time.Duration, now time.Time) (*LocalSigner, error) {
	old, err := OpenLocal(dir, now)
	if err != nil {
		return nil, err
	}
	b, err := ReadBundle(dir)
	if err != nil {
		return nil, err
	}
	head, err := headSeq(ledger)
	if err != nil {
		return nil, err
	}
	for i := range b.Keys {
		if b.Keys[i].KeyID == old.id && b.Keys[i].NotAfter == "" {
			b.Keys[i].NotAfter = now.Add(overlap).UTC().Format(time.RFC3339Nano)
			b.Keys[i].LastSeq = head
		}
	}
	if err := writeBundle(dir, b); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(dir, keyFile)); err != nil {
		return nil, fmt.Errorf("authz: retiring the old key: %w", err)
	}
	fresh, err := newKey(dir, now)
	if err != nil {
		return nil, err
	}
	return fresh, pin(fresh, ledger, now)
}

func headSeq(ledger Ledger) (int64, error) {
	if ledger == nil {
		return 0, nil
	}
	head, ok, err := ledger.Head()
	if err != nil || !ok {
		return 0, err
	}
	return head.Seq, nil
}

// pin has the key that replaced another checkpoint the log's head, so rewriting what the retired
// key signed would break a chain the new key vouches for.
func pin(s Signer, ledger Ledger, now time.Time) error {
	if ledger == nil {
		return nil
	}
	head, ok, err := ledger.Head()
	if err != nil || !ok {
		return err
	}
	p := &Point{Signer: s, Ledger: ledger, Now: func() time.Time { return now }}
	return p.checkpoint(context.Background(), head, p.now())
}

func newKey(dir string, now time.Time) (*LocalSigner, error) {
	b, err := bundleIn(dir)
	if err != nil {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("authz: generating the decision key: %w", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	// O_EXCL: two processes starting at once must not each write a key and trust only one.
	f, err := os.OpenFile(filepath.Join(dir, keyFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("authz: writing the decision key: %w", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	s, err := signerOf(key)
	if err != nil {
		return nil, err
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	b.Keys = append(b.Keys, TrustedKey{KeyID: s.id, PublicKey: publicPEM(public), Origin: OriginLocal,
		NotBefore: now.UTC().Format(time.RFC3339Nano)})
	return s, writeBundle(dir, b)
}

// bundleIn is the bundle a key is added to in dir, which a first key also creates.
func bundleIn(dir string) (Bundle, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Bundle{}, fmt.Errorf("authz: preparing %s: %w", dir, err)
	}
	b, err := ReadBundle(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Bundle{}, err
	}
	return b, nil
}

func publicPEM(der []byte) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signerOf(key *ecdsa.PrivateKey) (*LocalSigner, error) {
	id, err := keyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	return &LocalSigner{key: key, id: id}, nil
}

// keyID names a key by the SHA-256 of its public half, so the name cannot be claimed by another key.
func keyID(public *ecdsa.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return "", err
	}
	return "sha256:" + sha256Hex(der), nil
}

func readKey(path string) (*ecdsa.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, fmt.Errorf("authz: %s holds no PEM key", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("authz: reading %s: %w", path, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("authz: %s is not an ECDSA P-256 key", path)
	}
	return key, nil
}

// ReadBundle reads the trust bundle in dir.
func ReadBundle(dir string) (Bundle, error) {
	body, err := os.ReadFile(filepath.Join(dir, BundleFile))
	if err != nil {
		return Bundle{}, err
	}
	var b Bundle
	if err := json.Unmarshal(body, &b); err != nil {
		return Bundle{}, fmt.Errorf("authz: reading %s: %w", BundleFile, err)
	}
	return b, nil
}

func writeBundle(dir string, b Bundle) error {
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, BundleFile+".tmp")
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("authz: writing %s: %w", BundleFile, err)
	}
	return os.Rename(tmp, filepath.Join(dir, BundleFile))
}

// Open verifies an envelope presented now, such as a grant: at least one signature must be by a key
// the bundle trusts at the time given, over exactly the payload carried.
func (b Bundle) Open(e Envelope, at time.Time) (Statement, error) {
	return open(e, func(id string) (*ecdsa.PublicKey, bool) { return b.trusted(id, at) },
		"no signature verifies with a key trusted at "+at.UTC().Format(time.RFC3339)+retiredSignNothing)
}

// OpenLogged verifies an envelope as the log's entry at seq: signed by a key the bundle names that
// had not retired before the log reached seq.
func (b Bundle) OpenLogged(e Envelope, seq int64) (Statement, error) {
	// A row can be inserted at a position of its own choosing, and one before the log's first would
	// otherwise sit inside every retired key's trusted range.
	if seq < 1 {
		return Statement{}, fmt.Errorf("authz: entry %d is before the log begins", seq)
	}
	return open(e, func(id string) (*ecdsa.PublicKey, bool) { return b.trustedAt(id, seq) },
		fmt.Sprintf("no signature verifies with a key not retired before entry %d", seq)+retiredSignNothing)
}

const retiredSignNothing = "; a key retired before then signs nothing"

// open verifies an envelope's statement against the keys trust answers for, refusing with refusal
// when no signature verifies.
func open(e Envelope, trust func(id string) (*ecdsa.PublicKey, bool), refusal string) (Statement, error) {
	if e.PayloadType != PayloadType {
		return Statement{}, fmt.Errorf("authz: payload type %q is not an in-toto statement", e.PayloadType)
	}
	payload, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return Statement{}, fmt.Errorf("authz: the payload is not base64: %w", err)
	}
	digest := sha256.Sum256(PAE(e.PayloadType, payload))
	verified := false
	for _, sig := range e.Signatures {
		key, ok := trust(sig.KeyID)
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(sig.Sig)
		if err == nil && ecdsa.VerifyASN1(key, digest[:], raw) {
			verified = true
			break
		}
	}
	if !verified {
		return Statement{}, errors.New("authz: " + refusal)
	}
	var st Statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return Statement{}, fmt.Errorf("authz: the payload is not a statement: %w", err)
	}
	if st.Type != StatementType {
		return Statement{}, fmt.Errorf("authz: statement type %q is not in-toto's", st.Type)
	}
	return st, nil
}

// OpenWithKey verifies an envelope signed by one known ECDSA key, as a worker's result is signed with
// the key of the identity its certificate names.
func OpenWithKey(e Envelope, key *ecdsa.PublicKey) (Statement, error) {
	return open(e, func(string) (*ecdsa.PublicKey, bool) { return key, true }, "no signature verifies with the signer's key")
}

func (b Bundle) trusted(id string, at time.Time) (*ecdsa.PublicKey, bool) {
	k, key, ok := b.key(id)
	if !ok {
		return nil, false
	}
	from, err := time.Parse(time.RFC3339Nano, k.NotBefore)
	if err != nil || at.Before(from) {
		return nil, false
	}
	if k.NotAfter != "" {
		until, err := time.Parse(time.RFC3339Nano, k.NotAfter)
		if err != nil || !at.Before(until) {
			return nil, false
		}
	}
	return key, true
}

func (b Bundle) trustedAt(id string, seq int64) (*ecdsa.PublicKey, bool) {
	k, key, ok := b.key(id)
	if !ok || (k.NotAfter != "" && seq > k.LastSeq) {
		return nil, false
	}
	return key, true
}

func (b Bundle) key(id string) (TrustedKey, *ecdsa.PublicKey, bool) {
	for _, k := range b.Keys {
		if k.KeyID != id {
			continue
		}
		block, _ := pem.Decode([]byte(k.PublicKey))
		if block == nil {
			return TrustedKey{}, nil, false
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		key, ok := parsed.(*ecdsa.PublicKey)
		if err != nil || !ok {
			return TrustedKey{}, nil, false
		}
		// The id is the key's own digest; a bundle entry naming another key's id is not trusted.
		if named, err := keyID(key); err != nil || named != id {
			return TrustedKey{}, nil, false
		}
		return k, key, true
	}
	return TrustedKey{}, nil, false
}
