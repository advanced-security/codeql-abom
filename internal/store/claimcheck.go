package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// repositoryRoleDDL keeps the verified login's role on each repository a member declared, read
// apart from the listing that offered it; a role that could not be read is kept as unread.
const repositoryRoleDDL = `
CREATE TABLE IF NOT EXISTS abom_repository_role (
  owner   TEXT NOT NULL COLLATE NOCASE,
  repo    TEXT NOT NULL COLLATE NOCASE,
  login   TEXT NOT NULL,
  role    TEXT,
  unread  TEXT NOT NULL DEFAULT '',
  read_at TEXT NOT NULL,
  PRIMARY KEY (owner, repo)
);`

// How a declaration's claim stands against the role recorded behind it.
const (
	ClaimSupported   = "supported"
	ClaimUnsupported = "unsupported"
	ClaimUnknown     = "unknown"
)

// RepositoryRole is one read of the verified login's role on a repository: GitHub's role name, or
// why it could not be read.
type RepositoryRole struct {
	Owner  string `json:"owner"`
	Repo   string `json:"repo"`
	Login  string `json:"login"`
	Role   string `json:"role"`
	Unread string `json:"unread"`
	ReadAt string `json:"readAt"`
}

// ClaimCheck is whether the recorded role still supports a declaration. It decides nothing: a claim
// it cannot support stays declared, and discovery is bounded by the declaration alone.
type ClaimCheck struct {
	State string `json:"state"`
	// Role is what the claim rests on, as recorded: owner, admin, security manager, installation, member.
	Role   string `json:"role"`
	Detail string `json:"detail"`
	// Includes answers each repository a member declared, where the account cannot be declared whole.
	Includes []IncludeCheck `json:"includes"`
}

// IncludeCheck is the role behind one included repository.
type IncludeCheck struct {
	Repository string `json:"repository"`
	State      string `json:"state"`
	Role       string `json:"role"`
	Detail     string `json:"detail"`
}

// RecordRepositoryRoles replaces the roles on record with one refresh's reads for login.
func (s *Store) RecordRepositoryRoles(login string, at time.Time, roles []RepositoryRole) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("recording repository roles: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM abom_repository_role`); err != nil {
		return fmt.Errorf("recording repository roles: %w", err)
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	for _, r := range roles {
		if _, err := tx.Exec(`INSERT INTO abom_repository_role (owner, repo, login, role, unread, read_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(owner, repo) DO UPDATE SET login = excluded.login, role = excluded.role,
			  unread = excluded.unread, read_at = excluded.read_at`,
			r.Owner, r.Repo, login, nullable(r.Role), r.Unread, stamp); err != nil {
			return fmt.Errorf("recording the role on %s/%s: %w", r.Owner, r.Repo, err)
		}
	}
	return tx.Commit()
}

// RepositoryRoles is every role on record, keyed by lowercased owner/repo.
func (s *Store) RepositoryRoles() (map[string]RepositoryRole, error) {
	rows, err := queryAll(s, "reading repository roles", func(r *sql.Rows) (RepositoryRole, error) {
		var out RepositoryRole
		var role sql.NullString
		err := r.Scan(&out.Owner, &out.Repo, &out.Login, &role, &out.Unread, &out.ReadAt)
		out.Role = role.String
		return out, err
	}, `SELECT owner, repo, login, role, unread, read_at FROM abom_repository_role`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]RepositoryRole, len(rows))
	for _, r := range rows {
		out[strings.ToLower(r.Owner+"/"+r.Repo)] = r
	}
	return out, nil
}

// CheckClaim says whether the verification and the roles read for it support a declaration.
func CheckClaim(a DeclaredAccount, v Verification, roles map[string]RepositoryRole) ClaimCheck {
	if v.Login == "" {
		return ClaimCheck{State: ClaimUnknown, Detail: "nothing has been verified, so no role is on record", Includes: []IncludeCheck{}}
	}
	var known *Account
	for i := range v.Accounts {
		if strings.EqualFold(v.Accounts[i].Owner, a.Owner) {
			known = &v.Accounts[i]
		}
	}
	if known == nil {
		if v.ByApp() {
			return ClaimCheck{State: ClaimUnknown, Includes: []IncludeCheck{},
				Detail: "the App is not installed on " + a.Owner + ", and an App reads no person's role there"}
		}
		if v.CanReadMemberships() {
			return ClaimCheck{State: ClaimUnsupported, Includes: []IncludeCheck{},
				Detail: v.Login + " does not belong to " + a.Owner + ", as a complete list of memberships says"}
		}
		return ClaimCheck{State: ClaimUnknown, Includes: []IncludeCheck{},
			Detail: "the token could not see every membership, so " + a.Owner + " cannot be checked"}
	}
	check := ClaimCheck{Role: roleName(*known), Includes: []IncludeCheck{}}
	switch {
	case known.Role == RoleInstallation:
		check.State, check.Detail = ClaimSupported, "the App is installed on "+a.Owner
		return check
	case known.CanDeclare():
		check.State, check.Detail = ClaimSupported, v.Login+" is "+article(check.Role)+" of "+a.Owner
		return check
	case !known.CanInclude():
		check.State, check.Detail = ClaimUnsupported, "a "+check.Role+" cannot change anything in "+a.Owner
		return check
	case !a.IncludesOnly || len(a.Properties) > 0:
		check.State = ClaimUnsupported
		check.Detail = "a member may declare only the repositories they administer or maintain, by name"
	default:
		check.State, check.Detail = ClaimSupported, "every included repository is one "+v.Login+" administers or maintains"
		for _, repo := range a.Includes {
			include := checkInclude(a.Owner, repo, v.Login, roles)
			check.Includes = append(check.Includes, include)
			switch {
			case include.State == ClaimUnsupported:
				check.State, check.Detail = ClaimUnsupported, "an included repository is not one "+v.Login+" administers or maintains"
			case include.State == ClaimUnknown && check.State == ClaimSupported:
				check.State, check.Detail = ClaimUnknown, "the role on an included repository could not be read"
			}
		}
	}
	// A security manager may declare anything here, so while that role is unread nothing refutes the claim.
	if check.State == ClaimUnsupported && known.SecurityManager != SecurityManagerYes && known.SecurityManager != "no" {
		why := known.SecurityManagerReason
		if why == "" {
			why = "it was not read when the accounts were last refreshed"
		}
		check.State = ClaimUnknown
		check.Detail = check.Detail + ", unless " + v.Login + " is the security manager of " + a.Owner +
			", which cannot be said: " + why
	}
	return check
}

func checkInclude(owner, repo, login string, roles map[string]RepositoryRole) IncludeCheck {
	out := IncludeCheck{Repository: repo}
	r, ok := roles[strings.ToLower(owner+"/"+repo)]
	switch {
	case !ok || !strings.EqualFold(r.Login, login):
		out.State, out.Detail = ClaimUnknown, "not read yet; refreshing the accounts reads it"
	case r.Role == "":
		out.State, out.Detail = ClaimUnknown, r.Unread
	case r.Role == "admin" || r.Role == "maintain":
		out.State, out.Role, out.Detail = ClaimSupported, r.Role, login+" holds "+r.Role
	default:
		out.State, out.Role = ClaimUnsupported, r.Role
		out.Detail = login + " holds " + r.Role + ", which is not admin or maintain"
	}
	return out
}

func roleName(a Account) string {
	if a.Role == RoleMember && a.SecurityManager == SecurityManagerYes {
		return "security manager"
	}
	return a.Role
}

func article(role string) string {
	if role == RoleOwner || role == RoleAdmin {
		return "an " + role
	}
	return "a " + role
}
