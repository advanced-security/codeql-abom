package sourcepolicy

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
)

type reader struct {
	meta        catalog.SourceAccess
	metaErr     error
	setup       catalog.DefaultSetup
	setupErr    error
	scanningErr error
	reads       int
	probes      int
}

func (r *reader) SourceAccess(context.Context, string) (catalog.SourceAccess, error) {
	r.reads++
	return r.meta, r.metaErr
}
func (r *reader) DefaultSetup(context.Context, string) (catalog.DefaultSetup, error) {
	return r.setup, r.setupErr
}
func (r *reader) CheckScanningAccess(context.Context, string) error {
	r.probes++
	return r.scanningErr
}

func TestRemoteSourcingUsesAnySufficientEvidenceWithoutBlockingUnknownLicensing(t *testing.T) {
	for _, tt := range []struct {
		name, visibility, codeSecurity, advanced, setup, recorded string
		owned, denied, warning                                    bool
		metaErr, setupErr, scanningErr                            error
	}{
		{name: "public unowned", visibility: "public"},
		{name: "private unowned", visibility: "private", denied: true},
		{name: "internal unowned", visibility: "internal", denied: true},
		{name: "enabled before first alert", visibility: "private", owned: true, codeSecurity: "enabled"},
		{name: "legacy GHAS", visibility: "private", owned: true, advanced: "enabled"},
		{name: "configured other language", visibility: "private", owned: true, setup: "configured"},
		{name: "recorded configuration", visibility: "private", owned: true, recorded: "default setup is configured"},
		{name: "existing alerts or empty successful list", visibility: "private", owned: true},
		{name: "default setup off advanced works", visibility: "private", owned: true, setup: "not-configured"},
		{name: "explicit disabled", visibility: "private", owned: true, codeSecurity: "disabled", denied: true},
		{name: "disable supersedes recorded setup", visibility: "private", owned: true, codeSecurity: "disabled",
			recorded: "default setup is configured", denied: true},
		{name: "scanning disabled response", visibility: "private", owned: true, denied: true,
			scanningErr: &catalog.StatusError{Code: 403, Message: "Code scanning is not enabled for this repository"}},
		{name: "setup reports license disabled", visibility: "private", owned: true, denied: true,
			setupErr: &catalog.StatusError{Code: 403, Message: "Advanced Security must be enabled for this repository"}},
		{name: "permission unknown", visibility: "private", owned: true, warning: true,
			scanningErr: &catalog.StatusError{Code: 403, Message: "Resource not accessible by integration"}},
		{name: "ambiguous missing", visibility: "private", owned: true, warning: true,
			scanningErr: &catalog.StatusError{Code: 404}},
		{name: "metadata unknown owned", owned: true, warning: true, metaErr: errors.New("rate limited")},
		{name: "metadata unknown unowned", denied: true, metaErr: errors.New("rate limited")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &reader{meta: catalog.SourceAccess{Repository: "acme/repo", Visibility: tt.visibility,
				CodeSecurity: tt.codeSecurity, AdvancedSecurity: tt.advanced},
				metaErr: tt.metaErr, setup: catalog.DefaultSetup{State: tt.setup, Languages: []string{"go"}},
				setupErr: tt.setupErr, scanningErr: tt.scanningErr}
			warnings := 0
			p := &Policy{Read: r, Owned: func(string) (bool, error) { return tt.owned, nil },
				Evidence: func(string) (string, error) { return tt.recorded, nil },
				Warn:     func(string, string) { warnings++ }}
			for range 3 {
				err := p.Check(context.Background(), "acme/repo")
				if IsDenied(err) != tt.denied || (err != nil && !tt.denied) {
					t.Fatalf("Check = %v, want denied=%v", err, tt.denied)
				}
			}
			wantWarnings := 0
			if tt.warning {
				wantWarnings = 1
			}
			if warnings != wantWarnings || r.reads != 1 || r.probes > 1 {
				t.Errorf("warnings=%d reads=%d probes=%d", warnings, r.reads, r.probes)
			}
			if (!tt.owned || tt.visibility == "public") && r.probes != 0 {
				t.Error("asked scanning about a public or unowned repository")
			}
		})
	}
}

func TestAnOwnershipChangeAndAConfigurationRefreshChangeTheNextRemoteDecision(t *testing.T) {
	r := &reader{meta: catalog.SourceAccess{Repository: "acme/repo", Visibility: "private", CodeSecurity: "enabled"}}
	owned := true
	p := &Policy{Read: r, Owned: func(string) (bool, error) { return owned, nil }}
	if err := p.Check(context.Background(), "acme/repo"); err != nil {
		t.Fatal(err)
	}
	owned = false
	if err := p.Check(context.Background(), "acme/repo"); !IsDenied(err) {
		t.Fatalf("removed ownership: %v", err)
	}
	owned = true
	r.meta.CodeSecurity = "disabled"
	p.Invalidate("ACME/REPO")
	if err := p.Check(context.Background(), "acme/repo"); !IsDenied(err) {
		t.Fatalf("disabled: %v", err)
	}
	r.meta.CodeSecurity = "enabled"
	p.Invalidate("acme/repo")
	if err := p.Check(context.Background(), "acme/repo"); err != nil {
		t.Fatal(err)
	}
}

func TestATransferredPrivateRepositoryUsesItsCurrentOwnership(t *testing.T) {
	r := &reader{meta: catalog.SourceAccess{Repository: "stranger/repo", Visibility: "private", CodeSecurity: "enabled"}}
	p := &Policy{Read: r, Owned: func(name string) (bool, error) { return name == "acme/repo", nil }}
	if err := p.Check(context.Background(), "acme/repo"); !IsDenied(err) {
		t.Fatalf("transfer: %v", err)
	}
}

func TestConcurrentReferencesShareOneEligibilityRead(t *testing.T) {
	r := &reader{meta: catalog.SourceAccess{Repository: "acme/repo", Visibility: "private"}}
	p := &Policy{Read: r, Owned: func(string) (bool, error) { return true, nil }}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := p.Check(context.Background(), "acme/repo"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if r.reads != 1 || r.probes != 1 {
		t.Fatalf("reads=%d probes=%d, want one each", r.reads, r.probes)
	}
}

func TestCancelingASourceCheckDoesNotPermitOrCacheIt(t *testing.T) {
	r := &reader{metaErr: context.Canceled}
	p := &Policy{Read: r, Owned: func(string) (bool, error) { return true, nil }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Check(ctx, "acme/repo"); !errors.Is(err, context.Canceled) {
		t.Fatal(fmt.Errorf("canceled check = %v", err))
	}
}
