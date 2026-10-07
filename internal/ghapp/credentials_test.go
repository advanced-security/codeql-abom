package ghapp

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testConversion(t *testing.T, pemText string) Conversion {
	t.Helper()
	secret := "whsec-test-0123456789"
	c := Conversion{ID: testAppID, Slug: "abom-has-ghas", ClientID: testClientID,
		ClientSecret: "client-secret-never-stored", WebhookSecret: &secret, PEM: pemText,
		HTMLURL: "https://github.com/apps/abom-has-ghas"}
	c.Owner.Login, c.Owner.Type = "has-ghas", "Organization"
	return c
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// A key GitHub shows once is stored owner-only, and one others can read is refused, as ssh refuses it.
func TestAKeyFileOthersCanReadIsRefusedAtLoad(t *testing.T) {
	key, _ := testKeys(t)
	dir := filepath.Join(t.TempDir(), "github-app")
	saved, err := Save(dir, testConversion(t, pemOf(key)), "https://smee.io/abc", DefaultAPIURL)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.AppID != testAppID || saved.Key == nil {
		t.Fatalf("Save returned %+v", saved)
	}
	if mode(t, dir) != 0o700 || mode(t, KeyFile(dir)) != 0o600 || mode(t, WebhookSecretFile(dir)) != 0o600 {
		t.Errorf("modes: dir %o key %o secret %o, want 700/600/600",
			mode(t, dir), mode(t, KeyFile(dir)), mode(t, WebhookSecretFile(dir)))
	}
	if _, err := Save(dir, testConversion(t, pemOf(key)), "", DefaultAPIURL); err == nil {
		t.Error("a second App was saved over the first, orphaning its key")
	}

	loaded, err := Load(dir)
	if err != nil || loaded.AppID != testAppID || loaded.ClientID != testClientID || !loaded.Key.Equal(key) {
		t.Fatalf("Load = %+v, %v; want the saved App and key", loaded, err)
	}

	if err := os.Chmod(KeyFile(dir), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(dir)
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("a world-readable key loaded: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "PRIVATE KEY") {
		t.Errorf("the refusal quotes the key: %v", err)
	}

	_ = os.Chmod(KeyFile(dir), 0o600)
	if err := os.Chmod(WebhookSecretFile(dir), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := WebhookSecret(dir); err == nil {
		t.Error("a group-readable webhook secret was handed to the receiver")
	}
	_ = os.Chmod(WebhookSecretFile(dir), 0o600)
	if secret, err := WebhookSecret(dir); err != nil || string(secret) != "whsec-test-0123456789" {
		t.Errorf("WebhookSecret = %q, %v", secret, err)
	}
}

// Rotation installs only a key GitHub accepts, naming both keys by GitHub's fingerprint so the old can be deleted.
func TestRotatingTheKeyInstallsOnlyAKeyGitHubAccepts(t *testing.T) {
	key, next := testKeys(t)
	dir := filepath.Join(t.TempDir(), "github-app")
	if _, err := Save(dir, testConversion(t, pemOf(key)), "https://smee.io/abc", DefaultAPIURL); err != nil {
		t.Fatalf("Save: %v", err)
	}
	refuse := func(context.Context, Credentials) error { return errors.New("GitHub answered 401") }
	accept := func(_ context.Context, c Credentials) error {
		if !c.Key.Equal(next) || c.AppID != testAppID {
			return errors.New("asked about the wrong key")
		}
		return nil
	}

	if _, err := RotateKey(context.Background(), dir, []byte(pemOf(next)), refuse); err == nil {
		t.Fatal("a key GitHub refused was installed")
	}
	if c, _ := Load(dir); !c.Key.Equal(key) {
		t.Fatal("a refused rotation replaced the key")
	}
	if _, err := RotateKey(context.Background(), dir, []byte("not a key"), accept); err == nil {
		t.Fatal("a file that is not a key was installed")
	}

	rotated, err := RotateKey(context.Background(), dir, []byte(pemOf(next)), accept)
	if err != nil {
		t.Fatalf("RotateKey: %v", err)
	}
	if c, _ := Load(dir); !c.Key.Equal(next) || mode(t, KeyFile(dir)) != 0o600 {
		t.Errorf("after rotation the key is not the new one, or mode %o is not 600", mode(t, KeyFile(dir)))
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	sum := sha256.Sum256(der)
	if want := "SHA256:" + base64.StdEncoding.EncodeToString(sum[:]); rotated.Previous != want {
		t.Errorf("previous fingerprint %q, want GitHub's form %q", rotated.Previous, want)
	}
	if rotated.Installed != Fingerprint(&next.PublicKey) || rotated.Installed == rotated.Previous {
		t.Errorf("rotation = %+v", rotated)
	}
	if _, err := RotateKey(context.Background(), dir, []byte(pemOf(next)), accept); err == nil {
		t.Error("rotating to the key already installed reported success")
	}
}
