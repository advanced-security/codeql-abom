package failure

import "testing"

// Git's exit status is 128 for a resolver outage and for a deleted repository alike, so the text is
// the only evidence of which one happened. These are Git's own words, from git 2.4x over HTTPS and
// a local path, as a clone, a fetch of a commit and an ls-remote print them.
func TestEachFailureIsClassedByWhatTheRemoteSaid(t *testing.T) {
	for text, want := range map[string]Class{
		"fatal: unable to access 'https://github.com/octo/app.git/': Could not resolve host: github.com":           Transient,
		"fatal: unable to access 'https://github.com/octo/app.git/': Failed to connect to github.com port 443":     Transient,
		"fatal: unable to access 'https://github.com/octo/app.git/': Connection timed out after 300000 ms":         Transient,
		"fatal: unable to access 'https://github.com/octo/app.git/': OpenSSL SSL_read: SSL_ERROR_SYSCALL, errno 0": Transient,
		"fatal: unable to access 'https://github.com/octo/app.git/': The requested URL returned error: 502":        Transient,
		"fatal: unable to access 'https://github.com/octo/app.git/': The requested URL returned error: 429":        Transient,
		"error: RPC failed; curl 92 HTTP/2 stream 5 was not closed cleanly\nfatal: early EOF":                      Transient,
		"fatal: the remote end hung up unexpectedly":                                                               Transient,
		"signal: killed":                       Transient,
		"context deadline exceeded":            Transient,
		"something no rule here has ever seen": Transient,
		"":                                     Transient,

		"fatal: could not read Username for 'https://github.com': terminal prompts disabled":                         Access,
		"remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/o/r/'":          Access,
		"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 403":               Access,
		"fatal: unable to access 'https://github.com/o/r.git/': The requested URL returned error: 401":               Access,
		"remote: Repository not found.\nfatal: repository 'https://github.com/o/r.git/' not found":                   Access,
		"fatal: repository '/tmp/x/does-not-exist' does not exist":                                                   Access,
		"fatal: '/tmp/x/gone' does not appear to be a git repository\nfatal: Could not read from remote repository.": Access,
		"the GitHub App is not installed on this account: actions, so a private repository there cannot be read":     Access,

		"Cloning into '.'...\nwarning: Could not find remote branch 0.0.3 to clone.\nfatal: Remote branch 0.0.3 not found in upstream origin": Absent,
		"fatal: remote error: upload-pack: not our ref 0123456789abcdef0123456789abcdef01234567":                                              Absent,
		"fatal: couldn't find remote ref refs/tags/v9":                                                                                        Absent,
		"reference missing: octo/app has no v9":                                                                                               Absent,
	} {
		if got := Classify(text); got != want {
			t.Errorf("Classify(%q) = %q, want %q", text, got, want)
		}
	}
}

// Only an answer a later read would repeat ends coverage; a retry costs a clone, and a wrong
// terminal state costs coverage nobody notices losing.
func TestOnlyAnAnswerThatWouldRepeatIsTerminal(t *testing.T) {
	for class, want := range map[Class]bool{Transient: false, Access: true, Absent: true} {
		if got := class.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", class, got, want)
		}
	}
}
