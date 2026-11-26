package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

func declarationEntry(t *testing.T, s *Store, digest string) authz.Decision {
	t.Helper()
	entries, err := s.Assertions(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := authz.ReadBundle(s.decisions.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Digest != digest {
			continue
		}
		d, err := bundle.DecisionOf(e)
		if err != nil {
			t.Fatalf("the logged decision does not verify: %v", err)
		}
		return d
	}
	t.Fatalf("no logged assertion has digest %s", digest)
	return authz.Decision{}
}

// A declaration is written only with the signed decision that allowed it, and the decision is in the
// log, naming the operation, the entry point and what was judged.
func TestADeclarationCarriesTheDecisionThatAllowedIt(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}

	stored, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].DecisionDigest == "" {
		t.Fatalf("the declaration names no decision: %+v", stored)
	}
	d := declarationEntry(t, s, stored[0].DecisionDigest)
	if d.Operation() != authz.OpDeclareScope || !d.Allowed() {
		t.Errorf("decision is %s %v; want an allowed %s", d.Operation(), d.Allowed(), authz.OpDeclareScope)
	}
	if !strings.Contains(string(d.Evidence()), `"via":"cli"`) {
		t.Errorf("the evidence does not name the entry point: %s", d.Evidence())
	}

	accounts, verdict, err := s.VerifiedScope()
	if err != nil {
		t.Fatal(err)
	}
	if verdict.State != ScopeSigned || len(accounts) != 1 {
		t.Errorf("VerifiedScope = %d accounts, %+v; want the signed declaration", len(accounts), verdict)
	}
	if owned, _ := s.RepositoryIsOwned("acme/tool"); !owned {
		t.Error("a signed declaration is honored")
	}
}

// A row written without the decision point, as a direct database write would, is shown so it can be
// corrected and honored nowhere: nothing counts it as owned and nothing works from it.
func TestADeclarationWrittenAroundTheDecisionPointIsInert(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO abom_owned_scope (owner, kind, enabled, includes_only, declared_at)
		VALUES ('acme', 'organization', 1, 0, '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if owned, _ := s.RepositoryIsOwned("acme/tool"); owned {
		t.Error("an undecided declaration is honored")
	}
	if declared, _ := s.OwnedScopeDeclared(); declared {
		t.Error("an undecided declaration counts as declared")
	}
	accounts, verdict, err := s.VerifiedScope()
	if err != nil {
		t.Fatal(err)
	}
	if verdict.State != ScopeUnsigned || len(accounts) != 0 {
		t.Errorf("VerifiedScope = %d accounts, %+v; want nothing to act on", len(accounts), verdict)
	}
	if stored, _ := s.OwnedScope(); len(stored) != 1 {
		t.Errorf("the undecided row is still shown, so it can be confirmed; got %+v", stored)
	}
}

