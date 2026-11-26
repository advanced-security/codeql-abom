package store

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

// AuthzDirEnv names the decision key directory for every command that does not name one, which a
// PostgreSQL database needs: it has no directory beside it to default to.
const AuthzDirEnv = "ABOM_AUTHZ_DIR"

// DefaultAuthzDir is where a database's decision key and trust bundle live when no flag says:
// $ABOM_AUTHZ_DIR, else beside a SQLite file, as the database itself is. Empty for a PostgreSQL
// target with no such variable, which can then verify nothing and declare nothing.
func DefaultAuthzDir(path string) string {
	if dir := os.Getenv(AuthzDirEnv); dir != "" {
		return dir
	}
	if path == "" || IsPostgresTarget(path) || strings.HasPrefix(path, "file:") {
		return ""
	}
	return filepath.Join(filepath.Dir(path), "authz")
}

// decisionKeys is where a store's decisions are signed and verified, shared by every view of one
// database so a leased writer signs with the key its primary verifies with.
type decisionKeys struct {
	dir    string
	mu     sync.Mutex
	signer authz.Signer
}

// ErrNoDecisionKey is a database with no decision key directory, which can then decide nothing.
var ErrNoDecisionKey = errors.New("decisions are signed, and this database has no decision key; " +
	"name its directory with --authz-dir")

func (k *decisionKeys) open(ctx context.Context, ledger authz.Ledger) (authz.Signer, error) {
	if k == nil || k.dir == "" {
		return nil, ErrNoDecisionKey
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.signer == nil {
		signer, err := authz.OpenSigner(ctx, k.dir, ledger, time.Now())
		if err != nil {
			return nil, err
		}
		k.signer = signer
	}
	return k.signer, nil
}

// The states of the declaration on record, read against the decision it names.
const (
	// ScopeNone is no declaration, which there is nothing to verify about.
	ScopeNone = "none"
	// ScopeSigned is a declaration a trusted key decided, exactly as it now reads.
	ScopeSigned = "signed"
	// ScopeUnsigned names no decision: written before declarations were signed, or around the
	// decision point. Saving it again decides it.
	ScopeUnsigned = "unsigned"
	// ScopeUnverified names a decision that does not hold: unsigned by a trusted key, a refusal,
	// or a declaration changed since.
	ScopeUnverified = "unverified"
)

// ScopeVerdict is whether the declaration on record is one a decision allowed. Only a signed one
// drives work; the others are shown so you can decide them.
type ScopeVerdict struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	Digest string `json:"digest"`
	// DecidedAt and DecidedBy are the decision's, empty unless it verified. JudgedAs is the GitHub
	// login whose roles its claims were weighed against, which need not be DecidedBy's.
	DecidedAt string `json:"decidedAt"`
	DecidedBy string `json:"decidedBy"`
	JudgedAs  string `json:"judgedAs"`
}

// Undecided reports whether a declaration is on record that no decision holds for, so nothing read
// from ownership may be acted on until it is decided.
func (d ScopeVerdict) Undecided() bool {
	return d.State == ScopeUnsigned || d.State == ScopeUnverified
}

// claimJudgment is what one account's part of a decision was reached from.
type claimJudgment struct {
	Owner    string `json:"owner"`
	Kind     string `json:"kind"`
	Role     string `json:"role,omitempty"`
	Judgment string `json:"judgment"`
}

const (
	judgedEligible     = "eligible"
	judgedNothingNew   = "claims nothing it did not already"
	judgedUnreported   = "not among the accounts GitHub reports, so nothing refutes it"
	judgedNotVerified  = "nothing has been verified, so nothing refutes it"
	judgedPropertyHeld = "a property scope the schema rules out"
)

// declarationEvidence is what a declaration was judged from. The decision names who asked;
// JudgedAs is the GitHub login whose roles the claims were weighed against, which is ABOM's own
// verified identity and not necessarily theirs, and Route the decision that admitted the request.
type declarationEvidence struct {
	Via      string          `json:"via"`
	JudgedAs string          `json:"judgedAs"`
	Route    string          `json:"route,omitempty"`
	Accounts []claimJudgment `json:"accounts"`
}

type canonicalAccount struct {
	Owner        string           `json:"owner"`
	Kind         string           `json:"kind"`
	Enabled      bool             `json:"enabled"`
	IncludesOnly bool             `json:"includesOnly"`
	Includes     []string         `json:"includes"`
	Properties   [][]PropertyTerm `json:"properties"`
}

