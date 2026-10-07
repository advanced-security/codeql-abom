package ghapp

import (
	"crypto/rsa"
	"testing"
)

// KeysForTest and PEMForTest let the external tests share the internal tests' keys.
func KeysForTest(t *testing.T) (*rsa.PrivateKey, *rsa.PrivateKey) { return testKeys(t) }

func PEMForTest(key *rsa.PrivateKey) string { return pemOf(key) }
