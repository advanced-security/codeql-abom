package ghapp

import "strings"

// GitAuth is what a git invocation adds: arguments before the subcommand, and environment.
type GitAuth struct {
	Args []string
	Env  []string
}

// gitTokenEnv carries the token: unlike argv, another account cannot read a process's environment.
const gitTokenEnv = "ABOM_GIT_TOKEN"

// GitCredentials authenticates git to host as x-access-token, or with no token, as nobody at all.
func GitCredentials(host, token string) GitAuth {
	// The empty helper clears the operator's helpers: they would store the token, or answer without one.
	reset := []string{"-c", "credential.helper="}
	if token == "" {
		return GitAuth{Args: reset}
	}
	helper := `!f() { test "$1" = get && echo username=x-access-token && echo "password=$` + gitTokenEnv + `"; }; f`
	return GitAuth{
		Args: append(reset, "-c", "credential."+strings.TrimSuffix(host, "/")+".helper="+helper),
		Env:  []string{gitTokenEnv + "=" + token},
	}
}
