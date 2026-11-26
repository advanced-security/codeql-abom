package ghapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MembersPermissions reads which organization and team a person belongs to, for signing them in.
var MembersPermissions = PermissionSet{"members": "read"}

// ErrNoClientSecret reports an App that cannot sign anyone in: GitHub exchanges a sign-in only for
// the App's own client secret.
var ErrNoClientSecret = errors.New("the App was given no client secret; GitHub exchanges a sign-in only " +
	"for one, generated in the App's settings on GitHub")

// SignInUser is a person GitHub vouched for through the App's user authorization.
type SignInUser struct {
	ID    int64
	Login string
	Name  string
}

// Web is where people sign in: the host the API serves, which on GitHub.com drops its api. prefix.
func (a *App) Web() string {
	scheme := "https"
	if u, err := url.Parse(a.base()); err == nil && u.Scheme != "" {
		scheme = u.Scheme
	}
	return scheme + "://" + a.Host()
}

// AuthorizeURL is GitHub's page asking a person to authorize the App, for one sign-in attempt.
func (a *App) AuthorizeURL(state, challenge, redirect string) string {
	q := url.Values{"client_id": {a.Credentials.ClientID}, "redirect_uri": {redirect}, "state": {state},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return a.Web() + "/login/oauth/authorize?" + q.Encode()
}

// SignIn exchanges an authorization code for a user token, reads whose it is, and revokes it: ABOM
// keeps no token that acts as a person, and the App's installation reads everything else.
func (a *App) SignIn(ctx context.Context, code, verifier, redirect string) (SignInUser, error) {
	secret := a.Credentials.ClientSecret
	if secret == "" {
		return SignInUser{}, ErrNoClientSecret
	}
	token, err := a.userToken(ctx, code, verifier, redirect)
	if err != nil {
		return SignInUser{}, err
	}
	defer a.revoke(token)
	var who struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := a.call(ctx, http.MethodGet, "/user", token, &who, secret); err != nil {
		return SignInUser{}, fmt.Errorf("reading who signed in: %w", err)
	}
	if who.ID == 0 || who.Login == "" {
		return SignInUser{}, errors.New("GitHub did not say who signed in")
	}
	return SignInUser{ID: who.ID, Login: who.Login, Name: who.Name}, nil
}

func (a *App) userToken(ctx context.Context, code, verifier, redirect string) (string, error) {
	secret := a.Credentials.ClientSecret
	form := url.Values{"client_id": {a.Credentials.ClientID}, "client_secret": {secret}, "code": {code},
		"code_verifier": {verifier}, "redirect_uri": {redirect}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.Web()+"/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building the sign-in exchange: %w", withoutURL(err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	body, err := a.do(req)
	if err != nil {
		return "", fmt.Errorf("exchanging the sign-in code: %s", scrub(err.Error(), secret, code, verifier))
	}
	var answer struct {
		Token       string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return "", fmt.Errorf("decoding GitHub's sign-in answer: %w", err)
	}
	if answer.Token == "" {
		return "", fmt.Errorf("GitHub refused the sign-in: %s", scrub(strings.TrimSpace(answer.Error+" "+answer.Description),
			secret, code, verifier))
	}
	return answer.Token, nil
}

// revoke ends a user token as soon as it has said whose it is. Best effort: it expires anyway, and
// nothing ABOM holds can use it.
func (a *App) revoke(token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"access_token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		a.base()+"/applications/"+url.PathEscape(a.Credentials.ClientID)+"/token", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.SetBasicAuth(a.Credentials.ClientID, a.Credentials.ClientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	_, _ = a.do(req)
}

// do sends a request that is not an installation's, returning the body or GitHub's refusal.
func (a *App) do(req *http.Request) ([]byte, error) {
	client := a.HTTP
	if client == nil {
		client = defaultHTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, withoutURL(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &statusError{Status: resp.StatusCode, Message: messageOf(body)}
	}
	return body, nil
}

// Role is login's role in org, "admin" for an owner and "member" otherwise, or in org's team when
// team is named, "maintainer" or "member"; empty when they are not an active member. It is read with
// the installation's token carrying members: read and nothing else.
func (a *App) Role(ctx context.Context, org, team, login string) (string, error) {
	token, _, err := a.TokenWith(ctx, org, MembersPermissions, false)
	if err != nil {
		return "", err
	}
	path := "/orgs/" + url.PathEscape(org) + "/memberships/" + url.PathEscape(login)
	if team != "" {
		path = "/orgs/" + url.PathEscape(org) + "/teams/" + url.PathEscape(team) + "/memberships/" + url.PathEscape(login)
	}
	var membership struct {
		State string `json:"state"`
		Role  string `json:"role"`
	}
	err = a.call(ctx, http.MethodGet, path, token, &membership)
	var status *statusError
	switch {
	case errors.As(err, &status) && status.Status == http.StatusNotFound:
		return "", nil
	case err != nil:
		return "", err
	case membership.State != "active":
		return "", nil
	}
	return membership.Role, nil
}
