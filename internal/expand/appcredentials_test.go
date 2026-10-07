package expand

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

type installedTokens struct {
	own  map[string]string
	lent string
	err  error
}

func (s installedTokens) Token(_ context.Context, owner string) (string, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	if token, ok := s.own[owner]; ok {
		return token, false, nil
	}
	return s.lent, true, nil
}

func (s installedTokens) AccountToken(_ context.Context, owner string) (string, error) {
	return "", ghapp.ErrNotInstalled
}

// gitRecorder is a git stand-in recording each call's arguments and the token its environment carried.
func gitRecorder(t *testing.T, exit int) (string, func() [][]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the git stand-in is a shell script")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	stub := filepath.Join(dir, "git")
	script := "#!/bin/sh\n" +
		"{ printf 'token=%s' \"${ABOM_GIT_TOKEN-unset}\"; for a in \"$@\"; do printf '\\037%s' \"$a\"; done; printf '\\n'; } >> " + log + "\n" +
		"for r in \"$@\"; do printf '0123456789abcdef0123456789abcdef01234567\\t%s\\n' \"$r\"; done\n" +
		"exit " + string(rune('0'+exit)) + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub, func() [][]string {
		data, _ := os.ReadFile(log)
		var calls [][]string
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				calls = append(calls, strings.Split(line, "\x1f"))
			}
		}
		return calls
	}
}

// With no App, git runs exactly as it did before one existed: nothing before the subcommand, no token, the machine's own setup.
func TestWithoutAnAppGitRunsExactlyAsBefore(t *testing.T) {
	stub, calls := gitRecorder(t, 0)
	c := &Checkout{Git: stub}
	if _, err := c.Resolve(context.Background(), "octo", "repo", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, _ = c.Fetch(context.Background(), filepath.Join(t.TempDir(), "w"), "octo", "repo", "v4")
	got := calls()
	if len(got) < 3 {
		t.Fatalf("only %d git calls recorded: %q", len(got), got)
	}
	for _, call := range got {
		if call[0] != "token=unset" || !slices.Contains([]string{"ls-remote", "clone", "sparse-checkout", "checkout",
			"fetch", "rev-parse"}, call[1]) {
			t.Errorf("git was called as %q; with no App nothing may precede the subcommand or reach its environment", call)
		}
	}
}

// With an App every git call carries the owner's or the lent token by environment; a missing credential is retryable.
func TestWithAnAppEveryGitCallAuthenticatesAsTheOwnersInstallationOrTheLentOne(t *testing.T) {
	stub, calls := gitRecorder(t, 0)
	c := &Checkout{Git: stub, Tokens: installedTokens{own: map[string]string{"has-ghas": "own-has-ghas"}, lent: "lent-designated"}}
	ctx := context.Background()
	if _, err := c.Resolve(ctx, "has-ghas", "widgets", ""); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := c.Resolve(ctx, "actions", "checkout", "v4"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_, _ = c.Fetch(ctx, filepath.Join(t.TempDir(), "w"), "actions", "checkout", "v4")

	got := calls()
	if len(got) < 4 {
		t.Fatalf("only %d git calls recorded: %q", len(got), got)
	}
	for i, call := range got {
		want := "token=lent-designated"
		if i == 0 {
			want = "token=own-has-ghas"
		}
		if call[0] != want {
			t.Errorf("call %d %q carried %s, want %s", i, call[1:], call[0], want)
		}
		if len(call) < 5 || call[1] != "-c" || call[2] != "credential.helper=" ||
			!strings.HasPrefix(call[4], "credential.https://github.com.helper=") {
			t.Errorf("call %d %q does not clear the machine's helpers and answer as the installation", i, call[1:])
		}
		for _, arg := range call[1:] {
			if strings.Contains(arg, "own-has-ghas") || strings.Contains(arg, "lent-designated") {
				t.Errorf("call %d put a token in argv: %q", i, arg)
			}
		}
	}

	failing, _ := gitRecorder(t, 2)
	c.Git = failing
	_, err := c.Resolve(ctx, "acme", "private", "")
	if !IsUnavailable(err) || !errors.Is(err, ghapp.ErrNotInstalled) || !strings.Contains(err.Error(), "acme") {
		t.Errorf("a lent token refused = %v; want unavailable, naming acme as not installed", err)
	}
	c.Tokens = installedTokens{err: ghapp.ErrNoInstallation}
	_, err = c.Resolve(ctx, "actions", "checkout", "v4")
	if IsUnavailable(err) || !errors.Is(err, ghapp.ErrNoInstallation) {
		t.Errorf("no credential to be had = %v; want a retryable failure, not an unavailable repository", err)
	}
	_, err = c.Fetch(ctx, filepath.Join(t.TempDir(), "w"), "actions", "checkout", "v4")
	if IsUnavailable(err) || !errors.Is(err, ghapp.ErrNoInstallation) {
		t.Errorf("no credential to be had for a fetch = %v; want a retryable failure", err)
	}
}
