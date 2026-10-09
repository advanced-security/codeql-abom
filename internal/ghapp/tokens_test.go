package ghapp

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// GitHub takes an RS256 JWT naming the App and expiring within ten minutes; iat is backdated a minute for drift.
func TestTheAppJWTIsSignedByTheKeyNamesTheAppAndExpiresWithinTenMinutes(t *testing.T) {
	key, other := testKeys(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)

	token, err := signJWT(key, testClientID, now)
	if err != nil {
		t.Fatalf("signJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("a JWT has three segments, got %d: %q", len(parts), token)
	}
	for i, p := range parts {
		if strings.ContainsAny(p, "=+/") {
			t.Errorf("segment %d is not unpadded base64url: %q", i, p)
		}
	}
	var header struct{ Alg, Typ string }
	decodeSegment(t, parts[0], &header)
	if header.Alg != "RS256" || header.Typ != "JWT" {
		t.Errorf("header = %+v, want RS256 JWT", header)
	}
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	decodeSegment(t, parts[1], &claims)
	if claims.Iss != testClientID {
		t.Errorf("iss = %q, want the App's client id", claims.Iss)
	}
	if claims.Iat != now.Add(-time.Minute).Unix() {
		t.Errorf("iat = %d, want a minute before now (%d)", claims.Iat, now.Add(-time.Minute).Unix())
	}
	if lifetime := claims.Exp - now.Unix(); lifetime <= 0 || lifetime > 600 || claims.Exp-claims.Iat > 600 {
		t.Errorf("exp is %ds after now and %ds after iat; GitHub allows at most 600", lifetime, claims.Exp-claims.Iat)
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("signature: %v", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("the signature does not verify under the App's key: %v", err)
	}
	if rsa.VerifyPKCS1v15(&other.PublicKey, crypto.SHA256, sum[:], sig) == nil {
		t.Error("the signature verifies under a key that is not the App's")
	}
}

func decodeSegment(t *testing.T, segment string, into any) {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decoding %q: %v", segment, err)
	}
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("parsing %s: %v", raw, err)
	}
}

// A token per installation is reused until near expiry and replaced before it, or a 401 would read as access withdrawn.
func TestInstallationTokensAreMintedPerOwnerCachedAndRefreshedBeforeExpiry(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"},
		fakeInstallation{ID: 12, Login: "advanced-security", Type: "Organization", Selection: "selected"})
	app := gh.app(c.Now)
	ctx := context.Background()

	token := func(owner string) string {
		t.Helper()
		tok, borrowed, err := app.Token(ctx, owner)
		if err != nil || borrowed {
			t.Fatalf("Token(%s): borrowed=%v %v", owner, borrowed, err)
		}
		return tok
	}
	first, second := token("has-ghas"), token("advanced-security")
	if first == second || gh.tokens[first] != 11 || gh.tokens[second] != 12 {
		t.Fatalf("tokens %q and %q: want one per installation, 11 and 12", first, second)
	}
	if again := token("HAS-GHAS"); again != first {
		t.Errorf("an owner named in another case got a new token %q", again)
	}
	mints := func(id string) int { return gh.count("POST /app/installations/" + id + "/access_tokens") }
	if mints("11") != 1 || mints("12") != 1 || gh.count("GET /app/installations") != 1 {
		t.Fatalf("mints 11=%d 12=%d, listings %d; want each minted once from one listing",
			mints("11"), mints("12"), gh.count("GET /app/installations"))
	}

	c.Advance(50 * time.Minute)
	if token("has-ghas") != first || mints("11") != 1 {
		t.Errorf("a token with ten minutes left was replaced (mints %d)", mints("11"))
	}

	c.Advance(6 * time.Minute)
	if refreshed := token("has-ghas"); refreshed == first || mints("11") != 2 {
		t.Errorf("a token four minutes from expiry was reused (mints %d)", mints("11"))
	}
	if refreshed := token("advanced-security"); refreshed == second || mints("12") != 2 {
		t.Errorf("the other installation's token was not refreshed (mints %d)", mints("12"))
	}
	if gh.count("GET /app/installations") != 1 {
		t.Errorf("refreshing relisted installations %d times", gh.count("GET /app/installations"))
	}
}

