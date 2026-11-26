package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/advanced-security/codeql-abom/internal/authz"
)

var _ authz.Ledger = (*Store)(nil)

// The log is the store's, chained as authz chains it, read oldest first from any point, and its head
// is the newest entry; an empty log has none.
func TestTheAuthorizationLogChainsEveryAssertionInOrder(t *testing.T) {
	s := newTestStore(t)
	if _, ok, err := s.Head(); err != nil || ok {
		t.Fatalf("an empty log has a head: %v, %v", ok, err)
	}
	at := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	digests := []string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)}
	chain := authz.Genesis
	for i, d := range digests {
		e, err := s.AppendAssertion(authz.PredicateDecision, d, []byte(`{"n":`+string(rune('1'+i))+`}`), at.Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		chain = authz.NextChain(chain, d)
		if e.Seq != int64(i+1) || e.Chain != chain {
			t.Errorf("entry %d: seq %d chain %s, want %d and %s", i, e.Seq, e.Chain, i+1, chain)
		}
	}

	all, err := s.Assertions(0, 0)
	if err != nil || len(all) != 3 || all[2].Chain != chain || all[0].RecordedAt != "2026-11-25T12:00:00Z" ||
		string(all[1].Envelope) != `{"n":2}` {
		t.Fatalf("assertions = %+v, %v", all, err)
	}
	page, err := s.Assertions(1, 1)
	if err != nil || len(page) != 1 || page[0].Seq != 2 {
		t.Errorf("one after the first = %+v, %v", page, err)
	}
	if head, ok, err := s.Head(); err != nil || !ok || head.Seq != 3 || head.Chain != chain {
		t.Errorf("head = %+v, %v, %v", head, ok, err)
	}
}

// The log is evidence, so the database itself refuses to change or remove an entry: a correction is
// a new assertion, never an edit.
func TestTheDatabaseRefusesToChangeOrRemoveAnAuthorizationEntry(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AppendAssertion(authz.PredicateDecision, strings.Repeat("a", 64), []byte(`{}`), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE abom_authz_log SET digest = 'forged'`,
		`DELETE FROM abom_authz_log`,
	} {
		if _, err := s.DB().Exec(statement); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%s: %v, want refused as append-only", statement, err)
		}
	}
	if all, err := s.Assertions(0, 0); err != nil || len(all) != 1 || all[0].Digest != strings.Repeat("a", 64) {
		t.Errorf("after the refused writes: %+v, %v", all, err)
	}
}

// Two processes deciding against one database, a server and a command beside it, each extend the
// log: an append waits for the other rather than failing on the head both of them read.
func TestAppendsFromTwoProcessesBothExtendTheLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abom.db")
	var handles []*Store
	for range 2 {
		s, err := openTestStore(t, path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		if err := s.Migrate(); err != nil {
			t.Fatal(err)
		}
		handles = append(handles, s)
	}
	const each = 25
	var wg sync.WaitGroup
	errs := make(chan error, 2*each)
	for _, s := range handles {
		for i := range each {
			wg.Add(1)
			go func() {
				defer wg.Done()
				digest := fmt.Sprintf("%064x", i)
				if _, err := s.AppendAssertion(authz.PredicateDecision, digest, []byte(`{}`), time.Now()); err != nil {
					errs <- err
				}
			}()
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("an append failed rather than waiting: %v", err)
	}
	entries, err := handles[0].Assertions(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range entries {
		if e.Seq != int64(i+1) {
			t.Fatalf("entry %d has seq %d; the log has a gap or a fork", i+1, e.Seq)
		}
	}
	if len(entries) != 2*each {
		t.Errorf("the log holds %d entries, want %d", len(entries), 2*each)
	}
}
