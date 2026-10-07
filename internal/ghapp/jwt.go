package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const (
	// jwtBackdate absorbs clock drift, as GitHub's docs ask: a JWT issued in GitHub's future is refused.
	jwtBackdate = time.Minute
	// jwtLifetime stays inside GitHub's ten-minute ceiling even when counted from the backdated iat.
	jwtLifetime = 9 * time.Minute
)

// signJWT signs the token that authenticates as the App itself.
func signJWT(key *rsa.PrivateKey, issuer string, now time.Time) (string, error) {
	if key == nil {
		return "", fmt.Errorf("no private key is loaded")
	}
	claims, err := json.Marshal(struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}{now.Add(-jwtBackdate).Unix(), now.Add(jwtLifetime).Unix(), issuer})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("signing the App's JWT: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
