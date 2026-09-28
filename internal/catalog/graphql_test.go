package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// GraphQL answers 200 with an `errors` array, so a reader that only checks the status code takes a
// refusal for an empty organization. That is the difference between "we could not read it" and
// "there is nothing there", and only one of them is safe to act on.
func TestAGraphQLRefusalIsAnErrorRatherThanAnEmptyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"FORBIDDEN","message":"insufficient scopes"}]}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	var out struct{}
	err := c.graphQL(context.Background(), "query{viewer{login}}", nil, &out)
	if err == nil {
		t.Fatal("a refusal answered 200 and was read as success")
	}
	if !strings.Contains(err.Error(), "insufficient scopes") {
		t.Errorf("error %q does not carry what GitHub said", err)
	}
}

func TestAGraphQLReadSendsTheQueryAndItsVariables(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method %s, want POST", r.Method)
		}
		raw := make([]byte, r.ContentLength)
		if _, err := r.Body.Read(raw); err != nil && err.Error() != "EOF" {
			t.Fatal(err)
		}
		body = string(raw)
		fmt.Fprint(w, `{"data":{"viewer":{"login":"octocat"}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	var out struct {
		Viewer struct{ Login string } `json:"viewer"`
	}
	if err := c.graphQL(context.Background(), "query($o:String!){viewer{login}}",
		map[string]any{"o": "has-ghas"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Viewer.Login != "octocat" {
		t.Errorf("login %q, want octocat", out.Viewer.Login)
	}
	if !strings.Contains(body, `"variables"`) || !strings.Contains(body, "has-ghas") {
		t.Errorf("request body %q carried no variables", body)
	}
}

// `rateLimit { cost }` rides along with the data so the price is GitHub's rather than ours.
func TestTheCostOfAQueryIsReadFromTheAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":3,"remaining":4997,"limit":5000},
		                         "viewer":{"login":"octocat"}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	var out struct {
		Viewer struct{ Login string } `json:"viewer"`
	}
	if err := c.graphQL(context.Background(), "query{viewer{login}}", nil, &out); err != nil {
		t.Fatal(err)
	}
	if got := c.Stats().Points; got != 3 {
		t.Errorf("spent %d points, want the 3 GitHub charged", got)
	}
	if budget := c.Stats().GraphQL; !budget.Known || budget.Remaining != 4997 {
		t.Errorf("graphql budget %+v, want 4997 of 5000 known", budget)
	}
}

// The two budgets are separate, so a REST answer must not be able to overwrite what GraphQL last
// said was left. Reporting one for the other is how a crawl with points to spare concludes it has
// none, or the reverse.
func TestTheTwoBudgetsAreReadSeparately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("x-ratelimit-resource", "graphql")
			w.Header().Set("x-ratelimit-remaining", "4999")
			w.Header().Set("x-ratelimit-limit", "5000")
			w.Header().Set("x-ratelimit-reset", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			fmt.Fprint(w, `{"data":{"viewer":{"login":"octocat"}}}`)
			return
		}
		w.Header().Set("x-ratelimit-resource", "core")
		w.Header().Set("x-ratelimit-remaining", "4321")
		w.Header().Set("x-ratelimit-limit", "15000")
		fmt.Fprint(w, `[{"full_name":"has-ghas/a"}]`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	var out struct {
		Viewer struct{ Login string } `json:"viewer"`
	}
	if err := c.graphQL(context.Background(), "query{viewer{login}}", nil, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListRepositories(context.Background(), "has-ghas"); err != nil {
		t.Fatal(err)
	}

	stats := c.Stats()
	if stats.GraphQL.Remaining != 4999 {
		t.Errorf("graphql remaining %d, want 4999 — a REST answer overwrote it", stats.GraphQL.Remaining)
	}
	if stats.RateLimit.Remaining != 4321 {
		t.Errorf("rest remaining %d, want 4321", stats.RateLimit.Remaining)
	}
}

// GitHub Enterprise Server puts REST under /api/v3 and GraphQL beside it under /api/graphql, not
// beneath it. Appending would address /api/v3/graphql, which does not exist, and the fallback would
// then make every enterprise install look like one whose token cannot read GraphQL.
func TestTheGraphQLAddressIsBesideTheRestOneOnEnterprise(t *testing.T) {
	tests := []struct{ base, want string }{
		{"https://api.github.com", "https://api.github.com/graphql"},
		{"https://api.github.com/", "https://api.github.com/graphql"},
		{"https://ghe.example.com/api/v3", "https://ghe.example.com/api/graphql"},
		{"https://ghe.example.com/api/v3/", "https://ghe.example.com/api/graphql"},
	}
	for _, tt := range tests {
		if got := graphQLEndpoint(tt.base); got != tt.want {
			t.Errorf("graphQLEndpoint(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}
