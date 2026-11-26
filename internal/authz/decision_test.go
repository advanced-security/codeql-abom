package authz

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// memLedger is the store's log without a database: what Point appends, chained the same way.
type memLedger struct{ entries []Entry }

func (m *memLedger) AppendAssertion(predicateType, digest string, envelope []byte, at time.Time) (Entry, error) {
	prev := Genesis
	if n := len(m.entries); n > 0 {
		prev = m.entries[n-1].Chain
	}
	e := Entry{Seq: int64(len(m.entries) + 1), PredicateType: predicateType, Digest: digest,
		Chain: NextChain(prev, digest), Envelope: envelope, RecordedAt: at.UTC().Format(time.RFC3339Nano)}
	m.entries = append(m.entries, e)
	return e, nil
}

func (m *memLedger) Assertions(after int64, limit int) ([]Entry, error) {
	var out []Entry
	for _, e := range m.entries {
		if e.Seq > after && (limit <= 0 || len(out) < limit) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memLedger) Head() (Entry, bool, error) {
	if len(m.entries) == 0 {
		return Entry{}, false, nil
	}
	return m.entries[len(m.entries)-1], true, nil
}

func point(t *testing.T) (*Point, *memLedger, Bundle) {
	t.Helper()
	s, dir := signer(t)
	ledger := &memLedger{}
	clock := t0
	return &Point{Signer: s, Ledger: ledger, Now: func() time.Time { clock = clock.Add(time.Second); return clock },
		CheckpointEvery: 3}, ledger, bundle(t, dir)
}

var declare = Request{Operation: OpDeclareScope, Principal: Principal{Kind: PrincipalOperator, ID: "octocat"},
	Subject: "octo", Evidence: map[string]string{"role": "admin"}, Allow: true, Reason: "an owner of octo"}

// A permission nobody registered cannot be decided: the registry is the list of what needs one, and
// an operation outside it would be a check with no entry anybody could audit.
func TestOnlyARegisteredOperationIsDecided(t *testing.T) {
	p, ledger, _ := point(t)
	if _, err := p.Decide(context.Background(), Request{Operation: "abom/never-registered", Allow: true}); err == nil {
		t.Error("an unregistered operation was decided")
	}
	if len(ledger.entries) != 0 {
		t.Errorf("a refused request was logged %d times", len(ledger.entries))
	}
}

// Every decision is signed and logged, a refusal as much as a grant, and opens again from the log
// as the same decision; the zero value is never a decision, so a caller cannot make one up.
func TestEveryDecisionIsSignedLoggedAndOpensAgainFromTheLog(t *testing.T) {
	p, ledger, b := point(t)
	allowed, err := p.Decide(context.Background(), declare)
	if err != nil {
		t.Fatal(err)
	}
	refusal := declare
	refusal.Allow, refusal.Reason, refusal.Subject = false, "a member of acme, which supports no declaration", "acme"
	refused, err := p.Decide(context.Background(), refusal)
	if err != nil {
		t.Fatal(err)
	}
	if !allowed.Valid() || !allowed.Allowed() || !refused.Valid() || refused.Allowed() {
		t.Errorf("allowed %v/%v, refused %v/%v", allowed.Valid(), allowed.Allowed(), refused.Valid(), refused.Allowed())
	}
	if (Decision{}).Valid() || (Decision{}).Allowed() {
		t.Error("the zero Decision passes for one")
	}
	if allowed.Operation() != OpDeclareScope || allowed.Subject() != "octo" || allowed.Principal().ID != "octocat" {
		t.Errorf("decision = %s %s %+v", allowed.Operation(), allowed.Subject(), allowed.Principal())
	}
	if len(ledger.entries) != 2 || ledger.entries[0].Digest != allowed.Digest() || ledger.entries[1].Digest != refused.Digest() {
		t.Fatalf("logged %+v", ledger.entries)
	}
	var env Envelope
	if err := json.Unmarshal(ledger.entries[0].Envelope, &env); err != nil {
		t.Fatal(err)
	}
	opened, err := b.Decision(env, t0.Add(time.Minute))
	if err != nil || opened.Digest() != allowed.Digest() || !opened.Allowed() {
		t.Errorf("reopened %+v, %v", opened, err)
	}
}

// A signed checkpoint over the head lands every N assertions, so a log cut short after one is
// detectable by anybody holding it.
func TestACheckpointIsSignedOverTheHeadEveryNAssertions(t *testing.T) {
	p, ledger, b := point(t)
	for range 4 {
		if _, err := p.Decide(context.Background(), declare); err != nil {
			t.Fatal(err)
		}
	}
	kinds := []string{}
	for _, e := range ledger.entries {
		kinds = append(kinds, e.PredicateType)
	}
	want := []string{PredicateDecision, PredicateDecision, PredicateDecision, PredicateCheckpoint, PredicateDecision}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("kinds = %v, want %v", kinds, want)
	}
	report := VerifyLog(ledger.entries, b)
	if report.Failure != nil || report.Verified != 5 || report.Checkpoints != 1 || report.Head != ledger.entries[4].Chain {
		t.Errorf("report = %+v", report)
	}
}

