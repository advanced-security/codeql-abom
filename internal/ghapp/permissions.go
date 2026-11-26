package ghapp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// PermissionSet is what one token may do: a permission and its access, "read" or "write".
type PermissionSet map[string]string

// The permission sets tokens are minted with, one per kind of call; each is a subset of the manifest.
var (
	// CrawlPermissions is every read; an installation that granted less is read with the overlap.
	CrawlPermissions = PermissionSet{
		"metadata": "read", "contents": "read", "security_events": "read",
		"organization_custom_properties": "read", "actions": "read",
		"organization_administration": "read", "administration": "read",
	}
	// CopilotPermissions makes Copilot requests billed to the installing organization, and nothing else.
	CopilotPermissions = PermissionSet{"organization_copilot_requests": "read"}
)

// ErrNotGranted reports a call whose permissions the installation has not approved.
var ErrNotGranted = errors.New("the App's installation has not granted a permission this call needs")

// NotGrantedError names what an installation must approve before the call it refused can be made.
type NotGrantedError struct {
	Owner   string
	Missing []string
}

func (e *NotGrantedError) Error() string {
	what := "a permission this call needs"
	if len(e.Missing) > 0 {
		what = strings.Join(e.Missing, ", ")
	}
	return fmt.Sprintf("the App's installation on %s has not granted %s; an owner of %s approves it "+
		"in the installation's settings on GitHub", e.Owner, what, e.Owner)
}

func (e *NotGrantedError) Unwrap() error { return ErrNotGranted }

var accessLevel = map[string]int{"read": 1, "write": 2, "admin": 3}

// key is a set's identity in the token cache, independent of map order.
func (p PermissionSet) key() string {
	parts := make([]string, 0, len(p))
	for name, access := range p {
		parts = append(parts, name+"="+access)
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// missingFrom names each permission granted does not cover, sorted, as "name: access".
func (p PermissionSet) missingFrom(granted map[string]string) []string {
	var missing []string
	for name, access := range p {
		if accessLevel[granted[name]] < accessLevel[access] {
			missing = append(missing, name+": "+access)
		}
	}
	slices.Sort(missing)
	return missing
}

// Unapproved is what the App asks for and an installation has not granted, as "name: access", sorted.
func Unapproved(granted map[string]string) []string {
	return permissionSetOf(Permissions).missingFrom(granted)
}

// within is the part of p that granted covers, at p's access.
func (p PermissionSet) within(granted map[string]string) PermissionSet {
	out := PermissionSet{}
	for name, access := range p {
		if accessLevel[granted[name]] >= accessLevel[access] {
			out[name] = access
		}
	}
	return out
}

// TokenWith is owner's own installation token carrying exactly perms, never lent from another
// installation, as a Copilot session's is; fresh mints a new one even where one is cached. A permission the installation has
// not approved refuses the call before GitHub is asked.
func (a *App) TokenWith(ctx context.Context, owner string, perms PermissionSet, fresh bool) (string, time.Time, error) {
	return a.tokenWith(ctx, owner, "", perms, fresh)
}

// RepositoryTokenWith is TokenWith reaching only repo of owner's installation, as the publisher's
// write to its one repository is.
func (a *App) RepositoryTokenWith(ctx context.Context, owner, repo string, perms PermissionSet) (string, error) {
	token, _, err := a.tokenWith(ctx, owner, repo, perms, false)
	return token, err
}

func (a *App) tokenWith(ctx context.Context, owner, repo string, perms PermissionSet, fresh bool) (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := a.installationFor(ctx, owner)
	if err != nil {
		return "", time.Time{}, err
	}
	if missing := perms.missingFrom(a.granted(id)); len(missing) > 0 {
		if a.now().Before(a.listedAt.Add(relistAfter)) {
			return "", time.Time{}, &NotGrantedError{Owner: owner, Missing: missing}
		}
		// The approval may be newer than the listing that reported it absent.
		list, err := a.listInstallations(ctx)
		if err != nil {
			return "", time.Time{}, err
		}
		a.remember(list)
		if id, err = a.installationFor(ctx, owner); err != nil {
			return "", time.Time{}, err
		}
		if missing := perms.missingFrom(a.granted(id)); len(missing) > 0 {
			return "", time.Time{}, &NotGrantedError{Owner: owner, Missing: missing}
		}
	}
	t, err := a.mint(ctx, id, owner, perms, repo, fresh)
	return t.token, t.expires, err
}

// crawlSet is what the crawl asks id for: every read it was granted, and never a write.
func (a *App) crawlSet(id int64) PermissionSet {
	granted := a.granted(id)
	if granted == nil {
		return CrawlPermissions
	}
	return CrawlPermissions.within(granted)
}

// granted is what installation id approved, as the last listing reported it; nil when unlisted.
func (a *App) granted(id int64) map[string]string {
	for _, inst := range a.listed {
		if inst.ID == id {
			return inst.Permissions
		}
	}
	return nil
}

// workerPermissions is what a worker analyzing one repository reads: its contents, and the metadata
// every token carries.
var workerPermissions = PermissionSet{"contents": "read", "metadata": "read"}

// RepositoryToken lends a worker a read of one repository, and nothing else. An owner the App is not
// installed on lends nothing: a public repository needs no token, and the installation that reads
// public repositories would also read every private one it covers.
func (a *App) RepositoryToken(ctx context.Context, owner, repo string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id, err := a.installationFor(ctx, owner)
	if errors.Is(err, ErrNotInstalled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	t, err := a.mint(ctx, id, owner, workerPermissions, repo, false)
	if errors.Is(err, ErrNotInstalled) {
		return "", nil
	}
	return t.token, err
}
