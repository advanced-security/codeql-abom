package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// orgFixture serves one account over both routes from the same repositories, so a test can ask
// whether the two agree rather than whether each parses its own fixture.
type orgFixture struct {
	repos []fixtureRepo
	// posts and gets count what each route actually cost, which is the number these tests are
	// mostly about: an early stop that reads every page is a full listing wearing a sort order.
	posts, gets int
	// refuse makes GraphQL answer the way a token without the scope does.
	refuse bool
	// pageSize is how many repositories one page carries.
	pageSize int
}

type fixtureRepo struct {
	name     string
	created  string
	pushed   string
	archived bool
}

func (f *orgFixture) sorted(field string) []fixtureRepo {
	out := append([]fixtureRepo(nil), f.repos...)
	key := func(r fixtureRepo) string {
		if field == "CREATED_AT" {
			return r.created
		}
		return r.pushed
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && key(out[j]) > key(out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (f *orgFixture) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			f.posts++
			f.serveGraphQL(t, w, r)
			return
		}
		f.gets++
		f.serveREST(w, r)
	})
}

func (f *orgFixture) serveGraphQL(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if f.refuse {
		fmt.Fprint(w, `{"data":null,"errors":[{"type":"FORBIDDEN","message":"no graphql here"}]}`)
		return
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Variables struct {
			Field string `json:"field"`
			After string `json:"after"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("decoding the query: %v", err)
	}

	ordered := f.sorted(req.Variables.Field)
	start := 0
	if req.Variables.After != "" {
		fmt.Sscanf(req.Variables.After, "cursor-%d", &start)
	}
	end := min(start+f.pageSize, len(ordered))

	nodes := make([]string, 0, end-start)
	for _, repo := range ordered[start:end] {
		nodes = append(nodes, fmt.Sprintf(
			`{"nameWithOwner":"has-ghas/%s","isArchived":%t,"createdAt":"%s","pushedAt":"%s",
			  "defaultBranchRef":{"name":"main"}}`,
			repo.name, repo.archived, repo.created, repo.pushed))
	}
	fmt.Fprintf(w, `{"data":{"rateLimit":{"cost":1,"remaining":4999,"limit":5000},
	  "repositoryOwner":{"repositories":{
	    "pageInfo":{"hasNextPage":%t,"endCursor":"cursor-%d"},
	    "nodes":[%s]}}}}`, end < len(ordered), end, strings.Join(nodes, ","))
}

func (f *orgFixture) serveREST(w http.ResponseWriter, r *http.Request) {
	field := "PUSHED_AT"
	if r.URL.Query().Get("sort") == "created" {
		field = "CREATED_AT"
	}
	ordered := f.sorted(field)
	start := 0
	fmt.Sscanf(r.URL.Query().Get("page"), "%d", &start)
	if start > 0 {
		start = (start - 1) * f.pageSize
	}
	end := min(start+f.pageSize, len(ordered))
	if end < len(ordered) {
		next := *r.URL
		q := next.Query()
		q.Set("page", fmt.Sprint(end/f.pageSize+1))
		next.RawQuery = q.Encode()
		w.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, next.RequestURI()))
	}

	items := make([]string, 0, end-start)
	for _, repo := range ordered[start:end] {
		items = append(items, fmt.Sprintf(
			`{"full_name":"has-ghas/%s","archived":%t,"default_branch":"main","fork":false,"visibility":"public",
			  "created_at":"%s","pushed_at":"%s"}`,
			repo.name, repo.archived, repo.created, repo.pushed))
	}
	fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
}

// threePages is nine repositories pushed on nine different days, newest last in declaration order
// so that nothing depends on the fixture already being sorted.
func threePages() *orgFixture {
	repos := make([]fixtureRepo, 0, 9)
	for i := 1; i <= 9; i++ {
		repos = append(repos, fixtureRepo{
			name:    fmt.Sprintf("r%02d", i),
			created: fmt.Sprintf("2026-01-%02dT00:00:00Z", i),
			pushed:  fmt.Sprintf("2026-09-%02dT00:00:00Z", i),
		})
	}
	return &orgFixture{repos: repos, pageSize: 3}
}

func client(t *testing.T, f *orgFixture) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	c := New(srv.URL, "t", srv.Client())
	c.sleep = func(time.Duration) {}
	return c, srv.Close
}

// The whole point of the sort order is that it lets the read stop. A sweep that reads every page
// has spent a full listing and produced a smaller answer, which is worse than not sweeping.
func TestASweepStopsBelowTheMarkRatherThanReadingEveryPage(t *testing.T) {
	f := threePages()
	c, done := client(t, f)
	defer done()

	// Everything up to the 7th of September is already accounted for, so only r08 and r09 moved.
	since := Marks{HighPushed: time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)}
	listing, err := c.Sweep(context.Background(), "has-ghas", since)
	if err != nil {
		t.Fatal(err)
	}

	if got := names(listing.Repositories); strings.Join(got, ",") != "has-ghas/r09,has-ghas/r08" {
		t.Errorf("swept %v, want only the two repositories pushed after the mark", got)
	}
	// One page per question, against the three a full listing of this fixture costs. Two walks is
	// the design rather than an inefficiency: push order never reaches a repository that was
	// created and not pushed.
	if f.posts != 2 {
		t.Errorf("read %d pages, want 2 — one per ordered walk, each stopping on its first page", f.posts)
	}
	if listing.Full {
		t.Error("a prefix of a sorted order is not a full listing and must never claim to be")
	}
}

// A repository can be created and never pushed, so ordering by push would never reach it. The two
// questions have two marks because they have two answers.
func TestANewRepositoryIsFoundByCreationEvenWithNoPush(t *testing.T) {
	f := threePages()
	f.repos = append(f.repos, fixtureRepo{
		name: "fresh", created: "2026-09-30T00:00:00Z", pushed: "2020-01-01T00:00:00Z"})
	c, done := client(t, f)
	defer done()

	since := Marks{
		HighCreated: time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC),
		HighPushed:  time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC),
	}
	listing, err := c.Sweep(context.Background(), "has-ghas", since)
	if err != nil {
		t.Fatal(err)
	}

	if got := names(listing.Repositories); strings.Join(got, ",") != "has-ghas/fresh" {
		t.Errorf("swept %v, want the newly created repository that was never pushed", got)
	}
}

// Only a full listing can establish that a repository is gone, and the mark it leaves is what every
// later sweep stops at. A read that skipped a page would set the mark from an incomplete answer.
func TestAFullListingReadsEveryPageAndLeavesBothMarks(t *testing.T) {
	f := threePages()
	c, done := client(t, f)
	defer done()

	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}

	if len(listing.Repositories) != 9 {
		t.Errorf("listed %d repositories, want all 9", len(listing.Repositories))
	}
	if !listing.Full {
		t.Error("every page was read, so this is the listing that establishes absence")
	}
	want := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	if !listing.Marks.HighPushed.Equal(want) {
		t.Errorf("high-water push %v, want %v", listing.Marks.HighPushed, want)
	}
	if listing.Marks.HighCreated.IsZero() {
		t.Error("the creation mark is what the new-repository sweep stops at; it cannot be unset")
	}
}

// A capability that exists on one route only makes an organization unreadable rather than slower.
func TestWhenGraphQLRefusesRestAnswersAndSaysSo(t *testing.T) {
	f := threePages()
	f.refuse = true
	c, done := client(t, f)
	defer done()

	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if listing.Route != RouteREST {
		t.Errorf("route %q, want %q", listing.Route, RouteREST)
	}
	if listing.Fallback == "" {
		t.Error("why REST answered has to be stated, or the route is a hidden variable")
	}
	if len(listing.Repositories) != 9 {
		t.Errorf("REST listed %d repositories, want all 9", len(listing.Repositories))
	}
}

// Selection is the operator's; detection is at most a default. A pinned route that still probed the
// other one would spend a budget the operator asked it not to.
func TestAPinnedRouteIsNotSecondGuessed(t *testing.T) {
	f := threePages()
	c, done := client(t, f)
	defer done()
	c.Route = RouteREST

	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if f.posts != 0 {
		t.Errorf("made %d GraphQL calls with the route pinned to REST", f.posts)
	}
	if listing.Route != RouteREST {
		t.Errorf("route %q, want %q", listing.Route, RouteREST)
	}
}

// The two routes must agree, or every figure downstream depends on which one answered.
func TestBothRoutesReadTheSameAccountTheSameWay(t *testing.T) {
	f := threePages()
	f.repos = append(f.repos, fixtureRepo{
		name: "old", created: "2020-01-01T00:00:00Z", pushed: "2020-01-01T00:00:00Z", archived: true})

	c, done := client(t, f)
	defer done()
	viaGraphQL, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}

	c.Route = RouteREST
	viaREST, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Join(names(viaGraphQL.Repositories), ",") != strings.Join(names(viaREST.Repositories), ",") {
		t.Errorf("graphql saw %v and rest saw %v",
			names(viaGraphQL.Repositories), names(viaREST.Repositories))
	}
	for _, r := range viaGraphQL.Repositories {
		if r.FullName == "has-ghas/old" {
			t.Error("an archived repository cannot take a version bump and must not be counted")
		}
	}
	if !viaGraphQL.Marks.HighPushed.Equal(viaREST.Marks.HighPushed) {
		t.Errorf("marks differ by route: %v and %v",
			viaGraphQL.Marks.HighPushed, viaREST.Marks.HighPushed)
	}
}

// An account nobody has read has an unknown shape, so a sweep with no marks behind it has nothing
// to stop at and must read the whole thing rather than quietly reading one page of it.
func TestASweepWithNoMarksIsAFullListing(t *testing.T) {
	f := threePages()
	c, done := client(t, f)
	defer done()

	listing, err := c.Sweep(context.Background(), "has-ghas", Marks{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Repositories) != 9 || !listing.Full {
		t.Errorf("read %d repositories (full=%v), want all 9 as a full listing",
			len(listing.Repositories), listing.Full)
	}
}

// A repository with no commits has no branch, and GraphQL says so by answering null. REST answers
// the repository's *setting*, which can name a branch that does not exist. The preferred route is
// the one that does not invent the branch, and the recorded route makes the difference attributable.
func TestAnEmptyRepositoryIsNotGivenABranchItDoesNotHave(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"rateLimit":{"cost":1,"remaining":9999,"limit":10000},
		  "repositoryOwner":{"repositories":{
		    "pageInfo":{"hasNextPage":false,"endCursor":""},
		    "nodes":[{"nameWithOwner":"has-ghas/codeql-clone","isArchived":false,
		              "createdAt":"2024-10-03T21:50:22Z","pushedAt":"2024-10-03T21:50:22Z",
		              "defaultBranchRef":null}]}}}}`)
	}))
	defer srv.Close()

	c := New(srv.URL, "t", srv.Client())
	listing, err := c.ListAll(context.Background(), "has-ghas")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Repositories) != 1 {
		t.Fatalf("listed %d repositories, want 1", len(listing.Repositories))
	}
	if got := listing.Repositories[0].DefaultBranch; got != "" {
		t.Errorf("default branch %q, want none — the repository has no branches", got)
	}
}
