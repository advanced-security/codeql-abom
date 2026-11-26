package ghapp

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// approvedEverything is an installation that approved Copilot requests as well as every read.
var approvedEverything = map[string]string{
	"metadata": "read", "contents": "read", "security_events": "read",
	"organization_custom_properties": "read", "actions": "read", "organization_administration": "read",
	"administration": "read", "organization_copilot_requests": "read",
}

// approvedReadsOnly is an installation made before Copilot requests were asked for, and never re-approved.
var approvedReadsOnly = map[string]string{
	"metadata": "read", "contents": "read", "security_events": "read",
	"organization_custom_properties": "read", "actions": "read", "organization_administration": "read",
	"administration": "read",
}

// One token per installation would hand the crawl the Copilot grant, or a Copilot session every read,
// whichever was minted first; each kind of call gets a token carrying its own permissions alone.
func TestEachCallIsMintedOnlyThePermissionsItNeeds(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedEverything})
	app := gh.app(c.Now)
	ctx := context.Background()

	crawl, borrowed, err := app.Token(ctx, "has-ghas")
	if err != nil || borrowed {
		t.Fatalf("Token: borrowed=%v %v", borrowed, err)
	}
	copilot, expires, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false)
	if err != nil {
		t.Fatalf("TokenWith(copilot): %v", err)
	}
	if !expires.Equal(c.Now().Add(time.Hour)) {
		t.Errorf("the Copilot token expires %v, want GitHub's expiry an hour from now", expires)
	}
	if got := gh.carries[crawl]; !maps.Equal(got, CrawlPermissions) {
		t.Errorf("the crawl token carries %v, want exactly the reads %v", got, CrawlPermissions)
	}
	if got := gh.carries[copilot]; !maps.Equal(got, CopilotPermissions) {
		t.Errorf("the Copilot token carries %v, want exactly %v", got, CopilotPermissions)
	}
	if crawl == copilot {
		t.Fatal("the crawl and a Copilot session share one token")
	}

	mints := func() int { return gh.count("POST /app/installations/11/access_tokens") }
	if again, _, _ := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false); again != copilot || mints() != 2 {
		t.Errorf("a second Copilot token request minted again (%d mints) rather than reusing the first", mints())
	}
	if again, _, _ := app.Token(ctx, "has-ghas"); again != crawl || mints() != 2 {
		t.Errorf("a second crawl read minted again (%d mints)", mints())
	}
	refreshed, _, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, true)
	if err != nil || refreshed == copilot || mints() != 3 {
		t.Errorf("a fresh Copilot token = %q, %v after %d mints; want a newly minted one", refreshed, err, mints())
	}
	if after, _, _ := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false); after != refreshed {
		t.Errorf("after a refresh the cache handed back %q, not the refreshed token", after)
	}
}

// An installation that has not approved a grant is refused with the grant named, and GitHub is
// never asked: a 403 read mid-session as a product failure is the thing this exists to prevent.
func TestAPermissionTheInstallationHasNotApprovedIsRefusedBeforeGitHubIsAsked(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedReadsOnly})
	app := gh.app(c.Now)
	ctx := context.Background()

	for want, perms := range map[string]PermissionSet{
		"organization_copilot_requests: read": CopilotPermissions,
		"contents: write":                     {"contents": "write"},
	} {
		token, _, err := app.TokenWith(ctx, "has-ghas", perms, false)
		if token != "" || !errors.Is(err, ErrNotGranted) || !strings.Contains(err.Error(), want) ||
			!strings.Contains(err.Error(), "has-ghas") {
			t.Errorf("TokenWith(%v) = %q, %v; want ErrNotGranted naming %s on has-ghas", perms, token, err, want)
		}
	}
	if n := gh.count("POST /app/installations/11/access_tokens"); n != 0 {
		t.Errorf("GitHub was asked to mint %d tokens it was going to refuse", n)
	}

	crawl, _, err := app.Token(ctx, "has-ghas")
	if err != nil || !maps.Equal(gh.carries[crawl], CrawlPermissions) {
		t.Errorf("the crawl still reads with %v, %v; want the reads it was granted", gh.carries[crawl], err)
	}

	gh.mu.Lock()
	gh.installations[0].Granted = approvedEverything
	gh.mu.Unlock()
	if _, _, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false); !errors.Is(err, ErrNotGranted) {
		t.Errorf("seconds after a re-approval the listing was read again: %v", err)
	}
	c.Advance(2 * time.Minute)
	if token, _, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false); err != nil ||
		!maps.Equal(gh.carries[token], CopilotPermissions) {
		t.Errorf("after a re-approval and a fresh listing, TokenWith = %q, %v; want the Copilot token", token, err)
	}
}

