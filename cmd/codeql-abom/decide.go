package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/authz"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// commandDecision is how one command is decided: as an operation, recorded before it changes
// anything, or not by itself, for the reason given.
type commandDecision struct {
	op     string
	exempt string
}

// commandDecisions classifies every command. TestEveryCommandIsDecidedOrSaysWhyNot fails a command
// added without a row, and TestEveryDecidingCommandAsksForItsDecision one that skips its call.
var commandDecisions = map[string]commandDecision{
	"discover":    {op: authz.OpDiscoveryControl},
	"build":       {op: authz.OpDiscoveryControl},
	"expand-all":  {op: authz.OpDiscoveryControl},
	"refresh":     {op: authz.OpDiscoveryControl},
	"closure":     {op: authz.OpDiscoveryControl},
	"branches":    {op: authz.OpDiscoveryControl},
	"accounts":    {op: authz.OpVerifyIdentity},
	"own":         {op: authz.OpDeclareScope},
	"prune":       {op: authz.OpPrune},
	"verify-lock": {op: authz.OpLockVerify},
	"audit":       {op: authz.OpConfigure},

	"report":        {exempt: "reads only"},
	"bom":           {exempt: "reads only"},
	"stale":         {exempt: "reads only"},
	"version":       {exempt: "reads only"},
	"serve":         {exempt: "decides each control it serves"},
	"code-scanning": {exempt: "asks the running server, which decides each change"},
	"worker":        {exempt: "acts only under a grant the coordinator signed"},
	"attestor":      {exempt: "signs only what its worker leaves under a grant the coordinator signed"},
	"check":         {exempt: "compares on copies of the database and writes only its own report"},
	"demo":          {exempt: "a Demo database is practice data no declaration or analysis depends on"},
	"selfcheck":     {exempt: "analyzes a source tree and writes no database"},
	"serialize-mcp": {exempt: "relays tool calls for a session the server decided"},
	"egress-proxy":  {exempt: "forwards tunnels to the hosts it is told and writes nothing"},
	"app": {exempt: "manages the read-only App's own credentials through GitHub, which records them; " +
		"ABOM decides nothing with them"},
}

type commandEvidence struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// openDecided opens and migrates the database at dbPath for the command name, and records the
// decision to run it with args before anything acts. Migrating first also builds any index the
// schema has gained since this database was made.
func openDecided(ctx context.Context, name string, args []string, dbPath string, opts store.Options) (store.Backend, error) {
	s, err := store.OpenBackend(dbPath, opts)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", store.DatabaseLabel(dbPath), err)
	}
	if err := s.Migrate(); err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("migrating %s: %w", store.DatabaseLabel(dbPath), err)
	}
	if _, err := decideCommand(ctx, s, name, args); err != nil {
		_ = s.Close()
		return nil, err
	}
	return s, nil
}

// decideCommand signs and logs the decision to act on a command before it changes anything, and
// returns it for a decision the command then makes to cite. A decision that cannot be recorded is a
// command not run.
func decideCommand(ctx context.Context, s store.Backend, name string, args []string) (authz.Decision, error) {
	d, ok := commandDecisions[name]
	if !ok || d.op == "" {
		return authz.Decision{}, fmt.Errorf("%s: not a command that is decided", name)
	}
	if args == nil {
		args = []string{}
	}
	decision, err := s.Decide(ctx, authz.Request{Operation: d.op,
		Principal: commandLine(),
		Subject:   authz.CommandSubject(name), Evidence: commandEvidence{Command: name, Args: redactArgs(args)}, Allow: true})
	if err != nil {
		return authz.Decision{}, fmt.Errorf("%s: the decision to act could not be recorded, so nothing was done: %w", name, err)
	}
	return decision, nil
}

// serviceAccountToken is where Kubernetes projects the pod's ServiceAccount token.
var serviceAccountToken = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// commandLine is who runs a command: the command line, and in a pod the ServiceAccount it runs as,
// read from the token's subject. The token is not verified; it names, it does not authorize.
func commandLine() authz.Principal {
	body, err := os.ReadFile(serviceAccountToken)
	if err != nil {
		return authz.Local("command line")
	}
	parts := strings.Split(strings.TrimSpace(string(body)), ".")
	if len(parts) != 3 {
		return authz.Local("command line")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Subject string `json:"sub"`
	}
	if err != nil || json.Unmarshal(payload, &claims) != nil || claims.Subject == "" {
		return authz.Local("command line")
	}
	return authz.Local("command line as " + claims.Subject)
}

// redacted stands in for anything that could sign in somewhere by itself. The log cannot be edited
// and every viewer reads it, so a credential reaching it would be published for good.
const redacted = "REDACTED"

var (
	secretFlag = regexp.MustCompile(`(?i)secret|token|passw(or)?d|private-key`)
	secretTerm = regexp.MustCompile(`(?i)\b((?:ssl)?passw(?:or)?d)=[^\s&]*`)
)

// redactArgs keeps what a command was given readable, and drops the value of any flag named as a
// secret and the password from any connection URL or string.
func redactArgs(args []string) []string {
	out := make([]string, 0, len(args))
	hideNext := false
	for _, a := range args {
		flag := strings.HasPrefix(a, "-")
		switch name, value, assigned := strings.Cut(a, "="); {
		case hideNext && !flag:
			out = append(out, redacted)
		case flag && assigned && secretFlag.MatchString(name):
			out = append(out, name+"="+redacted)
		case flag && assigned:
			out = append(out, name+"="+scrub(value))
		default:
			out = append(out, scrub(a))
		}
		hideNext = flag && !strings.Contains(a, "=") && secretFlag.MatchString(a)
	}
	return out
}

func scrub(v string) string {
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil && u.User != nil {
			if _, has := u.User.Password(); has {
				u.User = url.UserPassword(u.User.Username(), redacted)
				v = u.String()
			}
		}
	}
	return secretTerm.ReplaceAllString(v, "${1}="+redacted)
}
