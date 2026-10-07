package ghapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Owner-only files beside the database, never in it; the client secret is not kept because nothing reads it.
const (
	appFile    = "app.json"
	keyFile    = "private-key.pem"
	secretFile = "webhook-secret"
)

// errConfigured reports a directory that already holds an App.
var errConfigured = errors.New("an App is already configured here")

// Credentials is a configured App: what identifies it, and the private key that proves it.
type Credentials struct {
	AppID      int64           `json:"appId"`
	Slug       string          `json:"slug"`
	ClientID   string          `json:"clientId"`
	Owner      string          `json:"owner"`
	OwnerKind  string          `json:"ownerKind"`
	HTMLURL    string          `json:"htmlUrl"`
	APIURL     string          `json:"apiUrl"`
	WebhookURL string          `json:"webhookUrl"`
	CreatedAt  string          `json:"createdAt"`
	Key        *rsa.PrivateKey `json:"-"`
}

// Conversion is GitHub's answer to exchanging a manifest code: the App, and its secrets.
type Conversion struct {
	ID            int64             `json:"id"`
	Slug          string            `json:"slug"`
	ClientID      string            `json:"client_id"`
	ClientSecret  string            `json:"client_secret"`
	WebhookSecret *string           `json:"webhook_secret"`
	PEM           string            `json:"pem"`
	HTMLURL       string            `json:"html_url"`
	Permissions   map[string]string `json:"permissions"`
	Events        []string          `json:"events"`
	Owner         struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

// Rotation names the key that was replaced and the one now installed, by GitHub's fingerprint.
type Rotation struct {
	Previous  string
	Installed string
}

// DefaultDir is the App directory beside the database it serves.
func DefaultDir(dbPath string) string { return filepath.Join(filepath.Dir(dbPath), "github-app") }

// KeyFile is where the App's private key lives.
func KeyFile(dir string) string { return filepath.Join(dir, keyFile) }

// WebhookSecretFile is where the receiver reads the App's webhook secret.
func WebhookSecretFile(dir string) string { return filepath.Join(dir, secretFile) }

// Configured reports whether dir holds an App.
func Configured(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, appFile))
	return err == nil
}

// Save records a converted App, refusing to replace one: GitHub never shows a key twice.
func Save(dir string, c Conversion, webhookURL, apiURL string) (Credentials, error) {
	if Configured(dir) {
		return Credentials{}, fmt.Errorf("%w: %s; move it aside to create another", errConfigured, dir)
	}
	key, err := parseKey([]byte(c.PEM))
	if err != nil {
		return Credentials{}, fmt.Errorf("GitHub's private key for App %d: %w", c.ID, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Credentials{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return Credentials{}, fmt.Errorf("restricting %s: %w", dir, err)
	}
	if err := writeOwnerOnly(KeyFile(dir), []byte(c.PEM)); err != nil {
		return Credentials{}, err
	}
	if c.WebhookSecret != nil && *c.WebhookSecret != "" {
		if err := writeOwnerOnly(WebhookSecretFile(dir), []byte(*c.WebhookSecret)); err != nil {
			return Credentials{}, err
		}
	}
	creds := Credentials{
		AppID: c.ID, Slug: c.Slug, ClientID: c.ClientID, Owner: c.Owner.Login,
		OwnerKind: strings.ToLower(c.Owner.Type), HTMLURL: c.HTMLURL, APIURL: apiURL,
		WebhookURL: webhookURL, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return Credentials{}, err
	}
	// Written last: its presence is what says an App is configured.
	if err := writeOwnerOnly(filepath.Join(dir, appFile), append(data, '\n')); err != nil {
		return Credentials{}, err
	}
	creds.Key = key
	return creds, nil
}

// Load reads the App in dir, refusing a key file anybody but this account can read.
func Load(dir string) (Credentials, error) {
	data, err := os.ReadFile(filepath.Join(dir, appFile))
	if err != nil {
		return Credentials{}, fmt.Errorf("reading the App in %s: %w", dir, err)
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil || c.AppID == 0 {
		return Credentials{}, fmt.Errorf("%s does not name an App", filepath.Join(dir, appFile))
	}
	pemBytes, err := readOwnerOnly(KeyFile(dir))
	if err != nil {
		return Credentials{}, err
	}
	if c.Key, err = parseKey(pemBytes); err != nil {
		return Credentials{}, fmt.Errorf("%s: %w", KeyFile(dir), err)
	}
	return c, nil
}

// WebhookSecret is the secret the App's deliveries are signed with, for the receiver.
func WebhookSecret(dir string) ([]byte, error) {
	secret, err := readOwnerOnly(WebhookSecretFile(dir))
	if err != nil {
		return nil, fmt.Errorf("reading the App's webhook secret: %w", err)
	}
	if secret = bytes.TrimRight(secret, "\r\n"); len(secret) == 0 {
		return nil, fmt.Errorf("%s is empty", WebhookSecretFile(dir))
	}
	return secret, nil
}

// Fingerprint is the SHA-256 fingerprint GitHub shows beside each of an App's keys.
func Fingerprint(key *rsa.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(der)
	return "SHA256:" + base64.StdEncoding.EncodeToString(sum[:])
}

// RotateKey installs a key GitHub accepts; the old one works until deleted on GitHub, so nothing is down.
func RotateKey(
	ctx context.Context, dir string, pemBytes []byte, accepted func(context.Context, Credentials) error,
) (Rotation, error) {
	current, err := Load(dir)
	if err != nil {
		return Rotation{}, err
	}
	key, err := parseKey(pemBytes)
	if err != nil {
		return Rotation{}, fmt.Errorf("the new key: %w", err)
	}
	out := Rotation{Previous: Fingerprint(&current.Key.PublicKey), Installed: Fingerprint(&key.PublicKey)}
	if out.Previous == out.Installed {
		return Rotation{}, fmt.Errorf("the key %s is already the one installed", out.Installed)
	}
	candidate := current
	candidate.Key = key
	if err := accepted(ctx, candidate); err != nil {
		return Rotation{}, fmt.Errorf("GitHub did not accept %s for App %d, so the installed key is unchanged: %w",
			out.Installed, current.AppID, err)
	}
	if err := writeOwnerOnly(KeyFile(dir), pemBytes); err != nil {
		return Rotation{}, err
	}
	return out, nil
}

func parseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			return key, nil
		}
	case "PRIVATE KEY":
		if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			if rsaKey, ok := key.(*rsa.PrivateKey); ok {
				return rsaKey, nil
			}
		}
	}
	return nil, fmt.Errorf("the %q block is not an RSA private key", block.Type)
}

func readOwnerOnly(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	// Windows has no mode bits to check; its ACLs are the operator's to set.
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other accounts (mode %o); run: chmod 600 %s",
			path, info.Mode().Perm(), path)
	}
	return os.ReadFile(path)
}

// writeOwnerOnly replaces path in one rename, so a reader sees the old file or the new, never half.
func writeOwnerOnly(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("restricting %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
