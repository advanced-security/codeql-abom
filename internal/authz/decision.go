package authz

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Operation is one entry in the guard registry: a thing that needs a permission, who may be given
// it, and what the decision reads to give it.
type Operation struct {
	Name     string
	Role     string
	Evidence string
}

// The operations the registry knows.
const (
	OpDeclareScope     = "abom/declare-scope"
	OpDiscoveryControl = "abom/discovery-control"
	OpVerifyIdentity   = "abom/verify-identity"
	OpLockVerify       = "abom/lock-verify"
	OpEnableScanning   = "abom/enable-code-scanning"
	OpConfigure        = "abom/configure"
	OpPolicy           = "abom/policy"
	OpQueryLifecycle   = "abom/query-lifecycle"
	OpQueryTest        = "abom/query-test"
	OpQueryRequest     = "abom/query-request"
	OpAuthoring        = "abom/authoring"
	OpRequestedRun     = "abom/requested-run"
	OpSavedView        = "abom/saved-view"
	OpPrune            = "abom/prune"
	OpSubmitSnapshot   = "abom/submit-snapshot"
	OpExportQuery      = "abom/export-query"
)

// The roles an operation can require, least to most.
const (
	RoleViewer   = "viewer"
	RoleAnalyst  = "analyst"
	RoleOperator = "operator"
	RoleAdmin    = "admin"
)

// Rank orders roles; a role nobody granted is below them all.
func Rank(role string) int {
	return slices.Index([]string{RoleViewer, RoleAnalyst, RoleOperator, RoleAdmin}, role) + 1
}

// Permits reports whether role is enough to perform op.
func (op Operation) Permits(role string) bool {
	return Rank(role) > 0 && Rank(role) >= Rank(op.Role)
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Operation{}
)

func init() {
	for _, op := range []Operation{
		{OpDeclareScope, RoleOperator, "the role GitHub reports for each account newly claimed, and the account's kind"},
		{OpDiscoveryControl, RoleOperator, "the control asked for and what it starts, stops, requeues or reads"},
		{OpVerifyIdentity, RoleOperator, "the identity ABOM reads GitHub as, read again"},
		{OpLockVerify, RoleOperator, "the lock verified and the network it asks"},
		{OpEnableScanning, RoleOperator, "the repositories default setup is previewed, approved or enabled on"},
		{OpConfigure, RoleAdmin, "the pools and tier suites changed"},
		{OpPolicy, RoleOperator, "the policy adopted, simulated or waived"},
		{OpQueryLifecycle, RoleOperator, "the query version staged or deployed"},
		{OpQueryTest, RoleAnalyst, "the query version tested or printed"},
		{OpQueryRequest, RoleAnalyst, "the request written"},
		{OpAuthoring, RoleAnalyst, "the authoring session or tool, and the Copilot spend it can make"},
		{OpRequestedRun, RoleAnalyst, "the run, its scope and its queries"},
		{OpSavedView, RoleViewer, "the view saved or removed"},
		{OpPrune, RoleOperator, "what the prune removes or compacts"},
		{OpSubmitSnapshot, RoleOperator, "the dependency snapshot submitted or renewed"},
		{OpExportQuery, RoleOperator, "the query version, its bundle digest and the one repository it goes to"},
	} {
		Register(op)
	}
}

// Register adds an operation to the registry. Registering a name twice is a programming error.
func Register(op Operation) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[op.Name]; dup {
		panic("authz: " + op.Name + " is registered twice")
	}
	if Rank(op.Role) == 0 {
		panic("authz: " + op.Name + " needs the role " + op.Role + ", which no ranking knows")
	}
	registry[op.Name] = op
}