// The log is evidence: an altered entry, two entries swapped or one taken out is found where it
// happened, and nothing after it is counted as verified.
func TestAVerifierFindsTheFirstAlteredReorderedOrMissingEntryAndTrustsNothingAfter(t *testing.T) {
	p, ledger, b := point(t)
	for range 5 {
		if _, err := p.Decide(context.Background(), declare); err != nil {
			t.Fatal(err)
		}
	}
	intact := ledger.entries
	if r := VerifyLog(intact, b); r.Failure != nil {
		t.Fatalf("an intact log failed: %+v", r.Failure)
	}

	altered := append([]Entry(nil), intact...)
	refusal := declare
	refusal.Allow = false
	other, err := (&Point{Signer: p.Signer, Now: p.Now}).Decide(context.Background(), refusal)
	if err != nil {
		t.Fatal(err)
	}
	altered[1].Envelope = other.envelopeJSON()
	swapped := append([]Entry(nil), intact...)
	swapped[1], swapped[2] = swapped[2], swapped[1]
	missing := append(append([]Entry(nil), intact[:2]...), intact[3:]...)
	rechained := append([]Entry(nil), intact...)
	rechained[2].Chain = strings.Repeat("0", 64)

	for name, c := range map[string]struct {
		log []Entry
		at  int64
	}{"altered": {altered, 2}, "swapped": {swapped, 2}, "missing": {missing, 3}, "rechained": {rechained, 3}} {
		r := VerifyLog(c.log, b)
		if r.Failure == nil || r.Failure.Seq != c.at || r.Verified != int(c.at)-1 || r.Failure.Why == "" {
			t.Errorf("%s: report %+v, want a failure at %d and %d verified", name, r, c.at, c.at-1)
		}
	}
}

func (d Decision) envelopeJSON() []byte {
	body, _ := json.Marshal(d.envelope)
	return body
}

// An operation needing a role no ranking knows could be performed by nobody, or, compared as rank
// zero, by anybody signed in; it is refused where it is registered.
func TestAnOperationNeedingAnUnknownRoleIsNotRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("an operation needing the role \"owner\" was registered")
		}
	}()
	Register(Operation{Name: "abom/test-unknown-role", Role: "owner"})
}

// An operation is permitted to the role it names and every role above it, and to nobody without one;
// one naming a role no ranking knows is permitted to nobody.
func TestAnOperationPermitsItsRoleAndThoseAboveIt(t *testing.T) {
	op := Operation{Name: "abom/test-permits", Role: RoleOperator}
	for role, want := range map[string]bool{"": false, "owner": false, RoleViewer: false, RoleAnalyst: false,
		RoleOperator: true, RoleAdmin: true} {
		if got := op.Permits(role); got != want {
			t.Errorf("%q: permitted %v, want %v", role, got, want)
		}
	}
	if unknown := (Operation{Name: "abom/test-unknown", Role: "owner"}); unknown.Permits("") || unknown.Permits("owner") {
		t.Error("an operation needing a role no ranking knows is permitted to somebody")
	}
}
