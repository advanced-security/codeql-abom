package ghauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Repository is one repository an account holds, as the operator's token sees it.
type Repository struct {
	Name     string `json:"name"`
	Private  bool   `json:"private"`
	Archived bool   `json:"archived"`
	Fork     bool   `json:"fork"`
}

// accountName is what GitHub issues as a login. Checked before the name goes into a `gh api` path,
// so a request can name an account and nothing else.
var accountName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)

// IsAccountName reports whether a name is one GitHub could have issued.
func IsAccountName(name string) bool { return accountName.MatchString(name) }

// ListRepositories reads every repository an account holds, through the operator's own token.
//
// own is set for the operator's user account, whose private repositories only `user/repos` names.
// A listing that fails on any page returns nothing: a prefix offered as the whole account would
// let a reader conclude a repository does not exist.
func ListRepositories(ctx context.Context, run Runner, owner string, own bool) ([]Repository, error) {
	if !IsAccountName(owner) {
		return nil, fmt.Errorf("%q is not a GitHub account name", owner)
	}
	path := "orgs/" + owner + "/repos?type=all&per_page=100"
	if own {
		path = "user/repos?affiliation=owner&per_page=100"
	}
	out, err := run(ctx, "gh", "api", path, "--paginate")
	if err != nil {
		return nil, listingProblem(owner, err)
	}
	return repositoriesOf(owner, out)
}

func repositoriesOf(owner string, out []byte) ([]Repository, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	repositories := []Repository{}
	for dec.More() {
		var page []struct {
			Repository
			Owner struct {
				Login string `json:"login"`
			} `json:"owner"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("reading %s's repositories: %w", owner, err)
		}
		for _, r := range page {
			if r.Name != "" && strings.EqualFold(r.Owner.Login, owner) {
				repositories = append(repositories, r.Repository)
			}
		}
	}
	sort.Slice(repositories, func(i, j int) bool {
		return strings.ToLower(repositories[i].Name) < strings.ToLower(repositories[j].Name)
	})
	return repositories, nil
}

// listingProblem says why an account's repositories could not be read, and what would fix it.
func listingProblem(owner string, err error) error {
	if errors.Is(err, ErrNotInstalled) {
		return problemFor(err)
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "SAML enforcement"):
		return &Problem{
			Reason: owner + " requires single sign-on, and the gh token is not authorized for it.",
			Remedy: "Open https://github.com/orgs/" + owner + "/sso to start a session, then run " +
				"gh auth refresh and authorize " + owner + " when GitHub asks.",
		}
	case strings.Contains(text, "HTTP 404"):
		return &Problem{
			Reason: "gh cannot see an organization named " + owner + ".",
			Remedy: "Check the name, or declare it by name if the token simply cannot see it.",
		}
	case strings.Contains(text, "gh auth login"),
		strings.Contains(text, "GH_TOKEN"),
		strings.Contains(text, "HTTP 401"):
		return problemFor(err)
	default:
		return &Problem{
			Reason: "gh could not list " + owner + "'s repositories: " + text,
			Remedy: "Check the authentication with: gh auth status",
		}
	}
}
