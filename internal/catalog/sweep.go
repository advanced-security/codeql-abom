package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Route names which API answered a read. Which one it was is recorded rather than inferred: the
// two must agree, and when they do not, a figure has to be attributable to the route that produced
// it instead of leaving a reader to reconcile two numbers.
const (
	RouteGraphQL = "graphql"
	RouteREST    = "rest"
)

// Ordering fields, spelled as GraphQL's enum and translated for REST.
const (
	orderPushed  = "PUSHED_AT"
	orderCreated = "CREATED_AT"
)

// reposPerGraphQLPage is GitHub's ceiling on `first`. Asking for more is an error rather than a
// larger page, and a query GitHub cannot answer in ten seconds is terminated and charged for.
const reposPerGraphQLPage = 100

// Marks are the high-water instants a sorted read stops below.
//
// They only ever move forward. A read that found nothing carries the zero time, and letting that
// win would drag the mark backwards and turn the next early stop into a full walk.
type Marks struct {
	HighCreated time.Time `json:"highCreated,omitzero"`
	HighPushed  time.Time `json:"highPushed,omitzero"`
}

// Empty reports that nothing has ever been established about this account. Never enumerated is not
// enumerated and empty: with no marks there is nothing to stop at, so the read is a full listing.
func (m Marks) Empty() bool { return m.HighCreated.IsZero() && m.HighPushed.IsZero() }

// bound is where a walk over one ordering field stops.
//
// A dimension with no mark of its own borrows the other's. Both are wall-clock instants meaning
// "the account was read to about here", and an unbounded walk beside a bounded one would spend a
// full listing to answer half a question.
func (m Marks) bound(field string) time.Time {
	own, other := m.HighPushed, m.HighCreated
	if field == orderCreated {
		own, other = m.HighCreated, m.HighPushed
	}
	if own.IsZero() {
		return other
	}
	return own
}

// Listing is one read of one account, with what it cost a reader the right to conclude.
type Listing struct {
	Repositories []Repository `json:"repositories,omitempty"`
	Route        string       `json:"route"`
	// Full is true only when every page was read. A repository that no longer exists appears in
	// no listing, so nothing but a full one can establish that something is gone — and a sweep
	// that claimed to be full would let the corpus keep dead repositories while looking current.
	Full bool `json:"full"`
	// Fallback says why the preferred route did not answer. Empty when it did.
	Fallback string `json:"fallback,omitempty"`
	// Pages is how many round trips the read took. An account whose full listing fits on one page
	// costs less to read whole than to sweep, because a sweep is two ordered walks: measured at
	// 1 point against 2 on a 27 repository organization.
	Pages int `json:"pages"`
	// PropertiesRead says the custom property values were actually read. False leaves them
	// unknown rather than known to be empty, which is the difference between a repository the
	// organization puts in no unit and one nobody was allowed to ask about.
	PropertiesRead bool `json:"propertiesRead"`
	// PropertiesUnread says why, when they were not.
	PropertiesUnread string `json:"propertiesUnread,omitempty"`
	Marks            Marks  `json:"marks"`
}

// ListAll reads every page of an account. It is the only read that can establish absence.
func (c *Client) ListAll(ctx context.Context, account string) (Listing, error) {
	return c.read(ctx, account, Marks{})
}

// Sweep reads only what moved since the given marks.
//
// Two ordered walks rather than one: a repository can be created and never pushed, so ordering by
// push would never reach it, and ordering by creation would never reach a push to an old one.
// With no marks behind it this is a full listing, because there is nothing to stop at.
func (c *Client) Sweep(ctx context.Context, account string, since Marks) (Listing, error) {
	return c.read(ctx, account, since)
}

// read runs the preferred route and falls back to the other, unless the operator pinned one.
func (c *Client) read(ctx context.Context, account string, since Marks) (Listing, error) {
	if account == "" || strings.ContainsAny(account, "/?#") {
		return Listing{}, fmt.Errorf("account %q is not a valid name", account)
	}
	if c.Route == RouteREST {
		return c.readREST(ctx, account, since)
	}

	listing, err := c.readGraphQL(ctx, account, since)
	if err == nil {
		return listing, nil
	}
	// Pinned to GraphQL means the operator wants the failure, not a quieter answer from
	// somewhere else that costs a budget they did not choose to spend.
	if c.Route == RouteGraphQL {
		return Listing{}, err
	}
	listing, restErr := c.readREST(ctx, account, since)
	if restErr != nil {
		return Listing{}, fmt.Errorf("%w (GraphQL first said: %v)", restErr, err)
	}
	listing.Fallback = err.Error()
	return listing, nil
}

// readGraphQL answers both questions over the preferred route.
//
// The property values arrive on the same pages as the repositories, so there is nothing extra to
// fetch and nothing to correlate afterwards.
func (c *Client) readGraphQL(ctx context.Context, account string, since Marks) (Listing, error) {
	listing, err := readVia(RouteGraphQL, since,
		func(field string, bound time.Time) ([]Repository, Marks, int, error) {
			return c.walkGraphQL(ctx, account, field, bound)
		})
	if err != nil {
		return Listing{}, err
	}
	listing.PropertiesRead = true
	return listing, nil
}

