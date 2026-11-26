// Package sourcepolicy decides whether remote content may be acquired, refusing what it cannot confirm.
package sourcepolicy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
)

const RefusalPrefix = "source policy: "

type Reader interface {
	SourceAccess(context.Context, string) (catalog.SourceAccess, error)
	DefaultSetup(context.Context, string) (catalog.DefaultSetup, error)
	CheckScanningAccess(context.Context, string) error
}

// Policy is shared by the Git and API readers using the same credentials.
type Policy struct {
	Read     Reader
	Owned    func(string) (bool, error)
	Evidence func(string) (string, error)
	entries  sync.Map
}

type entry struct {
	mu       sync.Mutex
	at       time.Time
	meta     catalog.SourceAccess
	err      error
	probed   bool
	probeErr error
}

// Denied is a source left remote, not a failed CodeQL analysis.
type Denied struct {
	Repository string
	Reason     string
}

func (e *Denied) Error() string { return RefusalPrefix + e.Repository + ": " + e.Reason }

func IsDenied(err error) bool {
	var denied *Denied
	return errors.As(err, &denied)
}

func (p *Policy) item(repository string) *entry {
	e, _ := p.entries.LoadOrStore(strings.ToLower(repository), &entry{})
	return e.(*entry)
}

// Invalidate is also called by explicit retry, so an operator need not wait for the memo to expire.
func (p *Policy) Invalidate(repository string) {
	if p == nil {
		return
	}
	e := p.item(repository)
	e.mu.Lock()
	e.at = time.Time{}
	e.mu.Unlock()
}

func (p *Policy) InvalidateAll() {
	p.entries.Range(func(key, _ any) bool {
		p.Invalidate(key.(string))
		return true
	})
}

func (p *Policy) Check(ctx context.Context, repository string) error {
	_, err := p.Admission(ctx, repository)
	return err
}

// Admission decides whether a repository's content may be read and says why it may: public, or
// declared owned with evidence that code scanning is licensed there. Eligibility GitHub cannot
// confirm is refused; a read that failed for a reason a retry could change is returned as an error
// that is not a refusal, so the work is asked about again rather than settled.
func (p *Policy) Admission(ctx context.Context, repository string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e := p.item(repository)
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.at) >= 5*time.Minute {
		e.meta, e.err = p.Read.SourceAccess(ctx, repository)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		e.at, e.probed, e.probeErr = time.Now(), false, nil
	}
	name := e.meta.Repository
	if name == "" {
		name = repository
	}
	if e.err == nil && e.meta.Visibility == "public" {
		return "public", nil
	}
	owned, err := p.Owned(name)
	if err != nil {
		return "", fmt.Errorf("checking source ownership of %s: %w", name, err)
	}
	if !owned {
		reason := "not declared owned and not confirmed public; declare ownership or check public access"
		if e.err != nil {
			reason += ": " + e.err.Error()
		}
		return "", &Denied{name, reason}
	}
	if e.err != nil {
		return "", unconfirmed(name, e.err)
	}
	if e.meta.Visibility != "private" && e.meta.Visibility != "internal" {
		return "", &Denied{name, notConfirmed + "GitHub did not report the repository's visibility"}
	}
	return p.checkPrivate(ctx, e, name)
}

// notConfirmed opens every refusal of eligibility nobody could establish, so it reads as not known
// rather than as ineligible.
const notConfirmed = "code-scanning eligibility not confirmed: "

// unconfirmed is what a failed read of eligibility means: GitHub declining to say is a refusal, and
// anything else is asked again.
func unconfirmed(repository string, err error) error {
	var status *catalog.StatusError
	if errors.As(err, &status) && (status.Code == 401 || status.Code == 403 || status.Code == 404) {
		return &Denied{repository, notConfirmed + err.Error() + "; grant the App access to read it, then resync"}
	}
	return fmt.Errorf("the eligibility of %s could not be read, and is asked again: %w", repository, err)
}

func (p *Policy) checkPrivate(ctx context.Context, e *entry, repository string) (string, error) {
	const owned = "declared owned; "
	switch {
	case e.meta.CodeSecurity == "enabled":
		return owned + "GitHub reports Code Security enabled", nil
	case e.meta.AdvancedSecurity == "enabled":
		return owned + "GitHub reports Advanced Security enabled", nil
	case e.meta.CodeSecurity == "disabled" || e.meta.AdvancedSecurity == "disabled":
		return "", &Denied{repository, "GitHub reports Code Security/Advanced Security disabled; enable it and retry"}
	}
	if p.Evidence != nil {
		evidence, err := p.Evidence(repository)
		if err != nil {
			return "", fmt.Errorf("reading scanning evidence for %s: %w", repository, err)
		}
		if evidence != "" {
			return owned + evidence, nil
		}
	}
	if !e.probed {
		setup, err := p.Read.DefaultSetup(ctx, repository)
		switch {
		case scanningDisabled(err):
			e.probeErr = err
		case err == nil && setup.State == "configured":
			e.probeErr = nil
		default:
			e.probeErr = p.Read.CheckScanningAccess(ctx, repository)
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		e.probed = true
	}
	if scanningDisabled(e.probeErr) {
		return "", &Denied{repository, "GitHub reports code scanning disabled; enable it and retry"}
	}
	if e.probeErr != nil {
		return "", unconfirmed(repository, e.probeErr)
	}
	return owned + "code scanning answers for it", nil
}

func scanningDisabled(err error) bool {
	var status *catalog.StatusError
	if !errors.As(err, &status) || (status.Code != 403 && status.Code != 404) {
		return false
	}
	message := strings.ToLower(status.Message)
	for _, phrase := range []string{
		"code scanning is not enabled", "advanced security must be enabled",
		"code security must be enabled", "code security is not enabled",
	} {
		if strings.Contains(message, phrase) {
			return true
		}
	}
	return false
}
