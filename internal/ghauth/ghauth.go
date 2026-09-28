// Package ghauth reads who the operator is from the gh CLI.
//
// ABOM records what it can attribute and declines to assert the rest, and account ownership is
// no different: an account counts as the operator's because GitHub says they belong to it, never
// because someone typed its name. Everything here is derived from an authenticated `gh` call.
//
// The token itself is never read, logged or stored. Only the facts it establishes leave this
// package.
package ghauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ErrNotInstalled reports that the gh CLI is not on PATH.
var ErrNotInstalled = errors.New("the gh CLI is not installed")

// Runner executes a command and returns its standard output. Injected so that verification is
// testable without a network, a token or gh itself.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Account is an account the authenticated user is verified to belong to.
type Account struct {
	Owner string `json:"owner"`
	// Kind is "user" for the operator's own account, "organization" otherwise.
	Kind string `json:"kind"`
	// Role is "owner" for their own account, and GitHub's "admin" or "member" for an org.
	Role string `json:"role"`
}

// Identity is who gh is authenticated as, and what that account belongs to.
type Identity struct {
	Host       string    `json:"host"`
	Login      string    `json:"login"`
	VerifiedAt time.Time `json:"verifiedAt"`
	Accounts   []Account `json:"accounts"`
	// Scopes the token carried, as GitHub reported them. Empty when gh reported none, which the
	// caller must read as "cannot say" rather than "belongs to nothing else".
	Scopes []string `json:"scopes"`
}

// Problem is a failure the operator can fix, carrying the command that fixes it.
//
// A setup failure is not the same as belonging to nothing, and the interface has to be able to
// tell them apart: one is a measurement, the other is a broken tool.
type Problem struct {
	Reason string `json:"reason"`
	Remedy string `json:"remedy"`
}

func (p *Problem) Error() string { return p.Reason + " " + p.Remedy }

// Exec runs a command, folding its stderr into the error so a failure says what gh said.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("running %s: %w", name, ErrNotInstalled)
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return stdout.Bytes(), nil
}

// splitResponse separates the headers gh -i prints from the body that follows the blank line.
//
// A body with no header block is returned whole, so a plain `gh api` response still parses.
func splitResponse(out []byte) (headers, body []byte) {
	for _, sep := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
		if i := bytes.Index(out, sep); i >= 0 {
			return out[:i], out[i+len(sep):]
		}
	}
	return nil, out
}

// scopesFrom reads X-Oauth-Scopes, which GitHub sends as a comma-separated list.
func scopesFrom(headers []byte) []string {
	for _, line := range strings.Split(string(headers), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "X-Oauth-Scopes") {
			continue
		}
		var scopes []string
		for _, s := range strings.Split(value, ",") {
			if s = strings.TrimSpace(s); s != "" {
				scopes = append(scopes, s)
			}
		}
		return scopes
	}
	return nil
}

// Verify asks gh who it is authenticated as and which organizations that account belongs to.
func Verify(ctx context.Context, run Runner) (Identity, error) {
	var id Identity

	// -i so the response headers come too. X-Oauth-Scopes is the only place that says whether
	// this token could have seen the organization list it is about to read, and an incomplete
	// list read as a complete one turns "cannot say" into "you do not belong to it".
	out, err := run(ctx, "gh", "api", "-i", "user")
	if err != nil {
		return id, problemFor(err)
	}
	headers, body := splitResponse(out)
	id.Scopes = scopesFrom(headers)
	var user struct {
		Login   string `json:"login"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &user); err != nil {
		return id, fmt.Errorf("reading the authenticated account: %w", err)
	}
	if user.Login == "" {
		return id, &Problem{
			Reason: "gh answered without naming an account.",
			Remedy: "Check the authentication with: gh auth status",
		}
	}
	id.Login = user.Login
	id.Host = hostOf(user.HTMLURL)
	id.VerifiedAt = time.Now().UTC()
	id.Accounts = []Account{{Owner: user.Login, Kind: "user", Role: "owner"}}

	out, err = run(ctx, "gh", "api", "user/memberships/orgs", "--paginate")
	if err != nil {
		return id, problemFor(err)
	}
	orgs, err := memberships(out)
	if err != nil {
		return id, err
	}
	id.Accounts = append(id.Accounts, orgs...)
	return id, nil
}

// memberships decodes the membership pages, keeping only those actually joined.
//
// gh versions differ on whether --paginate emits one array or adjacent arrays.
func memberships(out []byte) ([]Account, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var accounts []Account
	for dec.More() {
		var page []struct {
			State        string `json:"state"`
			Role         string `json:"role"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("reading organization memberships: %w", err)
		}
		for _, m := range page {
			// An invitation that has not been accepted is not membership.
			if m.State != "active" || m.Organization.Login == "" {
				continue
			}
			accounts = append(accounts, Account{
				Owner: m.Organization.Login,
				Kind:  "organization",
				Role:  m.Role,
			})
		}
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Owner < accounts[j].Owner })
	return accounts, nil
}

func hostOf(htmlURL string) string {
	if u, err := url.Parse(htmlURL); err == nil && u.Host != "" {
		return u.Host
	}
	return "github.com"
}

// problemFor turns a gh failure into something the operator can act on.
//
// The classification reads gh's own message because gh reports the HTTP status there. Anything
// unrecognized is still surfaced verbatim rather than being flattened into "no accounts", which
// would report a broken setup as a measurement.
func problemFor(err error) error {
	if errors.Is(err, ErrNotInstalled) {
		return &Problem{
			Reason: "The gh CLI is not installed, so there is no way to confirm which accounts are yours.",
			Remedy: "Install it from https://cli.github.com, then run: gh auth login",
		}
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "gh auth login"),
		strings.Contains(text, "GH_TOKEN"),
		strings.Contains(text, "HTTP 401"):
		return &Problem{
			Reason: "gh is not authenticated, so there is no way to confirm which accounts are yours.",
			Remedy: "Run: gh auth login",
		}
	case strings.Contains(text, "HTTP 403"):
		return &Problem{
			Reason: "The gh token cannot read organization membership, so your organizations cannot be confirmed.",
			Remedy: "Grant the scope with: gh auth refresh -s read:org",
		}
	default:
		return &Problem{
			Reason: "gh could not be asked which accounts are yours: " + text,
			Remedy: "Check the authentication with: gh auth status",
		}
	}
}
