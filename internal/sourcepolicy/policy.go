// Package sourcepolicy makes best-effort eligibility checks before remote content is acquired.
package sourcepolicy

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	Warn     func(repository, reason string)
	entries  sync.Map
}

type entry struct {
	mu       sync.Mutex
	at       time.Time
	meta     catalog.SourceAccess
	err      error
	probed   bool
	probeErr error
	warning  string
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
	if err := ctx.Err(); err != nil {
		return err
	}
	e := p.item(repository)
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.at) >= 5*time.Minute {
		e.meta, e.err = p.Read.SourceAccess(ctx, repository)
		if err := ctx.Err(); err != nil {
			return err
		}
		e.at, e.probed, e.probeErr = time.Now(), false, nil
	}
	name := e.meta.Repository
	if name == "" {
		name = repository
	}
	if e.err == nil && e.meta.Visibility == "public" {
		e.warning = ""
		return nil
	}
	owned, err := p.Owned(name)
	if err != nil {
		return fmt.Errorf("checking source ownership of %s: %w", name, err)
	}
	if !owned {
		reason := "not declared owned and not confirmed public; declare ownership or check public access"
		if e.err != nil {
			reason += ": " + e.err.Error()
		}
		return &Denied{name, reason}
	}
	if e.err != nil {
		p.warn(e, name, e.err.Error())
		return nil
	}
	if e.meta.Visibility != "private" && e.meta.Visibility != "internal" {
		p.warn(e, name, "GitHub did not report repository visibility")
		return nil
	}
	return p.checkPrivate(ctx, e, name)
}

func (p *Policy) checkPrivate(ctx context.Context, e *entry, repository string) error {
	if e.meta.CodeSecurity == "enabled" || e.meta.AdvancedSecurity == "enabled" {
		e.warning = ""
		return nil
	}
	if e.meta.CodeSecurity == "disabled" || e.meta.AdvancedSecurity == "disabled" {
		return &Denied{repository, "GitHub reports Code Security/Advanced Security disabled; enable it and retry"}
	}
	if p.Evidence != nil {
		evidence, err := p.Evidence(repository)
		if err != nil {
			return fmt.Errorf("reading scanning evidence for %s: %w", repository, err)
		}
		if evidence != "" {
			e.warning = ""
			return nil
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
			return err
		}
		e.probed = true
	}
	if scanningDisabled(e.probeErr) {
		return &Denied{repository, "GitHub reports code scanning disabled; enable it and retry"}
	}
	if e.probeErr != nil {
		p.warn(e, repository, e.probeErr.Error())
	} else {
		e.warning = ""
	}
	return nil
}

func (p *Policy) warn(e *entry, repository, reason string) {
	if e.warning == reason {
		return
	}
	e.warning = reason
	message := "code-scanning eligibility could not be confirmed; continuing for this declared repository: " + reason
	if p.Warn != nil {
		p.Warn(repository, message)
	} else {
		log.Printf("source policy: %s: %s", repository, message)
	}
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
