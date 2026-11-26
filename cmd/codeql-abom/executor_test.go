package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

// A worker is capped at what it is given, so it cannot take the coordinator's or the database's
// CPU, and CodeQL inside it is told that budget rather than sizing itself from the node.
func TestAWorkerIsCappedAtItsBudgetAndCodeQLIsToldIt(t *testing.T) {
	for name, c := range map[string]struct {
		cpu, cpuLimit, memory string
		limited               bool
		args                  []string
	}{
		"limit defaults to the request": {"1", "", "2Gi", true, []string{"--threads", "1", "--ram", "1228"}},
		"a fraction of a core is one":   {"500m", "", "2Gi", true, []string{"--threads", "1", "--ram", "1228"}},
		"a higher limit sets threads":   {"1", "3", "4Gi", true, []string{"--threads", "3", "--ram", "2457"}},
		"no limit uses the request":     {"2", "0", "2Gi", false, []string{"--threads", "2", "--ram", "1228"}},
	} {
		t.Run(name, func(t *testing.T) {
			res, args, err := poolFlags{prefix: "worker-", cpu: c.cpu, cpuLimit: c.cpuLimit, memory: c.memory,
				timeout: time.Hour}.budget()
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(args, c.args) {
				t.Errorf("args %v; want %v", args, c.args)
			}
			_, capped := res.Limits[corev1.ResourceCPU]
			if capped != c.limited {
				t.Errorf("CPU limited %v; want %v", capped, c.limited)
			}
			if res.Requests.Memory().Cmp(*res.Limits.Memory()) != 0 {
				t.Errorf("memory request %v and limit %v differ", res.Requests.Memory(), res.Limits.Memory())
			}
		})
	}
}

// A pool's budget is refused under the flag that set it: three pools are sized apart, and naming the
// crawl's flag for a test pool's mistake sends the fix to the wrong place.
func TestAPoolsBudgetIsRefusedUnderItsOwnFlag(t *testing.T) {
	ok := poolFlags{prefix: "test-worker-", cpu: "1", memory: "2Gi", timeout: time.Minute}
	for want, p := range map[string]poolFlags{
		"--test-worker-cpu: ":       {prefix: ok.prefix, cpu: "lots", memory: ok.memory, timeout: ok.timeout},
		"--test-worker-cpu-limit: ": {prefix: ok.prefix, cpu: ok.cpu, cpuLimit: "lots", memory: ok.memory, timeout: ok.timeout},
		"--test-worker-memory: ":    {prefix: ok.prefix, cpu: ok.cpu, memory: "lots", timeout: ok.timeout},
		"--test-worker-timeout ":    {prefix: ok.prefix, cpu: ok.cpu, memory: ok.memory},
	} {
		if _, _, err := p.budget(); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%+v: %v; want it refused as %q", p, err, want)
		}
	}
	if _, _, err := ok.budget(); err != nil {
		t.Errorf("%+v: %v", ok, err)
	}
}