// An account is read whole only by its own installation; a lent token would list its public face as complete.
func TestAnAccountWithoutItsOwnInstallationIsNotReadAsAnAccount(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"})
	app := gh.app(c.Now)
	ctx := context.Background()

	for range 5 {
		tok, err := app.AccountToken(ctx, "actions")
		if !errors.Is(err, ErrNotInstalled) || tok != "" || !strings.Contains(err.Error(), "actions") {
			t.Fatalf("AccountToken(actions) = %q, %v; want no token and ErrNotInstalled naming the owner", tok, err)
		}
	}
	if n := gh.count("GET /app/installations"); n != 1 {
		t.Errorf("five requests for an uninstalled owner listed installations %d times, want 1", n)
	}

	gh.install(fakeInstallation{ID: 13, Login: "actions", Type: "Organization", Selection: "all"})
	c.Advance(2 * time.Minute)
	if tok, err := app.AccountToken(ctx, "actions"); err != nil || gh.tokens[tok] != 13 {
		t.Errorf("after installing, AccountToken(actions) = %q, %v; want installation 13's token", tok, err)
	}

	gh.uninstall(11)
	c.Advance(time.Hour)
	if tok, err := app.AccountToken(ctx, "has-ghas"); !errors.Is(err, ErrNotInstalled) || tok != "" {
		t.Errorf("after uninstalling, AccountToken(has-ghas) = %q, %v; want ErrNotInstalled", tok, err)
	}

	var _ TokenSource = app
}

// Owners that will never install the App are read through the designated installation, never as the person or anonymously.
func TestAnOwnerWithoutAnInstallationReadsPublicRepositoriesThroughTheDesignatedOne(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	ctx := context.Background()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 12, Login: "advanced-security", Type: "Organization", Selection: "selected"},
		fakeInstallation{ID: 5, Login: "paused-org", Type: "Organization", Selection: "all", Suspended: true},
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"})
	app := gh.app(c.Now)

	own, borrowed, err := app.Token(ctx, "advanced-security")
	if err != nil || borrowed || gh.tokens[own] != 12 {
		t.Errorf("an installed owner got %q borrowed=%v %v; want its own installation 12's token", own, borrowed, err)
	}
	for _, owner := range []string{"actions", "docker", ""} {
		tok, borrowed, err := app.Token(ctx, owner)
		if err != nil || !borrowed || gh.tokens[tok] != 11 {
			t.Errorf("Token(%q) = %q borrowed=%v %v; want the earliest usable installation, 11, lent",
				owner, tok, borrowed, err)
		}
	}

	named := gh.app(c.Now)
	named.PublicReads = "Advanced-Security"
	if tok, borrowed, err := named.Token(ctx, "actions"); err != nil || !borrowed || gh.tokens[tok] != 12 {
		t.Errorf("with advanced-security named, Token(actions) = %q borrowed=%v %v; want installation 12's",
			tok, borrowed, err)
	}
	unnamed := gh.app(c.Now)
	unnamed.PublicReads = "nobody"
	if tok, _, err := unnamed.Token(ctx, "actions"); tok != "" || !errors.Is(err, ErrNotInstalled) ||
		!strings.Contains(err.Error(), "nobody") {
		t.Errorf("with an uninstalled account named, Token(actions) = %q, %v; want an error naming it", tok, err)
	}

	empty := newFakeGitHub(t, key, c.Now).app(c.Now)
	for _, owner := range []string{"actions", ""} {
		if tok, _, err := empty.Token(ctx, owner); tok != "" || !errors.Is(err, ErrNoInstallation) {
			t.Errorf("with no installation at all, Token(%q) = %q, %v; want ErrNoInstallation", owner, tok, err)
		}
	}
}

// Where requests go is the operator's configuration, never a URL read back from the App's files: a
// file anybody could edit would otherwise send the App's signed token wherever it named.
func TestRequestsGoToTheConfiguredAPINotOneReadFromTheFiles(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"})
	app := gh.app(c.Now)
	app.Credentials.APIURL = "http://127.0.0.1:1"

	if _, err := app.AccountToken(context.Background(), "has-ghas"); err != nil {
		t.Fatalf("AccountToken: %v; requests followed the file's URL rather than the configured one", err)
	}
}
