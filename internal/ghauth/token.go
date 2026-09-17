package ghauth

import (
	"context"
	"errors"
	"strings"
)

// Token returns the credential the gh CLI is logged in with.
//
// Identity is verified by asking gh, so gh's credentials are the ones the operator has already
// granted. Reading the API with different credentials — or, when no environment variable is set,
// with none — makes the corpus disagree with the identity banner above it: enumeration reported
// 91 of an organization's 181 repositories on 2026-09-08, with no failures, because it ran
// anonymously and 90 of them are private.
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
