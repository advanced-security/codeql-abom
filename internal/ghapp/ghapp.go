// Package ghapp is a read-only GitHub App identity: its key, its JWT, and a token per installation.
package ghapp

import (
	"context"
	"errors"
)

// TokenSource chooses the credential for a read by the account it concerns.
type TokenSource interface {
	// Token reads owner's repositories; borrowed means owner has no installation, so only public ones are readable.
	Token(ctx context.Context, owner string) (token string, borrowed bool, err error)
	// AccountToken reads owner as an account, which only its own installation sees whole.
	AccountToken(ctx context.Context, owner string) (string, error)
}

// ErrNotInstalled reports an owner the App cannot read; substituting another credential would hide that.
var ErrNotInstalled = errors.New("the GitHub App is not installed on this account")

// ErrNoInstallation reports an App installed nowhere, which has nothing to read with at all.
var ErrNoInstallation = errors.New("the GitHub App has no installation to read with")