// declarationDigest is what a decision about a declaration binds, folded as the write folds it so
// the rows read back give the same digest as the accounts written: first of each owner, names
// case-insensitive, blanks dropped, order and repetition ignored.
func declarationDigest(accounts []DeclaredAccount) string {
	seen := map[string]bool{}
	out := []canonicalAccount{}
	for _, a := range accounts {
		owner := strings.ToLower(strings.TrimSpace(a.Owner))
		if seen[owner] {
			continue
		}
		seen[owner] = true
		c := canonicalAccount{Owner: owner, Kind: a.Kind, Enabled: a.Enabled, IncludesOnly: a.IncludesOnly,
			Includes: []string{}, Properties: [][]PropertyTerm{}}
		for _, repo := range a.Includes {
			if repo = strings.ToLower(strings.TrimSpace(repo)); repo != "" {
				c.Includes = append(c.Includes, repo)
			}
		}
		slices.Sort(c.Includes)
		c.Includes = slices.Compact(c.Includes)
		for _, clause := range a.Properties {
			terms := []PropertyTerm{}
			for _, t := range clause.Terms {
				terms = append(terms, PropertyTerm{Name: strings.ToLower(strings.TrimSpace(t.Name)), Value: t.Value})
			}
			slices.SortFunc(terms, func(x, y PropertyTerm) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.Value, y.Value)) })
			if terms = slices.Compact(terms); len(terms) > 0 {
				c.Properties = append(c.Properties, terms)
			}
		}
		slices.SortFunc(c.Properties, func(x, y []PropertyTerm) int { return cmp.Compare(termsKey(x), termsKey(y)) })
		c.Properties = slices.CompactFunc(c.Properties, func(x, y []PropertyTerm) bool { return termsKey(x) == termsKey(y) })
		out = append(out, c)
	}
	slices.SortFunc(out, func(x, y canonicalAccount) int { return cmp.Compare(x.Owner, y.Owner) })
	body, _ := json.Marshal(out)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func termsKey(terms []PropertyTerm) string {
	body, _ := json.Marshal(terms)
	return string(body)
}

func scopeSubject(accounts []DeclaredAccount) string {
	return authz.ScopeSubject(declarationDigest(accounts))
}

// DecisionPoint signs with this database's decision key and logs to it: one key, opened once, for
// every view of the database and everything this process decides.
func (s *Store) DecisionPoint(ctx context.Context) (*authz.Point, error) {
	signer, err := s.decisions.open(ctx, s)
	if err != nil {
		return nil, err
	}
	return &authz.Point{Signer: signer, Ledger: s}, nil
}

// Decide signs a verdict on a registered operation with this database's decision key, and logs it.
func (s *Store) Decide(ctx context.Context, r authz.Request) (authz.Decision, error) {
	point, err := s.DecisionPoint(ctx)
	if err != nil {
		return authz.Decision{}, err
	}
	return point.Decide(ctx, r)
}

// decideDeclaration asks the decision point about a declaration, from the same judgments the picker
// offers by, and signs the answer for whoever asked. A refusal is signed and logged too, then returned.
func (s *Store) decideDeclaration(ctx context.Context, accounts []DeclaredAccount, by string) (authz.Decision, error) {
	point, err := s.DecisionPoint(ctx)
	if err != nil {
		return authz.Decision{}, err
	}
	v, err := s.Accounts()
	if err != nil {
		return authz.Decision{}, err
	}
	judgments, refusal, err := s.judgeDeclaration(accounts, v)
	if err != nil {
		return authz.Decision{}, err
	}
	asker, ok := authz.RequesterOf(ctx)
	if !ok {
		asker.Principal = authz.Local("")
		if by == DeclaredByCLI {
			asker.Principal = authz.Local("command line")
		}
	}
	request := authz.Request{Operation: authz.OpDeclareScope,
		Principal: asker.Principal,
		Subject:   scopeSubject(accounts),
		Evidence:  declarationEvidence{Via: by, JudgedAs: v.Login, Route: asker.Route, Accounts: judgments},
		Allow:     refusal == nil}
	if refusal != nil {
		request.Reason = refusal.Reason
	}
	d, err := point.Decide(ctx, request)
	if err != nil {
		return authz.Decision{}, err
	}
	if refusal != nil {
		return d, refusal
	}
	return d, nil
}

