package store

import (
	"net/url"
	"strings"
)

// IsPostgresTarget recognizes network targets before a caller treats them as filesystem paths.
func IsPostgresTarget(target string) bool {
	return strings.HasPrefix(target, "postgres://") || strings.HasPrefix(target, "postgresql://")
}

// DatabaseLabel omits URL credentials and connection parameters from logs and errors.
func DatabaseLabel(target string) string {
	if !IsPostgresTarget(target) {
		return target
	}
	u, err := url.Parse(target)
	if err != nil {
		return "PostgreSQL database"
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u.String()
}
