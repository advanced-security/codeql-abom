package ghapp

import (
	"net/url"
	"strings"
)

// Subject is the account a REST URL concerns, and whether it reads the account itself rather than a repository.
func Subject(rawURL string) (owner string, account bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(u.Path, "/api/v3"), "/"), "/")
	if len(parts) < 2 || parts[1] == "" {
		return "", false
	}
	switch parts[0] {
	case "repos":
		return parts[1], false
	case "orgs", "users":
		return parts[1], true
	}
	return "", false
}