func (s *Store) judgeDeclaration(accounts []DeclaredAccount, v Verification) ([]claimJudgment, *DeclarationRefused, error) {
	var refused *DeclarationRefused
	if err := s.refusePropertyScopes(accounts); errors.As(err, &refused) {
		return []claimJudgment{{Judgment: judgedPropertyHeld}}, refused, nil
	} else if err != nil {
		return nil, nil, err
	}
	return s.judgeClaims(accounts, v)
}

// ScopeVerdict reads whether the declaration on record is one a trusted decision allowed.
func (s *Store) ScopeVerdict() (ScopeVerdict, error) {
	accounts, err := s.OwnedScope()
	if err != nil {
		return ScopeVerdict{}, err
	}
	return s.judgeScope(accounts)
}

// VerifiedScope is the declaration work may act on: all of it when its decision verifies against
// the trust bundle and matches it, and none of it otherwise, with the reason.
func (s *Store) VerifiedScope() ([]DeclaredAccount, ScopeVerdict, error) {
	accounts, err := s.OwnedScope()
	if err != nil {
		return nil, ScopeVerdict{}, err
	}
	verdict, err := s.judgeScope(accounts)
	if err != nil || verdict.State != ScopeSigned {
		return []DeclaredAccount{}, verdict, err
	}
	return accounts, verdict, nil
}

// scopeActionable is the check the ownership reads that drive work make before answering: SQL can
// see that a declaration names a logged decision, and only this can see that the decision verifies
// and still matches the rows.
func (s *Store) scopeActionable() (bool, error) {
	verdict, err := s.ScopeVerdict()
	return verdict.State == ScopeSigned, err
}

func (s *Store) judgeScope(accounts []DeclaredAccount) (ScopeVerdict, error) {
	if len(accounts) == 0 {
		return ScopeVerdict{State: ScopeNone}, nil
	}
	digest := accounts[0].DecisionDigest
	for _, a := range accounts {
		if a.DecisionDigest != digest {
			return ScopeVerdict{State: ScopeUnverified,
				Reason: "its accounts name different decisions, which no single declaration does"}, nil
		}
	}
	if digest == "" {
		return ScopeVerdict{State: ScopeUnsigned,
			Reason: "no recorded decision shows that saving it was allowed"}, nil
	}
	unverified := func(format string, args ...any) (ScopeVerdict, error) {
		return ScopeVerdict{State: ScopeUnverified, Digest: digest, Reason: fmt.Sprintf(format, args...)}, nil
	}
	if s.decisions == nil || s.decisions.dir == "" {
		return unverified("no decision key directory is configured to verify it with")
	}
	entry, ok, err := s.assertionByDigest(digest)
	if err != nil {
		return ScopeVerdict{}, err
	}
	if !ok {
		return unverified("its decision is not in the authorization log")
	}
	bundle, err := authz.ReadBundle(s.decisions.dir)
	if err != nil {
		return unverified("the trust bundle cannot be read: %v", err)
	}
	d, err := bundle.DecisionOf(entry)
	switch {
	case err != nil:
		return unverified("its decision does not verify: %v", err)
	case d.Operation() != authz.OpDeclareScope:
		return unverified("its decision is about %s, not a declaration", d.Operation())
	case !d.Allowed():
		return unverified("its decision refused it: %s", d.Reason())
	case d.Subject() != scopeSubject(accounts):
		return unverified("the declaration does not match its decision; it was changed after it was decided")
	}
	var evidence declarationEvidence
	_ = json.Unmarshal(d.Evidence(), &evidence)
	return ScopeVerdict{State: ScopeSigned, Digest: digest, DecidedAt: entry.RecordedAt, DecidedBy: d.Principal().Called(),
		JudgedAs: evidence.JudgedAs}, nil
}

func (s *Store) assertionByDigest(digest string) (authz.Entry, bool, error) {
	e, err := scanAuthzEntry(s.db.QueryRow(`SELECT `+authzEntryColumns+` FROM abom_authz_log
		WHERE digest = ? ORDER BY seq LIMIT 1`, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return authz.Entry{}, false, nil
	}
	if err != nil {
		return authz.Entry{}, false, fmt.Errorf("reading a decision from the authorization log: %w", err)
	}
	return e, true, nil
}
