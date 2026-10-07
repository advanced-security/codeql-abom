package ghapp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func pkt(s string) string { return fmt.Sprintf("%04x%s", len(s)+4, s) }

// Git authenticates as x-access-token with no token in argv or a URL, and none reaches the operator's credential store.
func TestGitAuthenticatesWithoutATokenInArgvOrURL(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	const token = "ghs_installationTOKEN0123456789abcdef"
	const sha = "0123456789abcdef0123456789abcdef01234567"
	seen := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="GitHub"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		seen <- user + ":" + pass
		if user != "x-access-token" || pass != token {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path != "/has-ghas/widgets.git/info/refs" || r.URL.Query().Get("service") != "git-upload-pack" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
		_, _ = io.WriteString(w, pkt("# service=git-upload-pack\n")+"0000"+
			pkt(sha+" HEAD\x00symref=HEAD:refs/heads/main\n")+pkt(sha+" refs/heads/main\n")+"0000")
	}))
	t.Cleanup(srv.Close)

	home := t.TempDir()
	stored := filepath.Join(home, "stored-credentials")
	global := filepath.Join(home, "gitconfig")
	config := fmt.Sprintf("[credential]\n\thelper = store --file %s\n[credential %q]\n\thelper = store --file %s\n",
		stored, srv.URL, stored)
	if err := os.WriteFile(global, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	auth := GitCredentials(srv.URL, token)
	if len(auth.Args) == 0 || len(auth.Env) == 0 {
		t.Fatalf("GitCredentials = %+v, want arguments and environment", auth)
	}
	remote := srv.URL + "/has-ghas/widgets.git"
	args := append(append([]string{}, auth.Args...), "ls-remote", remote)
	for _, a := range args {
		if strings.Contains(a, token) {
			t.Errorf("the token is in argv: %q", a)
		}
	}
	if strings.Contains(remote, token) || strings.Contains(remote, "@") {
		t.Errorf("the remote URL carries a credential: %s", remote)
	}

	cmd := exec.Command(gitPath, args...)
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+global,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")
	cmd.Env = append(cmd.Env, auth.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("git ls-remote: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), sha+"\trefs/heads/main") {
		t.Errorf("ls-remote answered %q", stdout.String())
	}
	if got := <-seen; got != "x-access-token:"+token {
		t.Errorf("git authenticated as %q, want x-access-token and the installation token", got)
	}
	if strings.Contains(stderr.String(), token) || strings.Contains(stdout.String(), token) {
		t.Error("git printed the token")
	}
	if data, err := os.ReadFile(stored); err == nil && strings.Contains(string(data), token) ||
		strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte(token))) {
		t.Errorf("the operator's credential store kept the installation token: %s", data)
	}

	// An owner the App is not installed on is read anonymously, never with whatever the operator's helpers hold.
	u, _ := url.Parse(srv.URL)
	if err := os.WriteFile(stored, []byte("http://operator:person-token@"+u.Host+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	none := GitCredentials(srv.URL, "")
	if len(none.Env) != 0 || slices.ContainsFunc(none.Args, func(a string) bool { return strings.Contains(a, "!f()") }) {
		t.Errorf("no token still configured a helper: %+v", none)
	}
	anonymous := exec.Command(gitPath, append(append([]string{}, none.Args...), "ls-remote", remote)...)
	anonymous.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_GLOBAL="+global,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_ASKPASS=")
	if out, err := anonymous.CombinedOutput(); err == nil {
		t.Errorf("an uninstalled owner's private repository was read: %s", out)
	}
	select {
	case got := <-seen:
		t.Errorf("git presented %q for an owner the App is not installed on", got)
	default:
	}
}
