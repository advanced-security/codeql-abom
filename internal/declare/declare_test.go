package declare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), DefaultFileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

// Nobody has to have the file. Its absence is the ordinary case on a fresh clone and must not read
// as a failure, which is the same absent-is-not-zero rule the graph follows.
func TestAnAbsentFileDeclaresNothingAndIsNotAnError(t *testing.T) {
	accounts, err := Load(filepath.Join(t.TempDir(), "nothing-here.json"))
	if err != nil {
		t.Fatalf("an absent file is not an error: %v", err)
	}
	if accounts != nil {
		t.Errorf("an absent file declares nothing; got %+v", accounts)
	}
}

// A file that cannot be parsed must not be reported as a file that declared nothing. Those are
// opposite states, and only one of them is something the operator can fix.
func TestAMalformedFileIsAnErrorRatherThanAnEmptyDeclaration(t *testing.T) {
	if _, err := Load(write(t, `{"own": {"orgs": [`)); err == nil {
		t.Error("a truncated file must be refused, not read as declaring nothing")
	}
}

// The failure this guards is silent: a misspelled key parses cleanly, declares nothing, and looks
// exactly like a file the operator never filled in. `org` is the likely one, because the command
// line flag is `--org` and the key is `orgs`.
func TestAMisspelledKeyIsRefusedRatherThanIgnored(t *testing.T) {
	_, err := Load(write(t, `{"own": {"org": ["has-ghas"]}}`))
	if err == nil {
		t.Fatal("a key the schema does not define must be refused")
	}
	if !strings.Contains(err.Error(), `"org"`) {
		t.Errorf("the error has to name the key so it can be fixed; got %v", err)
	}
}

func TestTheFileDeclaresTheAccountsItNames(t *testing.T) {
	accounts, err := Load(write(t, `{"own": {"orgs": ["has-ghas"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("want one account, got %d", len(accounts))
	}
	got := accounts[0]
	if got.Owner != "has-ghas" || got.Kind != store.AccountOrganization || !got.Enabled {
		t.Errorf("an organization named in the file is declared and enabled; got %+v", got)
	}
}

// The same rule `own --include` follows: an include for an account nobody declared restricts
// nothing and would read as accepted, leaving a typo looking like a configuration.
func TestAnIncludeMustNameADeclaredAccount(t *testing.T) {
	_, err := Load(write(t, `{"own": {"orgs": ["has-ghas"], "includes": ["other/thing"]}}`))
	if err == nil {
		t.Error("an include naming an undeclared account must be refused")
	}
}

// Seeding is for a database that has never been told anything. Overwriting a declaration somebody
// made in the interface would make a restart silently revert their decision.
func TestSeedingAppliesOnlyWhenNothingIsDeclared(t *testing.T) {
	s := newStore(t)
	existing := store.DeclaredAccount{Owner: "chosen-by-hand", Kind: store.AccountOrganization, Enabled: true}
	if err := s.SetOwnedScope([]store.DeclaredAccount{existing}, store.DeclaredByInterface); err != nil {
		t.Fatal(err)
	}

	seeded, err := Seed(s, []store.DeclaredAccount{{
		Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if seeded {
		t.Error("a declaration already exists, so the file must not replace it")
	}

	got, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Owner != "chosen-by-hand" {
		t.Errorf("the operator's own declaration has to survive a restart; got %+v", got)
	}
}

func TestSeedingAnEmptyDeclarationRecordsThatItCameFromAFile(t *testing.T) {
	s := newStore(t)

	seeded, err := Seed(s, []store.DeclaredAccount{{
		Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !seeded {
		t.Fatal("nothing was declared, so the file is what declares it")
	}

	got, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Owner != "has-ghas" {
		t.Fatalf("want has-ghas declared, got %+v", got)
	}
	if got[0].DeclaredBy != store.DeclaredByConfig {
		t.Errorf("provenance must say a file wrote this, not a person; got %q", got[0].DeclaredBy)
	}
}

// Seeding declares; it never vouches. Verification is the store's honored view, and a file that
// could skip it would be a back door rather than a convenience.
//
// The membership list has to name *something*, because an empty `abom_account` cannot tell "the
// token saw the list and this was not on it" from "no list was ever recorded" — and only the first
// is evidence against the operator.
func TestAFileDeclarationIsStillSubjectToVerification(t *testing.T) {
	s := newStore(t)
	if _, err := Seed(s, []store.DeclaredAccount{{
		Owner: "has-ghas", Kind: store.AccountOrganization, Enabled: true,
	}}); err != nil {
		t.Fatal(err)
	}

	// A token that could see the whole membership list, which did not include this account.
	if err := s.ReplaceAccounts(store.Verification{
		Login:    "somebody",
		Scopes:   []string{"read:org"},
		Accounts: []store.Account{{Owner: "somewhere-else", Kind: store.AccountOrganization, Role: "admin"}},
	}); err != nil {
		t.Fatal(err)
	}

	owned, err := s.RepositoryIsOwned("has-ghas/anything")
	if err != nil {
		t.Fatal(err)
	}
	if owned {
		t.Error("a contradicted declaration roots nothing, however it arrived")
	}
}
