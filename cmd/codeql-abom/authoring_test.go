package main

import (
	"io"
	"strings"
	"testing"
)

// Nobody watches each session in a cluster, so there a session is bounded at 500 AI credits unless a
// limit was given, and 0 given is no limit. The Copilot CLI refuses a limit under 30, so one is
// refused before any session could fail on it, in either place; and at least one session works at once.
func TestSessionCreditsAreBoundedInAClusterAndNeverBelowWhatTheCLITakes(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		credits float64
		refused string
	}{
		{args: []string{"--executor", "kubernetes"}, credits: 500},
		{args: []string{"--executor", "kubernetes", "--authoring-credits", "0"}, credits: 0},
		{args: []string{"--executor", "kubernetes", "--authoring-credits", "120"}, credits: 120},
		{args: nil, credits: 0},
		{args: []string{"--authoring-credits", "10"}, refused: "30"},
		{args: []string{"--executor", "kubernetes", "--authoring-credits", "29.5"}, refused: "30"},
		{args: []string{"--authoring-credits", "-1"}, refused: "credits"},
		{args: []string{"--authoring-concurrency", "0"}, refused: "concurrency"},
	} {
		cfg, err := parseServeArgs(append([]string{"--db", "abom.db"}, tc.args...), io.Discard)
		switch {
		case tc.refused != "":
			if err == nil || !strings.Contains(err.Error(), tc.refused) {
				t.Errorf("%v answered %v; want it refused naming %s", tc.args, err, tc.refused)
			}
		case err != nil:
			t.Errorf("%v: %v", tc.args, err)
		case cfg.authoring.credits != tc.credits:
			t.Errorf("%v bounds a session at %v credits, want %v", tc.args, cfg.authoring.credits, tc.credits)
		}
	}
}
