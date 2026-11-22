package store

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func declareOrgs(t *testing.T, s *Store, owners ...string) {
	t.Helper()
	var accounts []DeclaredAccount
	for _, o := range owners {
		accounts = append(accounts, DeclaredAccount{Owner: o, Kind: "organization", Enabled: true})
	}
	if err := s.SetOwnedScope(accounts, DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}
}

func recordSync(t *testing.T, s *Store, sync InstallationSync) {
	t.Helper()
	if err := s.RecordInstallationSync(sync); err != nil {
		t.Fatalf("RecordInstallationSync: %v", err)
	}
}

func installationEvidence(t *testing.T, s *Store, owners ...string) map[string]*InstallationEvidence {
	t.Helper()
	got, err := s.InstallationEvidence(owners)
	if err != nil {
		t.Fatalf("InstallationEvidence: %v", err)
	}
	return got
}

// An installation never alters a declaration, or ownership would follow wherever somebody installed the App.
func TestInstallationsAreEvidenceBesideTheDeclarationNeverReplacingIt(t *testing.T) {
	s := newTestStore(t)
	declareOrgs(t, s, "has-ghas")
	if err := s.ReplaceAccounts(Verification{Login: "octocat", VerifiedAt: time.Now().UTC(),
		Accounts: []Account{{Owner: "octocat", Kind: "user", Role: "owner"}}}); err != nil {
		t.Fatalf("ReplaceAccounts: %v", err)
	}
	beforeScope, _ := s.OwnedScope()
	beforeAccounts, _ := s.Accounts()

	if got := installationEvidence(t, s, "has-ghas"); got["has-ghas"] != nil {
		t.Fatalf("evidence before any read = %+v, want none: nothing has asked", got["has-ghas"])
	}

	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	recordSync(t, s, InstallationSync{AppID: 7, At: at, Installations: []AppInstallation{
		{Owner: "has-ghas", Kind: "organization", InstallationID: 11, RepositorySelection: "all"},
		{Owner: "advanced-security", Kind: "organization", InstallationID: 12,
			RepositorySelection: "selected", Repositories: []string{"codeql-abom-test2", "codeql-abom-test1"}},
	}})

	afterScope, _ := s.OwnedScope()
	afterAccounts, _ := s.Accounts()
	if !reflect.DeepEqual(beforeScope, afterScope) {
		t.Errorf("the declaration changed:\nbefore %+v\nafter  %+v", beforeScope, afterScope)
	}
	if !reflect.DeepEqual(beforeAccounts, afterAccounts) {
		t.Errorf("the verified accounts changed:\nbefore %+v\nafter  %+v", beforeAccounts, afterAccounts)
	}
	if owned, _ := s.RepositoryIsOwned("advanced-security/codeql-abom-test1"); owned {
		t.Error("an installed but undeclared account's repository reads as owned")
	}

	got := installationEvidence(t, s, "has-ghas", "ADVANCED-SECURITY", "octo-org")
	all := got["has-ghas"]
	if all == nil || all.State != InstallationInstalled || all.InstallationID == nil || *all.InstallationID != 11 ||
		all.RepositorySelection == nil || *all.RepositorySelection != "all" || all.Repositories != nil {
		t.Errorf("has-ghas = %+v, want installed with every repository and no list", all)
	}
	selected := got["advanced-security"]
	if selected == nil || selected.State != InstallationInstalled ||
		!slices.Equal(selected.Repositories, []string{"codeql-abom-test1", "codeql-abom-test2"}) {
		t.Errorf("advanced-security = %+v, want installed on exactly the two test repositories", selected)
	}
	if other := got["octo-org"]; other == nil || other.State != InstallationUnknown ||
		other.Reason != InstallationNotInstalled {
		t.Errorf("octo-org = %+v, want unknown because it is not installed", other)
	}
}

// Revoked, suspended or unreadable leaves the account unknown, never empty or contradicted, keeping the last list read.
func TestARevokedOrUnreadInstallationLeavesTheAccountUnknown(t *testing.T) {
	s := newTestStore(t)
	declareOrgs(t, s, "has-ghas", "advanced-security")
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	recordSync(t, s, InstallationSync{AppID: 7, At: t0, Installations: []AppInstallation{
		{Owner: "has-ghas", Kind: "organization", InstallationID: 11, RepositorySelection: "all"},
		{Owner: "advanced-security", Kind: "organization", InstallationID: 12,
			RepositorySelection: "selected", Repositories: []string{"codeql-abom-test1"}},
	}})

	recordSync(t, s, InstallationSync{AppID: 7, At: t0.Add(time.Hour), Installations: []AppInstallation{
		{Owner: "advanced-security", Kind: "organization", InstallationID: 12,
			RepositorySelection: "selected", RepositoriesUnread: "GitHub answered 401"},
	}})
	got := installationEvidence(t, s, "has-ghas", "advanced-security")
	if e := got["has-ghas"]; e == nil || e.State != InstallationUnknown || e.Reason != InstallationNoLongerInstalled ||
		e.SeenAt == nil || *e.SeenAt != t0.Format(time.RFC3339Nano) {
		t.Errorf("revoked has-ghas = %+v, want unknown, no longer installed, last seen at the first read", e)
	}
	if e := got["advanced-security"]; e == nil || e.State != InstallationUnknown ||
		e.Reason != InstallationRepositoriesUnread || !slices.Equal(e.Repositories, []string{"codeql-abom-test1"}) {
		t.Errorf("unread advanced-security = %+v, want unknown and the last list kept", e)
	}

	recordSync(t, s, InstallationSync{AppID: 7, At: t0.Add(2 * time.Hour), Installations: []AppInstallation{
		{Owner: "advanced-security", Kind: "organization", InstallationID: 12,
			RepositorySelection: "selected", Suspended: true},
	}})
	if e := installationEvidence(t, s, "advanced-security")["advanced-security"]; e == nil ||
		e.State != InstallationUnknown || e.Reason != InstallationSuspended {
		t.Errorf("suspended = %+v, want unknown because it is suspended", e)
	}

	recordSync(t, s, InstallationSync{AppID: 7, At: t0.Add(3 * time.Hour), Unread: "GitHub answered 401: A JSON web token could not be decoded"})
	got = installationEvidence(t, s, "has-ghas", "advanced-security", "never-installed")
	for owner, e := range got {
		if e == nil || e.State != InstallationUnknown || e.Reason != InstallationListUnread || e.Detail == "" {
			t.Errorf("%s after a failed read = %+v, want unknown because the listing could not be read", owner, e)
		}
	}
	if e := got["advanced-security"]; e == nil || !slices.Equal(e.Repositories, []string{"codeql-abom-test1"}) {
		t.Errorf("a failed read erased what was last read: %+v", e)
	}
	if scope, _ := s.OwnedScope(); len(scope) != 2 {
		t.Errorf("the declaration shrank to %+v after losing the App", scope)
	}
}

