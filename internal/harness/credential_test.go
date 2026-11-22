package harness

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
)

// mintRecorder hands out a new installation token per mint and remembers which mints were refreshes.
type mintRecorder struct {
	refreshes []bool
	err       error
}

func (m *mintRecorder) mint(_ context.Context, refresh bool) (string, time.Time, error) {
	m.refreshes = append(m.refreshes, refresh)
	if m.err != nil {
		return "", time.Time{}, m.err
	}
	return "ghs_minted_" + string(rune('a'+len(m.refreshes)-1)), time.Now().Add(time.Hour), nil
}

// With an App, the Copilot client carries the installation token minted for the session — the CLI
// refuses one from a token provider, which must name an account and an installation token names none —
// and the child never finds a person's token in its environment, where an inherited GH_TOKEN would win.
func TestAnAppMintedSessionCarriesTheInstallationTokenAndInheritsNoGitHubToken(t *testing.T) {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN", "COPILOT_GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN"} {
		t.Setenv(name, "ghp_a_persons_token")
	}
	c := aCopilot()
	c.Mint = (&mintRecorder{}).mint

	opts := c.clientOptions(aSetup(t), "ghs_minted_a")
	if opts.GitHubToken != "ghs_minted_a" || opts.UseLoggedInUser == nil || *opts.UseLoggedInUser {
		t.Errorf("client token %q, logged-in user %v; want the installation token and logged-in user off",
			opts.GitHubToken, opts.UseLoggedInUser)
	}
	conn, ok := opts.Connection.(copilot.StdioConnection)
	if !ok || conn.Path != c.CLI || conn.Env == nil {
		t.Fatalf("connection %#v: want the CLI over stdio with an environment named for it", opts.Connection)
	}
	for _, kv := range conn.Env {
		if strings.Contains(kv, "ghp_a_persons_token") {
			t.Errorf("the Copilot child inherits %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
	if !strings.Contains(strings.Join(conn.Env, "\n"), "PATH=") {
		t.Error("the Copilot child lost PATH along with the tokens")
	}
}

// An installation token is not refreshed under a session, so the session ends while it is still good.
func TestAnAppSessionEndsBeforeItsTokenDoes(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	ctx, cancel := untilExpiry(context.Background(), expires)
	defer cancel()
	if deadline, ok := ctx.Deadline(); !ok || !deadline.Before(expires) || deadline.Before(expires.Add(-10*time.Minute)) {
		t.Errorf("deadline %v, want shortly before the token's expiry %v", deadline, expires)
	}
	sooner, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	ctx, cancel = untilExpiry(sooner, expires)
	defer cancel()
	if deadline, _ := ctx.Deadline(); deadline.After(time.Now().Add(time.Minute)) {
		t.Errorf("an earlier limit was lengthened to %v", deadline)
	}
}

// The configured model runs where the runtime offers it; where it does not, a session still runs —
// on the runtime's own choice — and says which model ran and why, rather than failing to start.
func TestASessionRunsOnAModelTheRuntimeOffers(t *testing.T) {
	for name, c := range map[string]struct {
		want, got string
		listed    []string
		why       bool
	}{
		"offered":                  {"claude-opus-5", "claude-opus-5", []string{"auto", "claude-opus-5"}, false},
		"not offered, auto is":     {"claude-opus-5", "auto", []string{"auto", "gpt-5.3-codex"}, true},
		"not offered, nor auto":    {"claude-opus-5", "gpt-5.3-codex", []string{"gpt-5.3-codex"}, true},
		"nothing configured, auto": {"", "auto", []string{"gpt-5.3-codex", "auto"}, true},
	} {
		got, why := chooseModel(c.want, c.listed)
		if got != c.got || (why != "") != c.why {
			t.Errorf("%s: chose %q (%q), want %q", name, got, why, c.got)
		}
	}
}

// A list that could not be read, or that offers nothing, is not a list that offers the flag's model:
// the session is refused with why rather than started on a model nobody said it may run.
func TestAModelListThatCannotBeReadRefusesTheSession(t *testing.T) {
	for name, c := range map[string]struct {
		listed []string
		err    error
	}{
		"unread":  {nil, errors.New("models.list failed")},
		"nothing": {[]string{}, nil},
	} {
		if model, err := modelFor("claude-opus-5", c.listed, c.err); err == nil || model != "" {
			t.Errorf("%s: chose %q, %v; want a refusal", name, model, err)
		}
	}
	if model, err := modelFor("claude-opus-5", []string{"auto"}, nil); err != nil || model != "auto" {
		t.Errorf("a read list chose %q, %v", model, err)
	}
}

// Without an App, the local and development path keeps the operator's own token as before.
func TestWithoutAnAppASessionKeepsTheOperatorsToken(t *testing.T) {
	c := aCopilot()
	opts := c.clientOptions(aSetup(t), "gho_operator")
	if opts.GitHubToken != "gho_operator" || opts.UseLoggedInUser != nil {
		t.Errorf("client token %q, logged-in user %v; want the operator's token as before",
			opts.GitHubToken, opts.UseLoggedInUser)
	}
	if conn, ok := opts.Connection.(copilot.StdioConnection); !ok || conn.Env != nil {
		t.Errorf("connection %#v: want the inherited environment, unchanged", opts.Connection)
	}
}

// A missing grant or an organization policy refusing App tokens refuses the session before the CLI
// starts, with the reason, and is not "no agent" or a 403 discovered mid-turn.
func TestAnAppTokenThatCannotBeMintedRefusesTheSessionBeforeTheCLIStarts(t *testing.T) {
	refused := errors.New("the App's installation on has-ghas has not granted organization_copilot_requests: read")
	c := aCopilot()
	c.CLI = "/nonexistent/copilot"
	c.Mint = (&mintRecorder{err: refused}).mint

	_, err := c.Run(context.Background(), aSetup(t))
	if !errors.Is(err, refused) || errors.Is(err, ErrNoAgent) {
		t.Errorf("Run = %v; want the mint's refusal, not no agent", err)
	}
}

// A session starts on a token minted for it, not one cached from earlier with minutes left: a
// session the default limit allows then ends inside that token's hour, whatever the runtime refreshes.
func TestAnAppSessionStartsOnAFreshlyMintedToken(t *testing.T) {
	m := &mintRecorder{}
	c := aCopilot()
	c.CLI = "/nonexistent/copilot"
	c.Mint = m.mint

	_, _ = c.Run(context.Background(), aSetup(t))
	if len(m.refreshes) == 0 || !m.refreshes[0] {
		t.Errorf("mints before the CLI started: %v; want a fresh one", m.refreshes)
	}
	if DefaultLimit >= time.Hour-5*time.Minute {
		t.Errorf("the default limit %s outlasts a fresh installation token's hour less its margin", DefaultLimit)
	}
}
