// Package authz decides, signs and verifies every permission ABOM acts on.
//
// A permission is decided once, by the decision point, and carried as a signed assertion: a DSSE
// envelope holding an in-toto Statement, the format GitHub's own attestations use. A consumer acts
// on a permission only after verifying the assertion behind it against the trust bundle, so the
// check happens where the permission is used rather than where a row was written. Every assertion
// is appended to a hash-chained log that `codeql-abom audit verify` re-checks offline.
package authz

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

// StatementType and PayloadType are in-toto's, so existing tooling reads what ABOM signs.
const (
	StatementType = "https://in-toto.io/Statement/v1"
	PayloadType   = "application/vnd.in-toto+json"
)

// The predicates ABOM signs, one type per kind of assertion.
const (
	predicateBase              = "https://github.com/advanced-security/codeql-abom/authz/"
	PredicateDecision          = predicateBase + "Decision/v1"
	PredicateTaskGrant         = predicateBase + "TaskGrant/v1"
	PredicateResultAttestation = predicateBase + "ResultAttestation/v1"
	PredicateIngestionReceipt  = predicateBase + "IngestionReceipt/v1"
	PredicateCheckpoint        = predicateBase + "Checkpoint/v1"
)

// Subject is what a statement is about, by name and digest.
type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

// Statement is an in-toto Statement v1.
type Statement struct {
	Type          string          `json:"_type"`
	Subject       []Subject       `json:"subject"`
	PredicateType string          `json:"predicateType"`
	Predicate     json.RawMessage `json:"predicate"`
}

// Signature is one DSSE signature: the key that made it and the ASN.1 ECDSA signature, base64.
type Signature struct {
	KeyID string `json:"keyid"`
	Sig   string `json:"sig"`
}

// Envelope is a DSSE envelope.
type Envelope struct {
	PayloadType string      `json:"payloadType"`
	Payload     string      `json:"payload"`
	Signatures  []Signature `json:"signatures"`
}

// PAE is DSSE's pre-authentication encoding, the bytes a signature is over.
func PAE(payloadType string, payload []byte) []byte {
	return []byte("DSSEv1 " + strconv.Itoa(len(payloadType)) + " " + payloadType + " " +
		strconv.Itoa(len(payload)) + " " + string(payload))
}

// Digest identifies an assertion: the SHA-256 of its payload. Signatures are left out because an
// ECDSA signature differs on every signing of the same statement.
func (e Envelope) Digest() (string, error) {
	payload, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return "", fmt.Errorf("authz: the envelope's payload is not base64: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// Seal signs a statement into an envelope.
func Seal(ctx context.Context, s Signer, st Statement) (Envelope, error) {
	st.Type = StatementType
	payload, err := json.Marshal(st)
	if err != nil {
		return Envelope{}, fmt.Errorf("authz: encoding a statement: %w", err)
	}
	sig, err := s.Sign(ctx, PAE(PayloadType, payload))
	if err != nil {
		return Envelope{}, fmt.Errorf("authz: signing a statement: %w", err)
	}
	return Envelope{PayloadType: PayloadType, Payload: base64.StdEncoding.EncodeToString(payload),
		Signatures: []Signature{{KeyID: s.KeyID(), Sig: base64.StdEncoding.EncodeToString(sig)}}}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
