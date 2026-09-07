package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// A tested package nothing calls reads as implemented.
//
// `internal/archive` was a complete pipeline with its own tests and testdata that no binary
// reached, and it is why the findings lane looked finished for as long as it did: reading it
// answered "does this work", never "does this run". Nothing in a build, a test run or a review
// diff distinguishes the two, so it is asserted here.
func TestEveryInternalPackageIsReachableFromABinary(t *testing.T) {
	// Module-qualified rather than ./cmd/...: a test runs in its own package directory, where a
	// relative pattern names the wrong tree or nothing at all.
	module := goList(t, "-m")[0]

	reachable := map[string]bool{}
	for _, pkg := range goList(t, "-deps", module+"/cmd/...") {
		reachable[pkg] = true
	}

	var orphans []string
	for _, pkg := range goList(t, module+"/internal/...") {
		if !reachable[pkg] {
			orphans = append(orphans, pkg)
		}
	}
	if len(orphans) > 0 {
		t.Errorf("no command reaches %s.\n"+
			"Give it one or delete it. A package reachable only from its own tests is dead code "+
			"that reviews as working.", strings.Join(orphans, ", "))
	}
}

func goList(t *testing.T, args ...string) []string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Fields(string(out))
}
