package advisory

import (
	"context"
	"errors"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/ghapp"
)

type lentTokens struct {
	token string
	err   error
}

func (l lentTokens) Token(context.Context, string) (string, bool, error) { return l.token, true, l.err }

func (l lentTokens) AccountToken(context.Context, string) (string, error) {
	return "", ghapp.ErrNotInstalled
}

// Advisories belong to no account, so the designated installation reads them; with none, nothing is read anonymously.
func TestAdvisoriesAreReadWithTheDesignatedInstallation(t *testing.T) {
	rec := &recorder{pages: map[string]string{"": "[]"}}
	c := newServer(t, rec)
	c.Token = "person-token"
	c.Tokens = lentTokens{token: "lent-designated"}
	if _, err := c.Fetch(context.Background(), []string{"actions/checkout"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := rec.asked()[0].Header.Get("Authorization"); got != "Bearer lent-designated" {
		t.Errorf("Authorization = %q, want the designated installation's token", got)
	}

	c.Tokens = lentTokens{err: ghapp.ErrNoInstallation}
	if _, err := c.Fetch(context.Background(), []string{"actions/checkout"}); !errors.Is(err, ghapp.ErrNoInstallation) {
		t.Errorf("with nothing to read with, Fetch = %v; want ErrNoInstallation", err)
	}
	if len(rec.asked()) != 1 {
		t.Errorf("%d requests made; one with no credential must not be sent at all", len(rec.asked()))
	}
}
