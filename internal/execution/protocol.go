// Package execution runs a batch's fetch and CodeQL work in a worker that holds no store, under a
// signed task grant, and accepts what it returns only as signed by the identity the grant named.
//
// The coordinator signs a TaskGrant carrying the task itself, so a worker acts only on bytes the
// decision point signed. The worker signs a ResultAttestation with its SPIFFE identity's key over
// every output's digest, and uploads it last; the coordinator verifies the identity against its
// roots, the grant against this attempt, and every output's bytes before anything is ingested.

package execution

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// Prefix is where every attempt's objects live, under its pool's name, so the object store's
// credentials can confine a pool's workers to their own attempts.
const Prefix = "attempts/"

// ObjectStore holds an attempt's outputs: immutable once written, and never the database. Get answers
// ErrNotFound for a key nothing has written yet.
type ObjectStore interface {
	Put(ctx context.Context, key string, body []byte) error
	// Get reads what is at key, refusing it with ErrTooLarge if it is longer than limit, which is never
	// more than maxObject: every read is bounded by what it reads.
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
}

// ErrNotFound is a key nothing has written yet: a report a worker has not got to, as against one the
// store could not read.
var ErrNotFound = errors.New("nothing is written there yet")

// ErrTooLarge is an object longer than its reader takes, which reading again will not change.
var ErrTooLarge = errors.New("it is longer than may be read")

// ErrStopped is a worker that stopped when asked to, rather than finishing or failing.
var ErrStopped = errors.New("the worker was stopped")

// Grant is what a TaskGrant binds: one attempt, its pool and identity, the task, where its outputs
// go, and until when.
type Grant struct {
	Attempt  string            `json:"attempt"`
	Pool     string            `json:"pool"`
	Identity string            `json:"identity"`
	Prefix   string            `json:"prefix"`
	Task     expand.RemoteTask `json:"task"`
	// Test is a query's tests to run instead of an analysis.
	Test *TestTask `json:"test,omitempty"`
	// Session is an authoring session to run instead of either.
	Session *SessionTask `json:"session,omitempty"`
	// PackSHA256 binds the operator's pack an analysis's PackFindings run from.
	PackSHA256 string `json:"packSha256,omitempty"`
	// AttemptKey is the public half of the key this attempt alone signs with, as PEM.
	AttemptKey string `json:"attemptKey"`
	IssuedAt   string `json:"issuedAt"`
	NotAfter   string `json:"notAfter"`
}

// TestTask is a query's own tests to run in a worker: the operator's pack, bound by its digest,
// and the test directories within it.
type TestTask struct {
	PackSHA256 string `json:"packSha256"`
	// Tests are slash paths relative to the pack's root.
	Tests []string `json:"tests"`
}

// Output is one file a worker returns, by its name under the attempt.
type Output struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	QueryID string `json:"queryId,omitempty"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

// The kinds of output.
const (
	KindEdges = "edges"
	KindExtra = "extra"
	KindSarif = "sarif"
	KindLock  = "lock"
	// What a test run leaves: its result, each .actual and each test's database.
	KindTestResult   = "test-result"
	KindActual       = "actual"
	KindTestDatabase = "test-database"
)

// Failure is why a worker produced nothing, in the class the coordinator settles it by.
type Failure struct {
	Kind   string `json:"kind"`
	Why    string `json:"why"`
	Ref    string `json:"ref,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// The classes of failure.
const (
	FailedNothing     = "nothing"
	FailedUnavailable = "unavailable"
	FailedNoSource    = "no-source"
	FailedRetryable   = "retryable"
)

// Result is what a ResultAttestation binds.
type Result struct {
	Attempt  string   `json:"attempt"`
	Grant    string   `json:"grant"`
	Identity string   `json:"identity"`
	Chain    string   `json:"chain"`
	Commit   string   `json:"commit,omitempty"`
	Ref      string   `json:"ref,omitempty"`
	Digest   string   `json:"digest,omitempty"`
	Outputs  []Output `json:"outputs"`
	Failure  *Failure `json:"failure,omitempty"`
}