// readREST answers both questions over the fallback route, which must stay able to answer both.
//
// The repository listing carries no properties here, so they cost a second walk of the whole
// organization — paginated a hundred repositories at a time, not a request each.
func (c *Client) readREST(ctx context.Context, account string, since Marks) (Listing, error) {
	listing, err := readVia(RouteREST, since,
		func(field string, bound time.Time) ([]Repository, Marks, int, error) {
			return c.walkREST(ctx, account, field, bound)
		})
	if err != nil {
		return Listing{}, err
	}
	listing.PropertiesRead, listing.PropertiesUnread = c.attachRESTProperties(
		ctx, account, listing.Repositories)
	return listing, nil
}

// walk is one ordered pass over an account, stopping below bound. A zero bound reads everything.
type walk func(field string, bound time.Time) ([]Repository, Marks, int, error)

// readVia runs the walks one route needs, so that both routes answer the same questions in the same
// order and a test can hold them to it.
//
// The route is passed in rather than read from the client, because a fallback runs with no route
// pinned and would otherwise label REST's answer as GraphQL's — which is the one thing the field
// exists to prevent.
func readVia(route string, since Marks, w walk) (Listing, error) {
	listing := Listing{Route: route, Full: since.Empty()}

	if listing.Full {
		repos, marks, pages, err := w(orderPushed, time.Time{})
		if err != nil {
			return Listing{}, err
		}
		listing.Repositories, listing.Marks, listing.Pages = repos, marks, pages
		return listing, nil
	}

	moved, movedMarks, movedPages, err := w(orderPushed, since.bound(orderPushed))
	if err != nil {
		return Listing{}, err
	}
	created, createdMarks, createdPages, err := w(orderCreated, since.bound(orderCreated))
	if err != nil {
		return Listing{}, err
	}
	listing.Repositories = mergeByName(moved, created)
	listing.Marks = Marks{HighCreated: createdMarks.HighCreated, HighPushed: movedMarks.HighPushed}
	listing.Pages = movedPages + createdPages
	return listing, nil
}

// mergeByName joins two prefixes, keeping the first sighting. A repository that was both pushed and
// created since the last read appears in both walks and is one repository.
func mergeByName(first, second []Repository) []Repository {
	seen := make(map[string]struct{}, len(first)+len(second))
	out := make([]Repository, 0, len(first)+len(second))
	for _, group := range [][]Repository{first, second} {
		for _, r := range group {
			if _, dup := seen[r.FullName]; dup {
				continue
			}
			seen[r.FullName] = struct{}{}
			out = append(out, r)
		}
	}
	return out
}

// repositoriesQuery reads one page of an account's repositories in a chosen order.
//
// `repositoryOwner` rather than `organization`, because an account is an organization or a person
// and GraphQL answers for both through the same interface — where REST keeps them on two endpoints
// and answers the wrong list for the wrong one. `rateLimit` rides along so the price is GitHub's
// figure rather than ours.
const repositoriesQuery = `
query($login:String!,$after:String,$field:RepositoryOrderField!,$first:Int!,$properties:Int!){
  rateLimit{cost remaining limit}
  repositoryOwner(login:$login){
    repositories(first:$first,after:$after,orderBy:{field:$field,direction:DESC}){
      pageInfo{hasNextPage endCursor}
      nodes{
        nameWithOwner isArchived createdAt pushedAt defaultBranchRef{name}
        repositoryCustomPropertyValues(first:$properties){totalCount nodes{propertyName value}}
      }
    }
  }
}`

// walkGraphQL pages through an account in the given order, stopping below bound.
func (c *Client) walkGraphQL(
	ctx context.Context, account, field string, bound time.Time,
) ([]Repository, Marks, int, error) {
	var (
		repos  []Repository
		marks  Marks
		cursor string
		pages  int
	)
	for {
		var page struct {
			RepositoryOwner *struct {
				Repositories struct {
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						NameWithOwner    string `json:"nameWithOwner"`
						IsArchived       bool   `json:"isArchived"`
						CreatedAt        string `json:"createdAt"`
						PushedAt         string `json:"pushedAt"`
						DefaultBranchRef *struct {
							Name string `json:"name"`
						} `json:"defaultBranchRef"`
						Properties struct {
							TotalCount int            `json:"totalCount"`
							Nodes      []propertyNode `json:"nodes"`
						} `json:"repositoryCustomPropertyValues"`
					} `json:"nodes"`
				} `json:"repositories"`
			} `json:"repositoryOwner"`
		}
		vars := map[string]any{
			"login": account, "field": field,
			"first": reposPerGraphQLPage, "properties": maxRepositoryProperties,
		}
		if cursor != "" {
			vars["after"] = cursor
		}
		if err := c.graphQL(ctx, repositoriesQuery, vars, &page); err != nil {
			return nil, Marks{}, pages, err
		}
		pages++
		if page.RepositoryOwner == nil {
			return nil, Marks{}, pages, fmt.Errorf("%s: %w", account, ErrNoDatabase)
		}

		for _, node := range page.RepositoryOwner.Repositories.Nodes {
			repo := Repository{
				FullName:  node.NameWithOwner,
				CreatedAt: parseTimestamp(node.CreatedAt),
				PushedAt:  parseTimestamp(node.PushedAt),
			}
			if node.DefaultBranchRef != nil {
				repo.DefaultBranch = node.DefaultBranchRef.Name
			}
			// Kept whole or not at all. Half the values would read as a deliberate placement in
			// a unit the organization did not choose.
			if node.Properties.TotalCount > len(node.Properties.Nodes) {
				repo.PropertiesTruncated = true
			} else {
				for _, p := range node.Properties.Nodes {
					repo.Properties = append(repo.Properties, p.values()...)
				}
			}
			if stop := absorb(&repos, &marks, repo, node.IsArchived, field, bound); stop {
				return repos, marks, pages, nil
			}
		}

		info := page.RepositoryOwner.Repositories.PageInfo
		if !info.HasNextPage || info.EndCursor == "" {
			return repos, marks, pages, nil
		}
		cursor = info.EndCursor
	}
}