// An edit made after the decision, such as widening a restricted account to all of it, no longer
// matches what was signed, so the whole declaration stops driving work until it is decided again.
func TestADeclarationEditedAfterItsDecisionDoesNotVerify(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"tool"}}}, DeclaredByInterface); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE abom_owned_scope SET includes_only = 0`); err != nil {
		t.Fatal(err)
	}

	accounts, verdict, err := s.VerifiedScope()
	if err != nil {
		t.Fatal(err)
	}
	if verdict.State != ScopeUnverified || len(accounts) != 0 || !strings.Contains(verdict.Reason, "does not match") {
		t.Errorf("VerifiedScope = %d accounts, %+v; want a declaration that no longer matches its decision", len(accounts), verdict)
	}
}

// Normalizing on the way in must not read as tampering on the way out: duplicates, spacing and case
// that the write folds still verify.
func TestADeclarationTheWriteNormalizesStillVerifies(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{
		{Owner: " Acme ", Kind: AccountOrganization, Enabled: true, IncludesOnly: true,
			Includes: []string{"Tool", "tool", " ", "other "}},
		{Owner: "acme", Kind: AccountUser, Enabled: false},
		{Owner: "octocat", Kind: AccountUser},
	}, DeclaredByConfig); err != nil {
		t.Fatal(err)
	}
	if _, verdict, err := s.VerifiedScope(); err != nil || verdict.State != ScopeSigned {
		t.Errorf("VerifiedScope = %+v, %v; want the normalized declaration to verify", verdict, err)
	}
}

// A decision only counts when a key the store trusts signed it: the same rows read with another
// key directory are not your decision.
func TestADecisionFromAKeyTheStoreDoesNotTrustDoesNotVerify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	s, err := openTestStore(t, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	other, err := openTestStore(t, path, Options{AuthzDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, verdict, err := other.VerifiedScope(); err != nil || verdict.State != ScopeUnverified {
		t.Errorf("VerifiedScope = %+v, %v; want a decision no trusted key signed", verdict, err)
	}
}

// A refusal is decided and logged as an allowance is: what an entry point was refused is as much a
// part of the record as what it was allowed.
func TestARefusedDeclarationIsSignedAsARefusal(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplaceAccounts(Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"read:org"}, Accounts: []Account{
			{Owner: "member-org", Kind: AccountOrganization, Role: RoleMember, SecurityManager: "no"}}}); err != nil {
		t.Fatal(err)
	}

	err := s.SetOwnedScope([]DeclaredAccount{{Owner: "member-org", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI)
	var refused *DeclarationRefused
	if !errors.As(err, &refused) || !refused.ByRole {
		t.Fatalf("SetOwnedScope = %v; want a refusal by role", err)
	}
	head, ok, err := s.Head()
	if err != nil || !ok {
		t.Fatalf("nothing was logged: %v", err)
	}
	d := declarationEntry(t, s, head.Digest)
	if d.Operation() != authz.OpDeclareScope || d.Allowed() || d.Reason() != refused.Reason {
		t.Errorf("logged %s allowed=%v reason %q; want the refusal %q", d.Operation(), d.Allowed(), d.Reason(), refused.Reason)
	}
	if d.Principal() != authz.Local("command line") || !strings.Contains(string(d.Evidence()), `"judgedAs":"octocat"`) {
		t.Errorf("the decision names %+v with evidence %s; want the command line, judged as octocat", d.Principal(), d.Evidence())
	}
}

// One request, one person: the declaration's decision names who asked and the decision that let
// them ask, beside the GitHub login its claims were judged against, and Settings names that person.
func TestADeclarationNamesWhoAskedAndTheLoginItWasJudgedAs(t *testing.T) {
	s := newTestStore(t)
	if err := s.ReplaceAccounts(Verification{Login: "octocat", Host: "github.com", VerifiedAt: time.Now().UTC(),
		Scopes: []string{"read:org"}, Accounts: []Account{{Owner: "acme", Kind: AccountOrganization, Role: RoleAdmin}}}); err != nil {
		t.Fatal(err)
	}
	asker := authz.SignedIn("https://issuer.example#0001", "Ada Lovelace")
	ctx := authz.WithRequester(context.Background(), authz.Requester{Principal: asker, Route: "route-digest"})

	if err := s.DeclareOwnedScope(ctx, []DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByInterface); err != nil {
		t.Fatal(err)
	}

	stored, err := s.OwnedScope()
	if err != nil || len(stored) != 1 {
		t.Fatalf("OwnedScope = %+v, %v", stored, err)
	}
	d := declarationEntry(t, s, stored[0].DecisionDigest)
	if d.Principal() != asker {
		t.Errorf("the decision names %+v; want who asked, %+v", d.Principal(), asker)
	}
	for _, want := range []string{`"judgedAs":"octocat"`, `"route":"route-digest"`} {
		if !strings.Contains(string(d.Evidence()), want) {
			t.Errorf("the evidence %s does not carry %s", d.Evidence(), want)
		}
	}
	if verdict, err := s.ScopeVerdict(); err != nil || verdict.DecidedBy != "Ada Lovelace" || verdict.JudgedAs != "octocat" {
		t.Errorf("ScopeVerdict = %+v, %v; want it decided by Ada Lovelace, judged as octocat", verdict, err)
	}
}

// The SQL gate every ownership read passes honors an allowed declaration and nothing else: a row
// naming a refusal, or another operation's allow, is drawn and counted as owned by nothing.
func TestTheSQLGateHonorsOnlyAnAllowedDeclaration(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	honored := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM abom_owned_scope_honored`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if honored() != 1 {
		t.Fatal("the allowed declaration is not honored")
	}
	ctx := context.Background()
	point, err := s.DecisionPoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	refusal, err := point.Decide(ctx, authz.Request{Operation: authz.OpDeclareScope, Principal: authz.Local(""),
		Subject: "abom/scope:forged", Reason: "refused"})
	if err != nil {
		t.Fatal(err)
	}
	prune, err := point.Decide(ctx, authz.Request{Operation: authz.OpPrune, Principal: authz.Local(""),
		Subject: "abom/route:POST /api/prune/apply", Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	for name, digest := range map[string]string{"a refusal": refusal.Digest(), "a prune allowed": prune.Digest()} {
		if _, err := s.db.Exec(`UPDATE abom_owned_scope SET decision_digest = ?`, digest); err != nil {
			t.Fatal(err)
		}
		if n := honored(); n != 0 {
			t.Errorf("a declaration naming %s is honored by %d rows", name, n)
		}
	}
}

// Decisions logged before the index of what each decided existed are indexed when the store is
// migrated, so a database from before it keeps honoring its declaration.
func TestDecisionsLoggedBeforeTheirIndexAreIndexedOnMigrate(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}},
		DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	drop := `DROP TABLE abom_authz_decision`
	if s.db.postgres {
		drop += ` CASCADE` // the honored scope view reads it, and Migrate creates the view again
	}
	if _, err := s.db.Exec(drop); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if owned, err := s.RepositoryIsOwned("acme/tool"); err != nil || !owned {
		t.Errorf("RepositoryIsOwned = %v, %v; want the declaration honored after migrating", owned, err)
	}
}

