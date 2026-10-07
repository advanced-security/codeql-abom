package main

import (
	"path/filepath"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// A rate the operator saved outranks the startup flag, the flag fills in only where nothing was
// saved, and a pool given neither keeps no rate at all.
func TestASavedRateOutranksTheFlagAndAPoolWithNeitherHasNone(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "abom.db"), store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPoolRate(store.PoolRate{Pool: work.PoolProbing, Unit: work.UnitGit, PerHour: 90, Burst: 3}); err != nil {
		t.Fatal(err)
	}
	pools := work.NewTokenPools()

	if err := configurePools(pools, s, 600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		pool, unit     string
		perHour, burst int
	}{
		{work.PoolProbing, work.UnitGit, 90, 3},
		{work.PoolFetching, work.UnitGit, 600, 1},
		{work.PoolAdvisories, work.UnitREST, 0, 1},
		{work.PoolEnumeration, work.UnitGraphQL, 0, 1},
	} {
		if perHour, burst := pools.Pool(tc.pool, tc.unit).Rate(); perHour != tc.perHour || burst != tc.burst {
			t.Errorf("%s in %s runs at %d with burst %d, want %d with burst %d",
				tc.pool, tc.unit, perHour, burst, tc.perHour, tc.burst)
		}
	}
}
