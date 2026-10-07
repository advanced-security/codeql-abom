package ghapp

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// Each installation keeps its selection and, read with its own token, its repositories; an unreadable list is unread, not empty.
func TestReadingInstallationsRecordsEachSelectionAndItsRepositories(t *testing.T) {
	key, _ := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"},
		fakeInstallation{ID: 12, Login: "advanced-security", Type: "Organization", Selection: "selected",
			Repos: []string{"codeql-abom-test1", "codeql-abom-test2"}},
		fakeInstallation{ID: 13, Login: "octo-org", Type: "Organization", Selection: "selected",
			Repos: []string{"secret"}},
		fakeInstallation{ID: 14, Login: "paused-org", Type: "Organization", Selection: "selected", Suspended: true})
	gh.reposStatus[13] = http.StatusForbidden

	sync := gh.app(c.Now).ReadInstallations(context.Background())
	if sync.Unread != "" || sync.AppID != testAppID || !sync.At.Equal(c.Now()) {
		t.Fatalf("sync = %+v, want a complete read by App %d now", sync, testAppID)
	}
	byOwner := map[string]int{}
	for i, inst := range sync.Installations {
		byOwner[inst.Owner] = i
	}
	if len(byOwner) != 4 {
		t.Fatalf("installations = %+v, want all four", sync.Installations)
	}
	all := sync.Installations[byOwner["has-ghas"]]
	if all.InstallationID != 11 || all.Kind != "organization" || all.RepositorySelection != "all" || all.Repositories != nil {
		t.Errorf("has-ghas = %+v, want installation 11 on every repository, no list read", all)
	}
	selected := sync.Installations[byOwner["advanced-security"]]
	if !slices.Equal(selected.Repositories, []string{"codeql-abom-test1", "codeql-abom-test2"}) || selected.RepositoriesUnread != "" {
		t.Errorf("advanced-security = %+v, want its two selected repositories", selected)
	}
	unread := sync.Installations[byOwner["octo-org"]]
	if unread.Repositories != nil || !strings.Contains(unread.RepositoriesUnread, "403") {
		t.Errorf("octo-org = %+v, want its list unread with GitHub's refusal", unread)
	}
	if paused := sync.Installations[byOwner["paused-org"]]; !paused.Suspended {
		t.Errorf("paused-org = %+v, want suspended", paused)
	}
	if gh.count("GET /installation/repositories") != 2 {
		t.Errorf("repository lists read %d times, want only the two selected, unsuspended installations",
			gh.count("GET /installation/repositories"))
	}
}

// A refused listing (a wrong key looks like a revoked App from here) is unread, not an App with no installations.
func TestAListingGitHubRefusesIsUnreadRatherThanEmpty(t *testing.T) {
	key, other := testKeys(t)
	c := newClock()
	gh := newFakeGitHub(t, key, c.Now,
		fakeInstallation{ID: 11, Login: "has-ghas", Type: "Organization", Selection: "all"})

	wrongKey := gh.app(c.Now)
	wrongKey.Credentials.Key = other
	sync := wrongKey.ReadInstallations(context.Background())
	if sync.Unread == "" || !strings.Contains(sync.Unread, "401") || len(sync.Installations) != 0 {
		t.Errorf("with a key GitHub does not know: %+v, want unread with GitHub's 401", sync)
	}

	gh.listStatus = http.StatusInternalServerError
	if sync := gh.app(c.Now).ReadInstallations(context.Background()); sync.Unread == "" {
		t.Errorf("a failed listing read as %+v, want unread", sync)
	}

	gh.srv.Close()
	if sync := gh.app(c.Now).ReadInstallations(context.Background()); sync.Unread == "" {
		t.Errorf("an unreachable GitHub read as %+v, want unread", sync)
	}
}