// A listing can be older than the approval it reports; GitHub's own refusal is the same refusal, not a fallback.
func TestGitHubRefusingAGrantTheListingReportedIsTheSameRefusal(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedEverything})
	app := gh.app(c.Now)
	ctx := context.Background()
	if _, _, err := app.Token(ctx, "has-ghas"); err != nil {
		t.Fatal(err)
	}
	gh.mu.Lock()
	gh.installations[0].Granted = approvedReadsOnly
	gh.mu.Unlock()

	token, _, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false)
	if token != "" || !errors.Is(err, ErrNotGranted) || !strings.Contains(err.Error(), "has-ghas") {
		t.Fatalf("TokenWith after GitHub refused = %q, %v; want ErrNotGranted", token, err)
	}
	if n := gh.count("GET /app/installations"); n != 1 {
		t.Fatalf("listings %d before the next request, want 1", n)
	}
	if _, _, err := app.TokenWith(ctx, "has-ghas", CopilotPermissions, false); !errors.Is(err, ErrNotGranted) ||
		gh.count("GET /app/installations") != 2 {
		t.Errorf("after GitHub's refusal the next request did not read the listing again (%d listings): %v",
			gh.count("GET /app/installations"), err)
	}
}

// GitHub's refusal does not say which permission it lacks, so the refusal names none rather than every
// one asked for: naming metadata, which the installation still holds, sends an owner to approve it again.
func TestGitHubsRefusalNamesNoPermissionTheInstallationStillHolds(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedEverything})
	app := gh.app(c.Now)
	ctx := context.Background()
	if _, _, err := app.Token(ctx, "has-ghas"); err != nil {
		t.Fatal(err)
	}
	gh.mu.Lock()
	gh.installations[0].Granted = approvedReadsOnly
	gh.mu.Unlock()

	asked := PermissionSet{"metadata": "read", "organization_copilot_requests": "read"}
	if _, _, err := app.TokenWith(ctx, "has-ghas", asked, false); !errors.Is(err, ErrNotGranted) ||
		strings.Contains(err.Error(), "metadata") {
		t.Errorf("GitHub refused one permission and the refusal is %v; want ErrNotGranted naming none still held", err)
	}
}

// The crawl reads an account whose installation cannot hold organization permissions, such as a
// user's, with the reads it did grant rather than being refused for the ones it cannot.
func TestTheCrawlAsksForTheReadsTheInstallationGrantedAndNoMore(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now, fakeInstallation{ID: 21, Login: "data-douser", Type: "User", Selection: "all",
		Granted: map[string]string{"metadata": "read", "contents": "write", "administration": "read"}})
	app := gh.app(c.Now)

	token, _, err := app.Token(context.Background(), "data-douser")
	want := map[string]string{"metadata": "read", "contents": "read", "administration": "read"}
	if err != nil || !maps.Equal(gh.carries[token], want) {
		t.Errorf("the crawl token for a user installation carries %v, %v; want %v", gh.carries[token], err, want)
	}
}

// A Copilot session is billed to the account it names or to nobody: lending another installation's
// token, as reads of public repositories do, would bill an organization that never installed the App.
func TestACopilotTokenIsNeverLentFromAnotherInstallation(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedEverything})
	app := gh.app(c.Now)

	token, _, err := app.TokenWith(context.Background(), "actions", CopilotPermissions, false)
	if token != "" || !errors.Is(err, ErrNotInstalled) {
		t.Errorf("TokenWith(actions) = %q, %v; want ErrNotInstalled and no token", token, err)
	}
	if n := gh.count("POST /app/installations/11/access_tokens"); n != 0 {
		t.Errorf("%d tokens were minted on has-ghas for another account's session", n)
	}
}

// A worker analyzing one repository is lent a read of that repository's contents and nothing else:
// not the installation's other repositories, and none of the crawl's other reads. An owner the App is
// not installed on lends nothing: a public repository needs no token, and the token of the
// installation that reads public repositories would also read every private one it covers.
func TestAWorkerIsLentAReadOfItsOneRepositoryAndNothingElse(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all", Granted: approvedEverything})
	app := gh.app(c.Now)
	ctx := context.Background()

	token, err := app.RepositoryToken(ctx, "has-ghas", "dubbo")
	if err != nil || token == "" {
		t.Fatalf("RepositoryToken = %q, %v", token, err)
	}
	if got := gh.scopedTo[token]; !slices.Equal(got, []string{"dubbo"}) {
		t.Errorf("the worker's token reads %v; want the one repository it analyzes", got)
	}
	if got, want := gh.carries[token], map[string]string{"contents": "read", "metadata": "read"}; !maps.Equal(got, want) {
		t.Errorf("the worker's token carries %v; want %v", got, want)
	}
	mints := func() int { return gh.count("POST /app/installations/11/access_tokens") }
	if again, _ := app.RepositoryToken(ctx, "has-ghas", "dubbo"); again != token || mints() != 1 {
		t.Errorf("a second lend for the same repository minted again (%d mints)", mints())
	}
	if other, _ := app.RepositoryToken(ctx, "has-ghas", "other"); other == token {
		t.Error("two repositories were lent one token")
	}

	before := mints()
	if none, err := app.RepositoryToken(ctx, "actions", "checkout"); none != "" || err != nil {
		t.Errorf("an owner without an installation was lent %q, %v; want nothing", none, err)
	}
	if mints() != before {
		t.Error("lending for an owner without an installation minted another installation's token")
	}
}