// Operations lists the registry, by name.
func Operations() []Operation {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Operation, 0, len(registry))
	for _, op := range registry {
		out = append(out, op)
	}
	slices.SortFunc(out, func(a, b Operation) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Lookup reads one operation from the registry.
func Lookup(name string) (Operation, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	op, ok := registry[name]
	return op, ok
}

// The kinds of principal a decision is made for.
const (
	PrincipalOperator = "operator"
	// PrincipalUser is a person signed in through the identity provider.
	PrincipalUser = "user"
)

// Principal is who asked. ID never changes for one person; Name is what they were called when they
// asked, kept as evidence and for reading, and absent where the ID is already a name.
type Principal struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Called is how a reader knows the principal: by name where one was given.
func (p Principal) Called() string { return cmp.Or(p.Name, p.ID) }

// Requester is who asked for the request a handler serves, and the digest of the decision that
// admitted it, so a decision the handler then makes about its content names the same person and
// cites the one before it. Empty from anywhere no route decision is made.
type Requester struct {
	Principal Principal
	Route     string
}

type requesterKey struct{}

// WithRequester carries who asked into what serves the request.
func WithRequester(ctx context.Context, r Requester) context.Context {
	return context.WithValue(ctx, requesterKey{}, r)
}

// RequesterOf is who asked, if a route decision said so.
func RequesterOf(ctx context.Context) (Requester, bool) {
	r, ok := ctx.Value(requesterKey{}).(Requester)
	return r, ok
}

// The principals a decision names, and the subjects it is about. Their text is signed into the log,
// so it changes here or nowhere.

// SignedIn is a person signed in, by the identifier their provider keeps for them and the name it
// gave when they signed in.
func SignedIn(id, name string) Principal { return Principal{Kind: PrincipalUser, ID: id, Name: name} }

// Local is whoever reaches this process without signing in: the interface on loopback, or, with
// channel, another way in such as the command line.
func Local(channel string) Principal {
	id := "local operator"
	if channel != "" {
		id += " (" + channel + ")"
	}
	return Principal{Kind: PrincipalOperator, ID: id}
}

// Remote is whoever reaches this process without signing in through a host it allows.
func Remote(host string) Principal {
	return Principal{Kind: PrincipalOperator, ID: "operator via " + host}
}

func RouteSubject(pattern string) string         { return "abom/route:" + pattern }
func ExportSubject(query, version string) string { return "abom/export:" + query + "@" + version }
func CommandSubject(name string) string          { return "abom/command:" + name }
func ScopeSubject(declarationDigest string) string {
	return "abom/owned-scope@sha256:" + declarationDigest
}

// Verdicts.
const (
	VerdictAllow  = "allow"
	VerdictRefuse = "refuse"
)

// Request is what a decision is asked about; the caller evaluates the guard and says the verdict.
type Request struct {
	Operation string
	Principal Principal
	Subject   string
	// Evidence is what the verdict was reached from, recorded in the decision with its digest.
	Evidence any
	Allow    bool
	Reason   string
}

type decisionPredicate struct {
	Operation      string          `json:"operation"`
	Principal      Principal       `json:"principal"`
	Subject        string          `json:"subject"`
	Evidence       json.RawMessage `json:"evidence"`
	EvidenceDigest string          `json:"evidenceDigest"`
	Verdict        string          `json:"verdict"`
	Reason         string          `json:"reason"`
	IssuedAt       string          `json:"issuedAt"`
}

// Decision is a signed verdict. Its fields are this package's alone: one comes only from the
// decision point or from verifying an envelope, so a guarded write that takes a Decision cannot be
// handed one nobody decided. The zero value is never valid.
type Decision struct {
	envelope Envelope
	digest   string
	p        decisionPredicate
}

func (d Decision) Valid() bool               { return d.digest != "" }
func (d Decision) Allowed() bool             { return d.Valid() && d.p.Verdict == VerdictAllow }
func (d Decision) Operation() string         { return d.p.Operation }
func (d Decision) Principal() Principal      { return d.p.Principal }
func (d Decision) Subject() string           { return d.p.Subject }
func (d Decision) Reason() string            { return d.p.Reason }
func (d Decision) Digest() string            { return d.digest }
func (d Decision) Evidence() json.RawMessage { return d.p.Evidence }

// Decision verifies an envelope presented now as a decision, trusted at the time given.
func (b Bundle) Decision(e Envelope, at time.Time) (Decision, error) {
	st, err := b.Open(e, at)
	if err != nil {
		return Decision{}, err
	}
	return decisionOf(e, st)
}

// DecisionOf verifies a log entry as the decision it holds, by its place in the log rather than the
// time its row records, which whatever wrote the row chose. One a retired key signed is honored only
// once the chain from it, read from log, reaches a checkpoint by a key still deciding; pins remembers
// the entries that has been shown for, and may be nil.
func (b Bundle) DecisionOf(e Entry, log LogReader, pins *Pins) (Decision, error) {
	var env Envelope
	if err := json.Unmarshal(e.Envelope, &env); err != nil {
		return Decision{}, fmt.Errorf("authz: entry %d holds no envelope: %w", e.Seq, err)
	}
	if digest, err := env.Digest(); err != nil || digest != e.Digest {
		return Decision{}, fmt.Errorf("authz: entry %d holds an envelope that is not the assertion it names", e.Seq)
	}
	st, by, err := b.openLogged(env, e.Seq)
	if err != nil {
		return Decision{}, err
	}
	if b.retired(by) {
		if err := pins.vouch(b, e, log); err != nil {
			return Decision{}, err
		}
	}
	return decisionOf(env, st)
}

// Claim is what a logged decision says it decided, read without verifying it.
type Claim struct {
	Operation string
	Verdict   string
}

// ClaimOf reads what a logged envelope says it decided, without verifying its signature. An index
// built from it may narrow what is honored, never widen it: whatever acts on a decision verifies it.
func ClaimOf(envelope []byte) (Claim, error) {
	var env Envelope
	if err := json.Unmarshal(envelope, &env); err != nil {
		return Claim{}, fmt.Errorf("authz: not an envelope: %w", err)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return Claim{}, fmt.Errorf("authz: the payload is not base64: %w", err)
	}
	var st Statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return Claim{}, fmt.Errorf("authz: the payload is not a statement: %w", err)
	}
	if st.PredicateType != PredicateDecision {
		return Claim{}, fmt.Errorf("authz: %s is not a decision", st.PredicateType)
	}
	var p decisionPredicate
	if err := json.Unmarshal(st.Predicate, &p); err != nil {
		return Claim{}, fmt.Errorf("authz: reading a decision: %w", err)
	}
	return Claim{Operation: p.Operation, Verdict: p.Verdict}, nil
}

