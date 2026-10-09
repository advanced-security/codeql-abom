package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// ExampleFiles reads what a linked example names from GitHub.
type ExampleFiles interface {
	ResolveRefs(ctx context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error)
	CommitFiles(ctx context.Context, repository, commit string, paths []string) (catalog.FileBatch, error)
}

// maxExampleLines bounds one linked example: it becomes a test fixture and part of an agent's
// prompt, and a link to a whole generated file would swamp both.
const maxExampleLines = 400

var (
	repoSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	lineRange   = regexp.MustCompile(`^L([0-9]+)(?:-L([0-9]+))?$`)
	commitSHA   = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// errNoReader is a link with nothing to read it: a configuration matter, not the operator's mistake.
var errNoReader = errors.New("ABOM has no GitHub reader configured, so it cannot read a linked example; paste it instead")

// blobLink is a parsed https://github.com/{owner}/{repo}/blob/{ref…}/{path}[#L{a}[-L{b}]]. Where the
// ref ends and the path begins is not in the link, so rest keeps both. A zero start is the whole file.
type blobLink struct {
	owner, repo string
	rest        []string
	start, end  int
}

func parseBlobLink(raw string) (blobLink, error) {
	refuse := func(why string) (blobLink, error) {
		return blobLink{}, fmt.Errorf("the link %q %s", raw, why)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return refuse("is not an https://github.com link")
	}
	segs := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segs) < 5 || segs[2] != "blob" {
		return refuse("does not name a file: write https://github.com/owner/repo/blob/ref/path")
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return refuse("holds an empty, . or .. segment")
		}
	}
	if !repoSegment.MatchString(segs[0]) || !repoSegment.MatchString(segs[1]) {
		return refuse("does not name an owner and repository")
	}
	if u.Fragment == "" {
		return blobLink{owner: segs[0], repo: segs[1], rest: segs[3:]}, nil
	}
	m := lineRange.FindStringSubmatch(u.Fragment)
	if m == nil {
		return refuse("names lines in no form GitHub writes: end it with #L10 or #L10-L20, or leave them off for the whole file")
	}
	start, _ := strconv.Atoi(m[1])
	end := start
	if m[2] != "" {
		end, _ = strconv.Atoi(m[2])
	}
	if start < 1 || end < start {
		return refuse("names lines that end before they start")
	}
	if end-start+1 > maxExampleLines {
		return refuse(fmt.Sprintf("names more than %d lines", maxExampleLines))
	}
	return blobLink{owner: segs[0], repo: segs[1], rest: segs[3:], start: start, end: end}, nil
}

// readExample reads the lines a linked example names, at the commit it was pinned to if it was,
// and otherwise at the commit its ref names now. The text the client sent is never kept.
func (s *Server) readExample(ctx context.Context, e store.RequestExample) (store.RequestExample, int, error) {
	link, err := parseBlobLink(strings.TrimSpace(e.URL))
	if err != nil {
		return e, http.StatusBadRequest, err
	}
	if s.Files == nil {
		return e, http.StatusServiceUnavailable, errNoReader
	}
	repository := link.owner + "/" + link.repo
	ref, path, commit := pinned(link, e)
	if commit == "" {
		var status int
		ref, path, commit, status, err = s.resolveLink(ctx, link)
		if err != nil {
			return e, status, err
		}
	}
	batch, err := s.Files.CommitFiles(ctx, repository, commit, []string{path})
	if err != nil {
		return e, http.StatusBadGateway, fmt.Errorf("reading %s: %w", e.URL, err)
	}
	file := batch.Files[path]
	if file.Unread != "" {
		return e, http.StatusBadRequest, errors.New(file.Unread)
	}
	lines := strings.SplitAfter(file.Text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if link.start == 0 {
		if len(lines) == 0 {
			return e, http.StatusBadRequest, fmt.Errorf("%s at %s is empty", path, commit[:7])
		}
		if len(lines) > maxExampleLines {
			return e, http.StatusBadRequest, fmt.Errorf(
				"%s at %s holds %d lines, more than the %d one example may: end the link with the lines that matter, as #L10-L20",
				path, commit[:7], len(lines), maxExampleLines)
		}
		link.start, link.end = 1, len(lines)
	}
	if link.end > len(lines) {
		return e, http.StatusBadRequest, fmt.Errorf("%s at %s holds %d lines, so it has no line %d",
			path, commit[:7], len(lines), link.end)
	}
	return store.RequestExample{
		Side: e.Side, URL: strings.TrimSpace(e.URL), Text: strings.Join(lines[link.start-1:link.end], ""),
		Repository: repository, Path: path, Ref: ref, Commit: commit,
		StartLine: link.start, EndLine: link.end,
	}, http.StatusOK, nil
}

// pinned is the commit an example was already read at, where the link still names the same ref
// and path; a link that changed is resolved again.
func pinned(link blobLink, e store.RequestExample) (string, string, string) {
	if commitSHA.MatchString(link.rest[0]) {
		return link.rest[0], strings.Join(link.rest[1:], "/"), link.rest[0]
	}
	if commitSHA.MatchString(e.Commit) && e.Ref != "" && e.Path != "" &&
		e.Ref+"/"+e.Path == strings.Join(link.rest, "/") {
		return e.Ref, e.Path, e.Commit
	}
	return "", "", ""
}

// resolveLink asks, in one question, every way of splitting the link into a ref and a path, and
// takes the shortest ref that exists, as GitHub's own blob view does.
func (s *Server) resolveLink(ctx context.Context, link blobLink) (string, string, string, int, error) {
	qs := make([]catalog.RefQuestion, len(link.rest)-1)
	for i := range qs {
		qs[i] = catalog.RefQuestion{Owner: link.owner, Repo: link.repo, Ref: strings.Join(link.rest[:i+1], "/")}
	}
	batch, err := s.Files.ResolveRefs(ctx, qs)
	if err != nil {
		return "", "", "", http.StatusBadGateway, fmt.Errorf("resolving %s/%s: %w", link.owner, link.repo, err)
	}
	unread := ""
	for i, a := range batch.Answers {
		if a.Commit != "" {
			return qs[i].Ref, strings.Join(link.rest[i+1:], "/"), a.Commit, http.StatusOK, nil
		}
		if !a.Missing && unread == "" {
			unread = a.Unread
		}
	}
	if unread != "" {
		return "", "", "", http.StatusBadGateway, fmt.Errorf("GitHub could not say where %s/%s points: %s",
			link.owner, link.repo, unread)
	}
	return "", "", "", http.StatusBadRequest, fmt.Errorf("%s/%s has no branch, tag or commit that the link names",
		link.owner, link.repo)
}

// readExamples re-reads every linked example of a request before it is kept.
func (s *Server) readExamples(ctx context.Context, body *store.QueryRequest) (int, error) {
	for i, e := range body.Examples {
		if strings.TrimSpace(e.URL) == "" {
			continue
		}
		read, status, err := s.readExample(ctx, e)
		if err != nil {
			return status, fmt.Errorf("example %d: %w", i+1, err)
		}
		body.Examples[i] = read
	}
	return http.StatusOK, nil
}

// readRequestExample reads one linked example without keeping it, so the form can show it.
func (s *Server) readRequestExample(w http.ResponseWriter, r *http.Request) {
	var body store.RequestExample
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not the expected JSON")
		return
	}
	read, status, err := s.readExample(r.Context(), body)
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, read, nil)
}
