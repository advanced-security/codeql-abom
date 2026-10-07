// Package events settles GitHub webhook deliveries as hints, never trusting what a payload claims.
package events

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// Signature failures.
var (
	ErrNoSecret     = errors.New("no webhook secret is configured")
	ErrUnsigned     = errors.New("the delivery carries no X-Hub-Signature-256")
	ErrBadSignature = errors.New("X-Hub-Signature-256 does not match the configured secret")
)

// Verify checks GitHub's HMAC-SHA256 of body; an empty key refuses all, as anyone can sign with it.
func Verify(secret, body []byte, header string) error {
	if len(secret) == 0 {
		return ErrNoSecret
	}
	if header == "" {
		return ErrUnsigned
	}
	digest, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return ErrBadSignature
	}
	claimed, err := hex.DecodeString(digest)
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if !hmac.Equal(claimed, mac.Sum(nil)) {
		return ErrBadSignature
	}
	return nil
}