// Without a key to sign with, nothing can be declared: there is no unsigned way in.
func TestAStoreWithoutADecisionKeyDeclaresNothing(t *testing.T) {
	s := newTestStore(t)
	s.decisions.dir = ""
	err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}}, DeclaredByCLI)
	if err == nil || !strings.Contains(err.Error(), "authz") {
		t.Fatalf("SetOwnedScope = %v; want it refused for want of a decision key", err)
	}
	if stored, _ := s.OwnedScope(); len(stored) != 0 {
		t.Errorf("an unsigned declaration was written: %+v", stored)
	}
}

// The ownership reads that drive work verify the decision itself, not just that one is named: an
// include slipped in beside a signed declaration leaves nothing owned until it is decided again.
func TestOwnershipReadsThatDriveWorkStopWhenTheDeclarationDoesNotVerify(t *testing.T) {
	s := newTestStore(t)
	if err := s.SetOwnedScope([]DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true,
		IncludesOnly: true, Includes: []string{"tool"}}}, DeclaredByInterface); err != nil {
		t.Fatal(err)
	}
	if owned, _ := s.RepositoryIsOwned("acme/tool"); !owned {
		t.Fatal("the signed declaration is not honored")
	}
	if _, err := s.db.Exec(`INSERT INTO abom_owned_include (owner, repo) VALUES ('acme', 'other')`); err != nil {
		t.Fatal(err)
	}

	if owned, err := s.RepositoryIsOwned("acme/tool"); err != nil || owned {
		t.Errorf("RepositoryIsOwned = %v, %v; want nothing owned", owned, err)
	}
	if among, err := s.OwnedAmong([]string{"acme/tool", "acme/other"}); err != nil || len(among) != 0 {
		t.Errorf("OwnedAmong = %v, %v; want nothing owned", among, err)
	}
	if declared, err := s.OwnedScopeDeclared(); err != nil || declared {
		t.Errorf("OwnedScopeDeclared = %v, %v; want no scope to act on", declared, err)
	}
	if owners, err := s.DeclaredOwners(); err != nil || len(owners) != 0 {
		t.Errorf("DeclaredOwners = %v, %v; want none", owners, err)
	}
}

