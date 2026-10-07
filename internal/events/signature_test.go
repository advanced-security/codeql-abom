package events

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Only the secret's holder can sign; with no secret, an HMAC anyone can compute must not pass.
func TestOnlyASignatureUnderTheConfiguredSecretVerifies(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign(secret, body)

	if err := Verify(secret, body, good); err != nil {
		t.Fatalf("a correct signature was refused: %v", err)
	}

	for _, tc := range []struct {
		name   string
		secret []byte
		body   []byte
		header string
		want   error
	}{
		{"no secret configured", nil, body, sign(nil, body), ErrNoSecret},
		{"empty secret configured", []byte{}, body, sign([]byte{}, body), ErrNoSecret},
		{"no signature", secret, body, "", ErrUnsigned},
		{"another secret", secret, body, sign([]byte("other"), body), ErrBadSignature},
		{"a different body", secret, []byte(`{"ref":"refs/heads/evil"}`), good, ErrBadSignature},
		{"the SHA-1 header's scheme", secret, body, "sha1=" + strings.TrimPrefix(good, "sha256="),
			ErrBadSignature},
		{"no scheme", secret, body, strings.TrimPrefix(good, "sha256="), ErrBadSignature},
		{"not hex", secret, body, "sha256=" + strings.Repeat("zz", 32), ErrBadSignature},
		{"truncated", secret, body, good[:len(good)-2], ErrBadSignature},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Verify(tc.secret, tc.body, tc.header); !errors.Is(err, tc.want) {
				t.Errorf("Verify = %v, want %v", err, tc.want)
			}
		})
	}
}
