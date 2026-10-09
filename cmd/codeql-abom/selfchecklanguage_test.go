package main

import (
	"io"
	"strings"
	"testing"
)

// The default stays `actions`, because the workflow and `make selfcheck` both call this with no
// language and must keep analyzing what they always analyzed.
func TestSelfcheckStillDefaultsToTheActionsWorkflows(t *testing.T) {
	cfg, err := parseSelfcheck(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if cfg.language != "actions" {
		t.Errorf("language = %q, want actions", cfg.language)
	}
}

// Naming a language without naming a suite must fail rather than analyze.
//
// `runner.DefaultSuite` is the *actions* findings suite, so `--language=go` on its own would build
// a Go database and run Actions queries against it — which reports zero findings and looks exactly
// like a clean result. A language and a suite are one decision and have to be made together.
func TestSelfcheckRefusesALanguageWhoseSuiteWasNotNamed(t *testing.T) {
	_, err := parseSelfcheck([]string{"--language=go"}, io.Discard)
	if err == nil {
		t.Fatal("a language with no suite was accepted, so Actions queries would run against Go")
	}
	if !strings.Contains(err.Error(), "suite") {
		t.Errorf("error = %q, want it to name the missing suite", err)
	}
}

func TestSelfcheckAcceptsALanguageWithItsOwnSuite(t *testing.T) {
	cfg, err := parseSelfcheck([]string{
		"--language=go", "--suite=codeql/go-queries:codeql-suites/go-code-quality.qls",
	}, io.Discard)
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if cfg.language != "go" {
		t.Errorf("language = %q, want go", cfg.language)
	}
	if !strings.Contains(cfg.suite, "go-code-quality") {
		t.Errorf("suite = %q, want the suite that was asked for", cfg.suite)
	}
}

// CodeQL sizes its own default from the machine, and on a hosted runner that left the C# suite
// 692 MiB of heap. The workflow states the budget instead, as code scanning's own action does, and
// a budget nobody stated is still CodeQL's to choose.
func TestSelfcheckGivesCodeQLTheMemoryItIsTold(t *testing.T) {
	cfg, err := parseSelfcheck([]string{"--ram=14336"}, io.Discard)
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if r := selfcheckRunner(cfg); r.RAMMB != 14336 {
		t.Errorf("CodeQL was given %d MB, want the 14336 asked for", r.RAMMB)
	}
	cfg, err = parseSelfcheck(nil, io.Discard)
	if err != nil {
		t.Fatalf("parseSelfcheck: %v", err)
	}
	if r := selfcheckRunner(cfg); r.RAMMB != 0 {
		t.Errorf("with no --ram CodeQL was given %d MB, want it left to CodeQL", r.RAMMB)
	}
	if _, err := parseSelfcheck([]string{"--ram=-1"}, io.Discard); err == nil {
		t.Error("a negative budget was accepted, and would be passed over in silence")
	}
}
