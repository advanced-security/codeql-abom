package expand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/customql"
	"github.com/advanced-security/codeql-abom/internal/runner"
)

// operatorsSuite is a crawl whose bound suite resolves to one query in the pack ABOM scaffolds,
// and the pack's directory.
func operatorsSuite(t *testing.T) (*Expander, string) {
	t.Helper()
	query := queryInPack(t, t.TempDir(), customql.Pack, "0.0.1", "CheckoutTagPin.ql", "select 1")
	return &Expander{Resolver: &fakeResolver{
		queries: []string{query},
		meta:    map[string]runner.Metadata{query: {ID: "acme/checkout-tag-pin", Kind: "problem"}},
	}}, filepath.Dir(query)
}

// A tier can be bound to the operator's suite before anybody has pressed Test, and the CLI then
// compiles none of it, so the round installs the pack before it digests it: the lock is part of
// what the digest covers.
func TestARoundOverTheOperatorsSuiteInstallsItsPackFirst(t *testing.T) {
	e, pack := operatorsSuite(t)
	installs := 0
	e.Install = func(context.Context) error {
		installs++
		return os.WriteFile(filepath.Join(pack, "codeql-pack.lock.yml"), []byte("lockVersion: 1.0.0\n"), 0o644)
	}

	planned, err := e.plan(context.Background(), Options{Suite: customql.Suite})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if installs != 1 {
		t.Errorf("installed the pack %d times, want once before the round", installs)
	}
	want, err := runner.PackDigest(pack)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.contents) != 1 || planned.contents[0].PackDigest != want {
		t.Errorf("the round records the pack as %+v, want it as it ran, lock included: %s",
			planned.contents, want)
	}
}

// Only the operator's suite needs the operator's pack.
func TestARoundOverShippedSuitesInstallsNothing(t *testing.T) {
	path := queryInPack(t, t.TempDir(), "codeql/actions-queries", "0.6.36", "Unpinned.ql", "select 1")
	e := &Expander{
		Resolver: &fakeResolver{
			queries: []string{path},
			meta:    map[string]runner.Metadata{path: {ID: "actions/unpinned-tag", Kind: "problem"}},
		},
		Install: func(context.Context) error {
			t.Error("the operator's pack was installed for a round of shipped queries")
			return nil
		},
	}
	if _, err := e.plan(context.Background(), Options{Suite: "abom-findings.qls"}); err != nil {
		t.Fatalf("plan: %v", err)
	}
}

// A pack that cannot be installed compiles nothing in the suite, so the round stops with the
// reason, naming the suite, rather than failing every repository with it.
func TestARoundWhosePackCannotBeInstalledStopsWithWhy(t *testing.T) {
	e, _ := operatorsSuite(t)
	e.Install = func(context.Context) error {
		return errors.New("codeql pack install: exit status 1: Failed to download codeql/actions-all@0.6.2")
	}
	_, err := e.plan(context.Background(), Options{Suite: customql.Suite})
	if err == nil || !strings.Contains(err.Error(), "Failed to download codeql/actions-all@0.6.2") ||
		!strings.Contains(err.Error(), customql.Suite) {
		t.Errorf("a round whose pack could not be installed returned %v, want the reason and the suite", err)
	}
}
