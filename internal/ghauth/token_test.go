package ghauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The credentials that verified the operator's identity are used for API calls too.
func TestTheTokenFallsBackToTheCliThatVerifiedTheIdentity(t *testing.T) {
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "gh" && strings.Join(args, " ") == "auth token" {
			return []byte("gho_fromcli\n"), nil
		}
		return nil, errors.New("unexpected command")
	}

	got, err := Token(context.Background(), run)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}

	if got != "gho_fromcli" {
		t.Errorf("Token = %q, want the CLI's token with its newline trimmed", got)
	}
}

// Not being logged in is a fact the caller has to be able to act on. Returning an empty token and
// no error is what let an anonymous enumeration pass for an authorized one.
func TestNotBeingLoggedInIsAnError(t *testing.T) {
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return nil, errors.New("gh auth token: exit status 1: not logged in")
	}

	if _, err := Token(context.Background(), run); err == nil {
		t.Fatal("Token succeeded with no credentials")
	}
}

// A token that is present but blank is the same failure wearing a success exit code.
func TestABlankTokenIsNotACredential(t *testing.T) {
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte("  \n"), nil
	}

	if _, err := Token(context.Background(), run); err == nil {
		t.Fatal("a blank token was accepted")
	}
}
