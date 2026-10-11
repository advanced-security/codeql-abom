package lockverify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// Budget. A network verification is a round of requests to GitHub for every pin, so one commit is
// not asked about twice within the hour and a day has a ceiling. The offline check reads only the
// tree, and is spaced only enough that a double press is not two runs.
const (
	NetworkCooldown = time.Hour
	LocalCooldown   = 10 * time.Minute
	NetworkPerDay   = 50
)

// Refusals, each a reason rather than a failure: nothing was run.
var (
	ErrNothingToVerify = errors.New(
		"no lockfile was read at the commit this repository resolves to, so there is nothing to verify")
	ErrTooSoon = errors.New("this commit was verified in this mode too recently to run again")
	ErrBudget  = errors.New("today's network verifications are spent")
)

// Service runs a verification somebody asked for, within the budget, and keeps the answer.
type Service struct {
	Store store.Backend
	// Fetch materializes owner/repo at commit into dest, which exists and is empty.
	Fetch  func(ctx context.Context, dest, owner, repo, commit string) error
	Runner Runner
	Now    func() time.Time
}

type verificationRequest struct {
	repository string
	owner      string
	repo       string
	commit     string
	mode       string
	now        time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Request verifies the commit a repository currently resolves to, if a lockfile was read there.
func (s *Service) Request(ctx context.Context, repository, mode, by string) (store.LockVerification, error) {
	req, err := s.verificationRequest(repository, mode)
	if err != nil {
		return store.LockVerification{}, err
	}
	if last, ok, err := s.verifyAllowed(req); err != nil || ok {
		return last, err
	}
	result := s.verify(ctx, req.owner, req.repo, req.commit, mode)
	return s.recordVerification(req, result, by)
}

func (s *Service) verificationRequest(repository, mode string) (verificationRequest, error) {
	if mode != ModeLocal && mode != ModeNetwork {
		return verificationRequest{}, fmt.Errorf("no verification mode %q: say %s or %s",
			mode, ModeLocal, ModeNetwork)
	}
	owner, repo, ok := strings.Cut(strings.TrimSpace(repository), "/")
	if !ok || owner == "" || repo == "" {
		return verificationRequest{}, fmt.Errorf("name a repository as owner/name")
	}
	commit, ok, err := s.Store.CurrentLockfileCommit(repository)
	if err != nil {
		return verificationRequest{}, err
	}
	if !ok {
		return verificationRequest{}, ErrNothingToVerify
	}
	return verificationRequest{
		repository: repository, owner: owner, repo: repo, commit: commit, mode: mode, now: s.now(),
	}, nil
}

func (s *Service) verifyAllowed(req verificationRequest) (store.LockVerification, bool, error) {
	cooldown := LocalCooldown
	if req.mode == ModeNetwork {
		cooldown = NetworkCooldown
	}
	last, seen, err := s.Store.LastLockVerification(req.repository, req.commit, req.mode)
	if err != nil {
		return store.LockVerification{}, false, err
	}
	if seen && req.now.Sub(last.VerifiedAt) < cooldown {
		return last, true, fmt.Errorf("%w: last run %s", ErrTooSoon, last.VerifiedAt.Format(time.RFC3339))
	}
	if req.mode != ModeNetwork {
		return store.LockVerification{}, false, nil
	}
	return store.LockVerification{}, false, s.networkBudget(req.now)
}

func (s *Service) networkBudget(now time.Time) error {
	spent, err := s.Store.LockVerificationsSince(ModeNetwork, now.Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if spent >= NetworkPerDay {
		return fmt.Errorf("%w: %d in the last day", ErrBudget, spent)
	}
	return nil
}

func (s *Service) recordVerification(
	req verificationRequest, result Result, by string,
) (store.LockVerification, error) {
	v := store.LockVerification{
		Repository: req.repository, Commit: req.commit, Mode: req.mode, Outcome: result.Outcome,
		ToolVersion: result.ToolVersion, LockfileVersion: result.LockfileVersion,
		Valid: result.Valid, Error: result.Error, RequestedBy: by, VerifiedAt: req.now,
		Findings: make([]store.LockVerificationFinding, 0, len(result.Findings)),
	}
	for _, f := range result.Findings {
		v.Findings = append(v.Findings, store.LockVerificationFinding{
			Workflow: f.Workflow, Category: f.Category, Severity: f.Severity,
			Confidence: f.Confidence, Dependency: f.Dependency, RequiredBy: f.RequiredBy,
			Detail: f.Detail, Known: f.Known,
		})
	}
	id, err := s.Store.RecordLockVerification(v)
	if err != nil {
		return store.LockVerification{}, err
	}
	v.ID = id
	return v, nil
}

// verify fetches the tree and runs the tool on it. A tree that cannot be fetched is an error
// outcome with its reason, so the attempt is on record.
func (s *Service) verify(ctx context.Context, owner, repo, commit, mode string) Result {
	dir, err := os.MkdirTemp("", "abom-lockverify-tree-*")
	if err != nil {
		return Result{Mode: mode, Outcome: OutcomeError, Error: err.Error(), Findings: []Finding{}}
	}
	defer os.RemoveAll(dir)
	if err := s.Fetch(ctx, dir, owner, repo, commit); err != nil {
		return Result{Mode: mode, Outcome: OutcomeError, Findings: []Finding{},
			Error: "fetching the tree at " + commit + ": " + err.Error()}
	}
	return s.Runner.Verify(ctx, dir, mode)
}
