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
	Repositories []ghauth.Repository `json:"repositories"`
	// Problem is a gh failure the reader can fix. The list is then empty because nothing was
	// read, not because the account holds nothing.
	Problem *ghauth.Problem `json:"problem"`
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

	out := AccountRepositories{Owner: owner, Repositories: []ghauth.Repository{}}
	repositories, err := list(r.Context(), owner, own)
	var problem *ghauth.Problem
	switch {
	case errors.As(err, &problem):
		out.Problem = problem
	case err != nil:
		writeJSON(w, nil, err)
		return
	case repositories != nil:
		out.Repositories = repositories
	}
	writeJSON(w, out, nil)
}