// Receipt is the coordinator's word on one attempt's result: accepted for ingestion, as verified,
// or refused and why. Signed and logged after the grant it answers, refusals as much as acceptances.
type Receipt struct {
	Attempt string `json:"attempt"`
	Grant   string `json:"grant"`
	// Attestation is the sha256 of the signed result, empty when there was none to read.
	Attestation string `json:"attestation"`
	Repository  string `json:"repository,omitempty"`
	// Tests are the test directories a test attempt ran, comma-separated.
	Tests string `json:"tests,omitempty"`
	// Session is the authoring session a session attempt ran.
	Session int64  `json:"session,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Verdict string `json:"verdict"`
	// Outcome is the failure the worker reported, for a result that verified and analyzed nothing.
	Outcome string `json:"outcome,omitempty"`
	Reason  string `json:"reason,omitempty"`
	At      string `json:"at"`
}

// Receipt verdicts.
const (
	ReceiptAccepted = "accepted"
	ReceiptRefused  = "refused"
)

func attestationKey(prefix, attempt string) string { return prefix + attempt + "/attestation.json" }

// grantKey is where the coordinator keeps the grant it signed for an attempt, which a coordinator
// started since reads its attempt's key from, by the digest it kept.
func grantKey(prefix, attempt string) string { return prefix + attempt + "/grant.json" }

func outputKey(prefix, attempt, name string) string { return prefix + attempt + "/out/" + name }

// packKey is where a pack is read from, by its digest: every attempt granted the same pack reads one copy.
func packKey(prefix, sha256 string) string { return prefix + "packs/" + sha256 + ".tgz" }

func classify(err error) *Failure {
	var nothing *expand.NothingToAnalyze
	switch {
	case errors.As(err, &nothing):
		return &Failure{Kind: FailedNothing, Why: nothing.Why, Ref: nothing.Tree.Ref, Commit: nothing.Tree.SHA}
	case expand.IsUnavailable(err):
		return &Failure{Kind: FailedUnavailable, Why: err.Error()}
	case runner.IsNoSourceCode(err):
		return &Failure{Kind: FailedNoSource, Why: err.Error()}
	}
	return &Failure{Kind: FailedRetryable, Why: err.Error()}
}

// failureError is classify's inverse: the failure as the worker met it, in its class and with its
// own text, which is the reason the coordinator records.
func failureError(f *Failure) error {
	switch f.Kind {
	case FailedNothing:
		return &expand.NothingToAnalyze{Tree: expand.Tree{Ref: f.Ref, SHA: f.Commit}, Why: f.Why}
	case FailedUnavailable:
		return failed{f.Why, expand.ErrUnavailable}
	case FailedNoSource:
		return failed{f.Why, runner.ErrNoSourceCode}
	}
	return errors.New(f.Why)
}

// failed is a failure's own text, still the error it was raised as.
type failed struct {
	why string
	is  error
}

func (e failed) Error() string { return e.why }
func (e failed) Unwrap() error { return e.is }

// TokenSecret is the key a lent read token travels under in Launch.Secrets, and the environment
// variable the worker reads it from.
const TokenSecret = "GITHUB_TOKEN"

// Launch is one attempt to start: its grant, and the secrets its worker may hold.
type Launch struct {
	Attempt string
	Grant   authz.Envelope
	Secrets map[string]string
	// Sealed is a session whose agent is offered ABOM's tools and nothing else, a draft's: its agent
	// reads the pack and writes nowhere the worker reads.
	Sealed bool
}

// Launcher starts a worker for an attempt and says when it has finished. Stop asks the worker to stop,
// giving it time to report what it did; Wait then answers ErrStopped once it has.
type Launcher interface {
	Start(ctx context.Context, l Launch) error
	Wait(ctx context.Context, attempt string) error
	Stop(ctx context.Context, attempt string) error
	Cleanup(ctx context.Context, attempt string)
}

// AnalyzeFunc is the analysis a worker runs: expand.Expander.AnalyzeTask in production.
type AnalyzeFunc func(ctx context.Context, t expand.RemoteTask, dir string) (expand.RemoteOutput, error)

// TestFunc runs tests in a pack unpacked at root, as runner.Runner.RunTests does.
type TestFunc func(ctx context.Context, root string, tests []string) (runner.TestResult, error)

// RepositoryTokens lends a worker a read of the one repository it analyzes, or nothing where there is
// nothing to lend for it.
type RepositoryTokens interface {
	RepositoryToken(ctx context.Context, owner, repo string) (string, error)
}

// digest is how every byte string here is named: the hex of its SHA-256.
func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newAttempt() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
