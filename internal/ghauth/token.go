package ghauth

import (
	"context"
	"errors"
	"strings"
)

// Token returns the credential the gh CLI is logged in with.
//
// Identity is verified by asking gh, so gh's credentials are the ones the operator has already
// granted. Reading the API with different credentials can make the corpus disagree with the
// identity banner above it.
func Token(ctx context.Context, run Runner) (string, error) {
	out, err := run(ctx, "gh", "auth", "token")
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		// A blank token with a zero exit status would otherwise be spent as a credential and
		// answer with whatever is public.
		return "", errors.New("gh auth token returned nothing; run: gh auth login")
	}
	return token, nil
}
