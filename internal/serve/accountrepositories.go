package serve

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/ghauth"
)

// AccountRepositories is what GitHub says an account holds, offered for an include list.
type AccountRepositories struct {
	Owner        string              `json:"owner"`
	Repositories []OfferedRepository `json:"repositories"`
	// Problem is a gh failure the reader can fix. The list is then empty because nothing was
	// read, not because the account holds nothing.
	Problem *ghauth.Problem `json:"problem"`
}

// OfferedRepository is one repository the picker lists, and whether the reader may include it.
type OfferedRepository struct {
	ghauth.Repository
	// Declarable is the server's answer, never re-derived in the browser: the account may be
	// declared whole, or the reader administers or maintains this repository.
	Declarable bool `json:"declarable"`
}

// offered marks what may be included: anything in an account that may be declared whole, and
// otherwise what the reader administers or maintains. Write, triage and read are not claims.
func offered(repositories []ghauth.Repository, whole bool) []OfferedRepository {
	out := make([]OfferedRepository, 0, len(repositories))
	for _, r := range repositories {
		out = append(out, OfferedRepository{Repository: r, Declarable: whole ||
			r.Permission == ghauth.PermissionAdmin || r.Permission == ghauth.PermissionMaintain})
	}
	return out
}

// accountRepositories lists one account's repositories for the declaration picker.
//
// A control rather than a read: it runs gh with the operator's token, spending their rate limit
// and naming private repositories that token can see, which no GET or other origin may cause.
// Nothing is written; what the reader picks is only stored by saving the declaration.
func (s *Server) accountRepositories(w http.ResponseWriter, r *http.Request) {
	if !s.allowControl(w, r) {
		return
	}
	owner := r.PathValue("owner")
	if !ghauth.IsAccountName(owner) {
		writeError(w, http.StatusBadRequest, owner+" is not a GitHub account name")
		return
	}
	list := s.ListRepositories
	if list == nil {
		list = func(ctx context.Context, owner string, own bool) ([]ghauth.Repository, error) {
			return ghauth.ListRepositories(ctx, ghauth.Exec, owner, own)
		}
	}

	// The operator's own account is decided by the recorded verification, never by the request.
	verified, _ := s.Store.Accounts()
	own := verified.Login != "" && strings.EqualFold(verified.Login, owner)
	whole := false
	for _, a := range verified.Accounts {
		if strings.EqualFold(a.Owner, owner) {
			whole = a.CanDeclare()
		}
	}

	out := AccountRepositories{Owner: owner, Repositories: []OfferedRepository{}}
	repositories, err := list(r.Context(), owner, own)
	var problem *ghauth.Problem
	switch {
	case errors.As(err, &problem):
		out.Problem = problem
	case err != nil:
		writeJSON(w, nil, err)
		return
	case repositories != nil:
		out.Repositories = offered(repositories, whole)
	}
	writeJSON(w, out, nil)
}
