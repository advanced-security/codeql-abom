package serve

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// The owned-scope API shows installation evidence beside each declaration without changing the declaration.
func TestTheOwnedScopeReportsInstallationEvidenceBesideEachDeclaration(t *testing.T) {
	srv, s := newServer(t)
	if err := s.SetOwnedScope([]store.DeclaredAccount{
		{Owner: "has-ghas", Kind: "organization", Enabled: true},
		{Owner: "octo-org", Kind: "organization", Enabled: true},
	}, store.DeclaredByCLI); err != nil {
		t.Fatalf("SetOwnedScope: %v", err)
	}

	type view struct {
		Owner        string                      `json:"owner"`
		State        string                      `json:"state"`
		Installation *store.InstallationEvidence `json:"installation"`
	}
	read := func() map[string]view {
		t.Helper()
		rec := get(t, srv, "/api/owned-scope")
		if !strings.Contains(rec.Body.String(), `"installation":`) {
			t.Fatalf("the owned scope carries no installation field: %s", rec.Body.String())
		}
		var got []view
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decoding: %v", err)
		}
		out := map[string]view{}
		for _, v := range got {
			out[v.Owner] = v
		}
		return out
	}

	if got := read(); got["has-ghas"].Installation != nil {
		t.Errorf("before any read: %+v, want null because nothing has asked", got["has-ghas"].Installation)
	}

	if err := s.RecordInstallationSync(store.InstallationSync{AppID: 7, At: time.Now(),
		Installations: []store.AppInstallation{{Owner: "has-ghas", Kind: "organization",
			InstallationID: 11, RepositorySelection: "all"}}}); err != nil {
		t.Fatalf("RecordInstallationSync: %v", err)
	}
	got := read()
	if len(got) != 2 {
		t.Fatalf("declared accounts = %d, want the two declared and nothing the App added", len(got))
	}
	if e := got["has-ghas"].Installation; e == nil || e.State != store.InstallationInstalled {
		t.Errorf("has-ghas = %+v, want installed", e)
	}
	if e := got["octo-org"].Installation; e == nil || e.State != store.InstallationUnknown ||
		e.Reason != store.InstallationNotInstalled {
		t.Errorf("octo-org = %+v, want unknown because it is not installed", e)
	}
}