// The decision is made where the declaration is written, so a second writer would be a way in that
// no decision guards. Only ownedscope.go writes the declaration and only authzlog.go the log.
func TestOnlyTheDecisionPointWritesDeclarationsAndTheLog(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	writers := map[*regexp.Regexp]string{
		regexp.MustCompile(`(?is)(INSERT\s+(OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM)\s+abom_owned_(scope|include|property_clause|property_term)\b`): "internal/store/ownedscope.go",
		regexp.MustCompile(`(?is)(INSERT\s+(OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM)\s+abom_authz_log\b`):                                           "internal/store/authzlog.go",
	}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if skip := skipNonSource(root, path, d); skip != nil {
			return skip
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for pattern, owner := range writers {
			if filepath.ToSlash(rel) == owner {
				continue
			}
			for _, m := range pattern.FindAllString(string(body), -1) {
				t.Errorf("%s writes %q; only %s may", rel, m, owner)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// keyFrom is whoever holds a copy of a decision key file: it signs as that key did.
type keyFrom struct {
	key *ecdsa.PrivateKey
	id  string
}

func stolenKey(t *testing.T, dir string) keyFrom {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "decision.key"))
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(body)
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key := parsed.(*ecdsa.PrivateKey)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	return keyFrom{key: key, id: "sha256:" + hex.EncodeToString(sum[:])}
}

func (k keyFrom) KeyID() string { return k.id }
func (k keyFrom) Sign(_ context.Context, message []byte) ([]byte, error) {
	digest := sha256.Sum256(message)
	return ecdsa.SignASN1(rand.Reader, k.key, digest[:])
}

// A copy of a retired key, and a database write, are not a declaration: a decision it signs after
// retiring, logged with a time from before, is refused by its place in the log, which is what the
// key's retirement fixed and what nothing writing a row can choose.
func TestADeclarationSignedByARetiredKeyAfterItRetiredDoesNotVerify(t *testing.T) {
	s := newTestStore(t)
	accounts := []DeclaredAccount{{Owner: "acme", Kind: AccountOrganization, Enabled: true}}
	if err := s.SetOwnedScope(accounts, DeclaredByCLI); err != nil {
		t.Fatal(err)
	}
	stolen := stolenKey(t, s.decisions.dir)
	// Retired an hour from now, so a time half an hour from now is inside the old key's window.
	rotated := time.Now().Add(time.Hour)
	if _, err := authz.RotateLocal(s.decisions.dir, s, 7*24*time.Hour, rotated); err != nil {
		t.Fatal(err)
	}
	stored, err := s.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	forger := &authz.Point{Signer: stolen, Ledger: s, Now: func() time.Time { return rotated.Add(-30 * time.Minute) }}
	forged, err := forger.Decide(context.Background(), authz.Request{Operation: authz.OpDeclareScope,
		Principal: authz.Principal{Kind: authz.PrincipalOperator, ID: "octocat"},
		Subject:   scopeSubject(stored), Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE abom_owned_scope SET decision_digest = ?`, forged.Digest()); err != nil {
		t.Fatal(err)
	}
	verdict, err := s.ScopeVerdict()
	if err != nil {
		t.Fatal(err)
	}
	if verdict.State != ScopeUnverified || !strings.Contains(verdict.Reason, "not retired before entry") {
		t.Errorf("a declaration a retired key signed after retiring reads %+v", verdict)
	}
}

// Nothing declared and a verified declaration are both settled; only a declaration naming no
// decision, or one that does not hold, leaves the scope undecided.
func TestOnlyADeclarationNoDecisionHoldsForIsUndecided(t *testing.T) {
	for state, want := range map[string]bool{ScopeNone: false, ScopeSigned: false, ScopeUnsigned: true, ScopeUnverified: true} {
		if got := (ScopeVerdict{State: state}).Undecided(); got != want {
			t.Errorf("%s: undecided %v, want %v", state, got, want)
		}
	}
}
