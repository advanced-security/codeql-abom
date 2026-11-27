package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/ghauth"
	"github.com/advanced-security/codeql-abom/internal/serve"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// parityAccounts are who is declaring: an administrator of one organization and a plain member of
// another, which is the role the original defect let the command line declare whole.
var parityAccounts = []ghauth.Account{
	{Owner: "admin-org", Kind: "organization", Role: "admin"},
	{Owner: "member-org", Kind: "organization", Role: "member", SecurityManager: "no"},
}

// parityRoles are the roles GitHub reports on the repositories a member declares by name.
var parityRoles = map[string]string{"member-org/maintained": "maintain"}

func parityRole(_ context.Context, owner, repo, _ string) (string, error) {
	if role, ok := parityRoles[owner+"/"+repo]; ok {
		return role, nil
	}
	return "", errors.New("gh cannot read the role on " + owner + "/" + repo)
}

// entryPoints declare one organization through each way in, on a database of its own: whole, or
// only the one repository include names.
var entryPoints = map[string]func(t *testing.T, db, org, include string) error{
	"command line": func(t *testing.T, db, org, include string) error {
		args := []string{"--db", db, "--org", org}
		if include != "" {
			args = append(args, "--include", org+"/"+include)
		}
		return own(t, args...)
	},
	"interface": func(t *testing.T, db, org, include string) error {
		s := openParity(t, db)
		defer s.Close()
		body := `[{"owner":"` + org + `","kind":"organization","enabled":true}]`
		if include != "" {
			body = `[{"owner":"` + org + `","kind":"organization","enabled":true,"includesOnly":true,` +
				`"includes":["` + include + `"]}]`
		}
		r := httptest.NewRequest(http.MethodPut, "/api/owned-scope", strings.NewReader(body))
		r.Host = "127.0.0.1:8080"
		r.Header.Set("X-Abom-Control", "1")
		rec := httptest.NewRecorder()
		(&serve.Server{Store: s, ReadRepositoryRole: parityRole}).Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			return &store.DeclarationRefused{Reason: rec.Body.String()}
		}
		return nil
	},
	"local file": func(t *testing.T, db, org, include string) error {
		path := filepath.Join(t.TempDir(), "declare.json")
		file := `{"own":{"orgs":["` + org + `"]}}`
		if include != "" {
			file = `{"own":{"orgs":["` + org + `"],"includes":["` + org + "/" + include + `"]}}`
		}
		if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
		s := openParity(t, db)
		defer s.Close()
		var stderr strings.Builder
		if err := seedDeclaration(t.Context(), s, path, parityRole, &stderr); err != nil {
			return err
		}
		if declared, _ := s.OwnedScope(); len(declared) == 0 {
			return &store.DeclarationRefused{Reason: stderr.String()}
		}
		return nil
	},
}

func openParity(t *testing.T, db string) store.Backend {
	t.Helper()
	s, err := store.OpenBackend(db, store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// parityDB is a database on which the same operator was verified, as the page and the command line
// both establish before they declare.
func parityDB(t *testing.T) string {
	t.Helper()
	db := ownDB(t)
	asks(t, verifiedAs("octocat", parityAccounts...), parityRoles)
	s := openParity(t, db)
	defer s.Close()
	accounts := make([]store.Account, 0, len(parityAccounts))
	for _, a := range parityAccounts {
		accounts = append(accounts, store.Account{Owner: a.Owner, Kind: a.Kind, Role: a.Role, SecurityManager: a.SecurityManager})
	}
	if err := s.ReplaceAccounts(store.Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"read:org"}, Accounts: accounts}); err != nil {
		t.Fatal(err)
	}
	return db
}

// decisionsIn are the decisions a database's log holds, oldest first, each verified with its bundle.
func decisionsIn(t *testing.T, db string) []authz.Decision {
	t.Helper()
	s := openParity(t, db)
	defer s.Close()
	entries, err := s.Assertions(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := authz.ReadBundle(store.DefaultAuthzDir(db))
	if err != nil {
		t.Fatal(err)
	}
	var out []authz.Decision
	for _, e := range entries {
		if e.PredicateType != authz.PredicateDecision {
			continue
		}
		d, err := bundle.DecisionOf(e, s, nil)
		if err != nil {
			t.Fatalf("a logged decision does not verify: %v", err)
		}
		out = append(out, d)
	}
	return out
}

// lastDecision is the newest decision a database's log holds.
func lastDecision(t *testing.T, db string) authz.Decision {
	t.Helper()
	decisions := decisionsIn(t, db)
	if len(decisions) == 0 {
		t.Fatal("no decision was logged")
	}
	return decisions[len(decisions)-1]
}

// judged is a decision less how it was asked for: the entry point, who asked through it and the
// decision that admitted them differ by entry point, and the judgment, including the login it was
// judged as, may not.
type judged struct {
	Allowed  bool
	Reason   string
	Subject  string
	Evidence map[string]any
}

func judgedOf(t *testing.T, d authz.Decision) judged {
	t.Helper()
	var evidence map[string]any
	if err := json.Unmarshal(d.Evidence(), &evidence); err != nil {
		t.Fatal(err)
	}
	delete(evidence, "via")
	delete(evidence, "route")
	return judged{Allowed: d.Allowed(), Reason: d.Reason(), Subject: d.Subject(), Evidence: evidence}
}

// The defect this guards: one way in declared what another refused. Every entry point reaches the
// same signed decision about the same declaration by the same operator, an administrator's
// organization allowed and a member's refused, and each decision is logged as made.
func TestEveryEntryPointReachesTheSameDecisionAboutADeclaration(t *testing.T) {
	type claim struct{ org, include string }
	for c, allowed := range map[claim]bool{
		{"admin-org", ""}: true, {"member-org", ""}: false, {"member-org", "maintained"}: true,
	} {
		org := c.org + "/" + c.include
		var first judged
		var firstVia string
		for via, declare := range entryPoints {
			db := parityDB(t)
			err := declare(t, db, c.org, c.include)
			if (err == nil) != allowed {
				t.Errorf("%s declaring %s: %v; want allowed=%v", via, org, err, allowed)
			}
			d := lastDecision(t, db)
			if d.Operation() != authz.OpDeclareScope {
				t.Fatalf("%s logged %s", via, d.Operation())
			}
			got := judgedOf(t, d)
			if firstVia == "" {
				first, firstVia = got, via
				continue
			}
			if !reflect.DeepEqual(got, first) {
				t.Errorf("%s and %s decided %s differently:\n\t%+v\n\t%+v", firstVia, via, org, first, got)
			}
		}
	}
}