// The installer is the installation delivery's sender: GitHub required an organization owner to
// install an App asking for organization permissions. A delivery with no sender, or none at all,
// leaves the installer unknown, which is not "nobody installed it".
func TestTheInstallerIsTheDeliverysSenderAndUnknownWithoutOne(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, r := range []struct {
		id           int64
		owner, login string
	}{{11, "has-ghas", "octocat"}, {13, "acme", ""}, {11, "has-ghas", "mallory"}} {
		if err := s.RecordInstaller(r.id, r.owner, r.login, "d-"+r.login, at); err != nil {
			t.Fatalf("RecordInstaller: %v", err)
		}
	}
	recordSync(t, s, InstallationSync{AppID: 7, At: at, Installations: []AppInstallation{
		{Owner: "has-ghas", Kind: "organization", InstallationID: 11, RepositorySelection: "all"},
		{Owner: "advanced-security", Kind: "organization", InstallationID: 12, RepositorySelection: "all"},
		{Owner: "acme", Kind: "organization", InstallationID: 13, RepositorySelection: "all"},
	}})

	got := installationEvidence(t, s, "has-ghas", "advanced-security", "acme")
	if by := got["has-ghas"].InstalledBy; by == nil || *by != "octocat" {
		t.Errorf("has-ghas installed by %v, want octocat, the first delivery's sender, not a replay's", by)
	}
	for _, owner := range []string{"advanced-security", "acme"} {
		if by := got[owner].InstalledBy; by != nil {
			t.Errorf("%s installed by %q, want unknown", owner, *by)
		}
	}

	recordSync(t, s, InstallationSync{AppID: 7, At: at.Add(time.Hour), Installations: []AppInstallation{
		{Owner: "has-ghas", Kind: "organization", InstallationID: 21, RepositorySelection: "all"},
	}})
	if by := installationEvidence(t, s, "has-ghas")["has-ghas"].InstalledBy; by != nil {
		t.Errorf("a reinstallation is credited to %q, who installed the one it replaced", *by)
	}
}

// An include is outside the installation only where the evidence says so: not installed, no longer
// installed, suspended, or a selected list that was read and omits it. A listing or a repository
// list that could not be read says nothing, and marking every include would turn a failed read into
// a statement about access.
func TestAnUnreadInstallationMarksNoIncludeAsOutsideIt(t *testing.T) {
	s := newTestStore(t)
	at := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	recordSync(t, s, InstallationSync{AppID: 7, At: at, Installations: []AppInstallation{
		{Owner: "listed", Kind: "organization", InstallationID: 11, RepositorySelection: "selected",
			Repositories: []string{"kept"}},
		{Owner: "unlisted", Kind: "organization", InstallationID: 12, RepositorySelection: "selected",
			RepositoriesUnread: "listing the repositories installed on unlisted: GitHub answered 502"},
		{Owner: "paused", Kind: "organization", InstallationID: 13, RepositorySelection: "all", Suspended: true},
	}})
	includes := []string{"kept", "other"}
	got := installationEvidence(t, s, "listed", "unlisted", "paused", "absent")
	for owner, want := range map[string][]string{
		"listed": {"other"}, "unlisted": nil, "paused": {"kept", "other"}, "absent": {"kept", "other"},
	} {
		if marked := got[owner].Uncovered(includes); !slices.Equal(marked, want) || (want == nil) != (marked == nil) {
			t.Errorf("%s marks %v, want %v", owner, marked, want)
		}
	}

	recordSync(t, s, InstallationSync{AppID: 7, At: at.Add(time.Hour), Unread: "listing the App's installations: GitHub answered 500"})
	for _, owner := range []string{"listed", "paused", "absent"} {
		if marked := installationEvidence(t, s, owner)[owner].Uncovered(includes); marked != nil {
			t.Errorf("after a listing that could not be read, %s marks %v; want cannot say", owner, marked)
		}
	}
}