// walkREST does the same over the fallback route, which GitHub sorts with the same two orderings.
func (c *Client) walkREST(
	ctx context.Context, account, field string, bound time.Time,
) ([]Repository, Marks, int, error) {
	sort := "pushed"
	if field == orderCreated {
		sort = "created"
	}
	next := fmt.Sprintf("%s/orgs/%s/repos?per_page=%d&type=all&sort=%s&direction=desc",
		c.baseURL, url.PathEscape(account), reposPerPage, sort)

	repos, marks, pages, err := c.walkRESTFrom(ctx, next, account, field, bound)
	if err == nil {
		return repos, marks, pages, nil
	}
	if !errors.Is(err, ErrNoDatabase) {
		return nil, Marks{}, pages, err
	}
	// Not an organization. `type=all` is not a parameter the user endpoint takes.
	return c.walkRESTFrom(ctx, fmt.Sprintf("%s/users/%s/repos?per_page=%d&sort=%s&direction=desc",
		c.baseURL, url.PathEscape(account), reposPerPage, sort), account, field, bound)
}

func (c *Client) walkRESTFrom(
	ctx context.Context, next, account, field string, bound time.Time,
) ([]Repository, Marks, int, error) {
	var (
		repos []Repository
		marks Marks
		pages int
	)
	for next != "" {
		resp, err := c.do(ctx, next, jsonAccept)
		if err != nil {
			return nil, Marks{}, pages, err
		}
		pages++
		var page []struct {
			FullName      string `json:"full_name"`
			Archived      bool   `json:"archived"`
			DefaultBranch string `json:"default_branch"`
			CreatedAt     string `json:"created_at"`
			PushedAt      string `json:"pushed_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			resp.Body.Close()
			return nil, Marks{}, pages, fmt.Errorf("decoding repository page for %s: %w", account, err)
		}
		link := resp.Header.Get("Link")
		resp.Body.Close()

		for _, r := range page {
			repo := Repository{
				FullName:      r.FullName,
				DefaultBranch: r.DefaultBranch,
				CreatedAt:     parseTimestamp(r.CreatedAt),
				PushedAt:      parseTimestamp(r.PushedAt),
			}
			if stop := absorb(&repos, &marks, repo, r.Archived, field, bound); stop {
				return repos, marks, pages, nil
			}
		}
		next = nextLink(link)
	}
	return repos, marks, pages, nil
}

// absorb takes one repository from a walk, reporting whether the walk has reached the mark.
//
// The marks move for every repository seen, archived ones included. A sweep that skipped an
// archived repository's timestamp would stop below it again on every later run, re-reading a prefix
// it has already decided to ignore.
func absorb(repos *[]Repository, marks *Marks, repo Repository, archived bool, field string, bound time.Time) bool {
	if repo.FullName == "" {
		return false
	}
	// Ordered descending, so the first value at or below the mark ends the walk: everything
	// after it is older still.
	if !bound.IsZero() && !ordering(repo, field).After(bound) {
		return true
	}
	if repo.CreatedAt.After(marks.HighCreated) {
		marks.HighCreated = repo.CreatedAt
	}
	if repo.PushedAt.After(marks.HighPushed) {
		marks.HighPushed = repo.PushedAt
	}
	// An archived repository cannot take a version bump, so counting it would overstate every
	// remediation estimate. It still moved the marks above, because the walk did see it.
	if archived {
		return false
	}
	*repos = append(*repos, repo)
	return false
}

// ordering picks the field a walk is sorted by.
func ordering(r Repository, field string) time.Time {
	if field == orderCreated {
		return r.CreatedAt
	}
	return r.PushedAt
}

// parseTimestamp reads one of GitHub's instants, answering the zero time for a repository that has
// none — a repository with no commits has never been pushed, and year one would be a fabrication.
func parseTimestamp(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
