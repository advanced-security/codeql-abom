package ghauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Whether a member holds an organization's security manager role, which memberships cannot say.
const (
	SecurityManagerYes     = "yes"
	SecurityManagerNo      = "no"
	SecurityManagerUnknown = "unknown"
)

// securityManagerReads bounds the gh processes one verification runs at once.
const securityManagerReads = 6

// readSecurityManagers asks, for each organization login is only a member of, whether they hold
// its security manager role: through the organization roles that replace the security managers
// endpoint, and through the teams that endpoint names where the roles cannot be read. A read that
// fails is "cannot say", never "no". Both need admin:org of a classic token, so one whose reported
// scopes lack it is not spent asking; a token reporting none cannot be judged that way and is asked.
func readSecurityManagers(ctx context.Context, run Runner, login string, scopes []string, accounts []Account) {
	var members []int
	for i, a := range accounts {
		if a.Kind == KindOrganization && a.Role == "member" {
			members = append(members, i)
		}
	}
	if len(members) == 0 {
		return
	}
	if len(scopes) > 0 && !slices.Contains(scopes, "admin:org") {
		for _, i := range members {
			accounts[i].SecurityManager = SecurityManagerUnknown
			accounts[i].SecurityManagerReason = "the gh token lacks the admin:org scope, which reading who manages security in " +
				accounts[i].Owner + " needs (gh auth refresh -s admin:org)"
		}
		return
	}
	teams, teamsErr := userTeams(ctx, run)
	slots := make(chan struct{}, securityManagerReads)
	var wg sync.WaitGroup
	for _, i := range members {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			a := &accounts[i]
			a.SecurityManager, a.SecurityManagerReason = securityManager(ctx, run, login, a.Owner, teams, teamsErr)
		})
	}
	wg.Wait()
}

func securityManager(ctx context.Context, run Runner, login, org string,
	teams map[string]map[string]bool, teamsErr error) (string, string) {
	if !IsAccountName(org) {
		return SecurityManagerUnknown, fmt.Sprintf("%q is not a name GitHub issues, so it was not asked about", org)
	}
	state, rolesErr := fromOrganizationRoles(ctx, run, login, org)
	if rolesErr == nil {
		return state, ""
	}
	out, err := run(ctx, "gh", "api", "orgs/"+org+"/security-managers")
	if err != nil {
		return SecurityManagerUnknown, cannotRead(org, err)
	}
	var managers []struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(out, &managers); err != nil {
		return SecurityManagerUnknown, "gh answered something other than " + org + "'s security manager teams"
	}
	if teamsErr != nil {
		return SecurityManagerUnknown, "your teams could not be read, so " + org +
			"'s security manager teams cannot be matched: " + cannotRead(org, teamsErr)
	}
	for _, m := range managers {
		if teams[strings.ToLower(org)][strings.ToLower(m.Slug)] {
			return SecurityManagerYes, ""
		}
	}
	return SecurityManagerUnknown, "no security manager team of " + org + " includes you, and a role assigned " +
		"to you directly could not be read: " + cannotRead(org, rolesErr)
}

// fromOrganizationRoles reads the security_manager role's assignees, teams' members included.
func fromOrganizationRoles(ctx context.Context, run Runner, login, org string) (string, error) {
	out, err := run(ctx, "gh", "api", "orgs/"+org+"/organization-roles")
	if err != nil {
		return "", err
	}
	var listed struct {
		Roles []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return "", fmt.Errorf("reading %s's organization roles: %w", org, err)
	}
	for _, r := range listed.Roles {
		if r.Name != "security_manager" {
			continue
		}
		out, err := run(ctx, "gh", "api", fmt.Sprintf("orgs/%s/organization-roles/%d/users?per_page=100", org, r.ID), "--paginate")
		if err != nil {
			return "", err
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		for dec.More() {
			var page []struct {
				Login string `json:"login"`
			}
			if err := dec.Decode(&page); err != nil {
				return "", fmt.Errorf("reading %s's security managers: %w", org, err)
			}
			for _, u := range page {
				if strings.EqualFold(u.Login, login) {
					return SecurityManagerYes, nil
				}
			}
		}
		return SecurityManagerNo, nil
	}
	return SecurityManagerNo, nil
}

// userTeams is every team the user belongs to, by lowercased organization and slug.
func userTeams(ctx context.Context, run Runner) (map[string]map[string]bool, error) {
	out, err := run(ctx, "gh", "api", "user/teams?per_page=100", "--paginate")
	if err != nil {
		return nil, err
	}
	teams := map[string]map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var page []struct {
			Slug         string `json:"slug"`
			Organization struct {
				Login string `json:"login"`
			} `json:"organization"`
		}
		if err := dec.Decode(&page); err != nil {
			return nil, fmt.Errorf("reading your teams: %w", err)
		}
		for _, t := range page {
			org := strings.ToLower(t.Organization.Login)
			if teams[org] == nil {
				teams[org] = map[string]bool{}
			}
			teams[org][strings.ToLower(t.Slug)] = true
		}
	}
	return teams, nil
}

// cannotRead says why gh could not answer, naming the scope where gh named one.
func cannotRead(org string, err error) string {
	if strings.Contains(err.Error(), "admin:org") {
		return "gh cannot read who manages security in " + org + " without the admin:org scope " +
			"(gh auth refresh -s admin:org)"
	}
	if strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "HTTP 403") {
		return "gh is not allowed to read who manages security in " + org
	}
	return "gh could not read who manages security in " + org
}
