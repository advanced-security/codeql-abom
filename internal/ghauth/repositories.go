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
	// Permission is the reader's highest role on it — admin, maintain, write, triage or read — and
	// empty where GitHub reported none.
	Permission string `json:"permission"`
}

// Repository roles, highest first. GitHub reports write as push and read as pull.
const (
	PermissionAdmin    = "admin"
	PermissionMaintain = "maintain"
	PermissionWrite    = "write"
	PermissionTriage   = "triage"
	PermissionRead     = "read"
)

// permissions is GitHub's per-role answer on a listed repository.
type permissions struct {
	Admin    bool `json:"admin"`
	Maintain bool `json:"maintain"`
	Push     bool `json:"push"`
	Triage   bool `json:"triage"`
	Pull     bool `json:"pull"`
}

func (p *permissions) highest() string {
	switch {
	case p == nil:
		return ""
	case p.Admin:
		return PermissionAdmin
	case p.Maintain:
		return PermissionMaintain
	case p.Push:
		return PermissionWrite
	case p.Triage:
		return PermissionTriage
	case p.Pull:
		return PermissionRead
	}
	return ""
}

// advisoryFork is the name GitHub gives a temporary private fork made to fix a security advisory.
var advisoryFork = regexp.MustCompile(`-ghsa(-[23456789cfghjmpqrvwx]{4}){3}$`)

// IsAdvisoryFork reports whether a repository is a temporary private fork made for an advisory,
// which nobody declares: it exists to be merged and deleted.
func IsAdvisoryFork(name string) bool { return advisoryFork.MatchString(strings.ToLower(name)) }

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
			Permissions *permissions `json:"permissions"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("reading %s's repositories: %w", owner, err)
		}
		for _, r := range page {
			if r.Name != "" && strings.EqualFold(r.Owner.Login, owner) && !IsAdvisoryFork(r.Name) {
				r.Permission = r.Permissions.highest()
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
	if p := new(Problem); errors.As(err, &p) {
		return p
	}
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

// repositoryName is what GitHub accepts as a repository name, checked before it reaches a path.
var repositoryName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

// ReadRepositoryRole reads login's role on owner/repo, as what it permits — admin, maintain, write,
// triage or read — so a custom role is judged by the permissions it carries, as the picker judges it.
// A token that cannot see the repository, or a custom role reported without them, is an error, never a role.
func ReadRepositoryRole(ctx context.Context, run Runner, owner, repo, login string) (string, error) {
	if !IsAccountName(owner) || !IsAccountName(login) || !repositoryName.MatchString(repo) || repo == "." || repo == ".." {
		return "", fmt.Errorf("%q, %q and %q are not names GitHub issues", owner, repo, login)
	}
	out, err := run(ctx, "gh", "api", "repos/"+owner+"/"+repo+"/collaborators/"+login+"/permission")
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 403") {
			return "", fmt.Errorf("gh cannot read %s's role on %s/%s", login, owner, repo)
		}
		return "", fmt.Errorf("gh could not read %s's role on %s/%s: %w", login, owner, repo, err)
	}
	var answer struct {
		RoleName string `json:"role_name"`
		User     struct {
			Permissions *permissions `json:"permissions"`
		} `json:"user"`
	}
	if err := json.Unmarshal(out, &answer); err != nil {
		return "", fmt.Errorf("reading %s's role on %s/%s: %w", login, owner, repo, err)
	}
	if role := answer.User.Permissions.highest(); role != "" {
		return role, nil
	}
	switch answer.RoleName {
	case PermissionAdmin, PermissionMaintain, PermissionWrite, PermissionTriage, PermissionRead:
		return answer.RoleName, nil
	}
	return "", fmt.Errorf("GitHub names %s's role on %s/%s %q and reports nothing it permits", login, owner, repo, answer.RoleName)
}