func decisionOf(e Envelope, st Statement) (Decision, error) {
	if st.PredicateType != PredicateDecision {
		return Decision{}, fmt.Errorf("authz: %s is not a decision", st.PredicateType)
	}
	var p decisionPredicate
	if err := json.Unmarshal(st.Predicate, &p); err != nil {
		return Decision{}, fmt.Errorf("authz: reading a decision: %w", err)
	}
	digest, err := e.Digest()
	if err != nil {
		return Decision{}, err
	}
	return Decision{envelope: e, digest: digest, p: p}, nil
}

// DefaultCheckpointEvery is how many assertions a checkpoint covers.
const DefaultCheckpointEvery = 100

// Point is the decision point: it signs what was decided and appends it to the log.
type Point struct {
	Signer Signer
	// Ledger receives every assertion. Nil signs without logging, which only a test does.
	Ledger Ledger
	Now    func() time.Time
	// CheckpointEvery is how many assertions lie between two checkpoints; zero is the default.
	CheckpointEvery int
}

func (p *Point) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

// Decide signs a verdict on a registered operation. A refusal is signed and logged as a grant is:
// what was refused is as much part of the record as what was allowed.
func (p *Point) Decide(ctx context.Context, r Request) (Decision, error) {
	if _, ok := Lookup(r.Operation); !ok {
		return Decision{}, fmt.Errorf("authz: %q is not a registered operation", r.Operation)
	}
	evidence, err := json.Marshal(r.Evidence)
	if err != nil {
		return Decision{}, fmt.Errorf("authz: encoding the evidence for %s: %w", r.Operation, err)
	}
	now := p.now()
	pred := decisionPredicate{Operation: r.Operation, Principal: r.Principal, Subject: r.Subject,
		Evidence: evidence, EvidenceDigest: sha256Hex(evidence), Verdict: VerdictRefuse, Reason: r.Reason,
		IssuedAt: now.Format(time.RFC3339Nano)}
	if r.Allow {
		pred.Verdict = VerdictAllow
	}
	body, err := json.Marshal(pred)
	if err != nil {
		return Decision{}, err
	}
	env, digest, err := p.assert(ctx, Statement{
		Subject:       []Subject{{Name: r.Subject, Digest: map[string]string{"sha256": pred.EvidenceDigest}}},
		PredicateType: PredicateDecision, Predicate: body}, now)
	if err != nil {
		return Decision{}, err
	}
	return Decision{envelope: env, digest: digest, p: pred}, nil
}

type checkpointPredicate struct {
	Seq      int64  `json:"seq"`
	Chain    string `json:"chain"`
	IssuedAt string `json:"issuedAt"`
}

// assert signs a statement and logs it, checkpointing the head when the cadence says so.
func (p *Point) assert(ctx context.Context, st Statement, now time.Time) (Envelope, string, error) {
	env, err := Seal(ctx, p.Signer, st)
	if err != nil {
		return Envelope{}, "", err
	}
	digest, err := env.Digest()
	if err != nil || p.Ledger == nil {
		return env, digest, err
	}
	body, err := json.Marshal(env)
	if err != nil {
		return Envelope{}, "", err
	}
	entry, err := p.Ledger.AppendAssertion(st.PredicateType, digest, body, now)
	if err != nil {
		return Envelope{}, "", fmt.Errorf("authz: logging an assertion: %w", err)
	}
	every := int64(p.CheckpointEvery)
	if every <= 0 {
		every = DefaultCheckpointEvery
	}
	// By sequence rather than a counter, so the cadence survives a restart; every+1 because each
	// checkpoint takes a sequence number of its own.
	if st.PredicateType != PredicateCheckpoint && entry.Seq%(every+1) == every {
		if err := p.checkpoint(ctx, entry, now); err != nil {
			return Envelope{}, "", err
		}
	}
	return env, digest, nil
}

// Sign signs any statement with the decision key and logs it, as a grant is.
func (p *Point) Sign(ctx context.Context, st Statement) (Envelope, string, error) {
	return p.assert(ctx, st, p.now())
}

// Checkpoint signs the log's head now, as a server does when it stops.
func (p *Point) Checkpoint(ctx context.Context) error {
	if p.Ledger == nil {
		return nil
	}
	head, ok, err := p.Ledger.Head()
	if err != nil || !ok || head.PredicateType == PredicateCheckpoint {
		return err
	}
	return p.checkpoint(ctx, head, p.now())
}

func (p *Point) checkpoint(ctx context.Context, head Entry, now time.Time) error {
	body, err := json.Marshal(checkpointPredicate{Seq: head.Seq, Chain: head.Chain, IssuedAt: now.Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	_, _, err = p.assert(ctx, Statement{
		Subject:       []Subject{{Name: "abom_authz_log", Digest: map[string]string{"sha256": head.Chain}}},
		PredicateType: PredicateCheckpoint, Predicate: body}, now)
	return err
}
