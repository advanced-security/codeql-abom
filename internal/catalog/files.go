package catalog

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/advanced-security/codeql-abom/internal/work"
)

// CommitFile is one file of a commit. Unread says why there is no text, which is never an empty file.
type CommitFile struct {
	Text   string
	Unread string
}

// FileBatch is one query's files by path, and what GitHub charged and reported.
type FileBatch struct {
	Files    map[string]CommitFile
	Requests int
	Cost     int
	Priced   bool
	Reading  work.Reading
}

// CommitFiles reads files of one commit in one GraphQL query, as the repository's owner, so a
// private repository answers where the owner installed the App.
func (c *Client) CommitFiles(ctx context.Context, repository, commit string, paths []string) (FileBatch, error) {
	out := FileBatch{Files: make(map[string]CommitFile, len(paths))}
	if len(paths) == 0 {
		return out, nil
	}
	if err := c.allowSource(ctx, repository); err != nil {
		return out, err
	}
	owner, name, err := splitRepository(repository)
	if err != nil {
		return out, err
	}
	// Every path is a variable, because a path is whatever a repository named its files.
	vars := map[string]any{"o": owner, "n": name}
	decls := []string{"$o: String!", "$n: String!"}
	fields := make([]string, 0, len(paths))
	for i, p := range paths {
		v := "e" + strconv.Itoa(i)
		vars[v] = commit + ":" + p
		decls = append(decls, "$"+v+": String!")
		fields = append(fields, fmt.Sprintf(
			"f%d: object(expression: $%s) { __typename ... on Blob { text isBinary isTruncated } }", i, v))
	}
	query := fmt.Sprintf("query(%s) {\nrateLimit { cost remaining limit resetAt }\n"+
		"r: repository(owner: $o, name: $n) { %s }\n}", strings.Join(decls, ", "), strings.Join(fields, " "))

	var batch RefBatch
	data, errs, err := c.postRefs(ctx, query, vars, owner, &batch)
	out.Requests, out.Cost, out.Priced, out.Reading = batch.Requests, batch.Cost, batch.Priced, batch.Reading
	if err != nil {
		return out, err
	}
	unreadAll := func(why string) (FileBatch, error) {
		for _, p := range paths {
			out.Files[p] = CommitFile{Unread: why}
		}
		return out, nil
	}
	raw := data["r"]
	if len(raw) == 0 || string(raw) == "null" {
		return unreadAll(cmp.Or(reasonAt(errs, "r", ""), "GitHub answered nothing for "+repository))
	}
	var blobs map[string]*struct {
		Type        string  `json:"__typename"`
		Text        *string `json:"text"`
		IsBinary    bool    `json:"isBinary"`
		IsTruncated bool    `json:"isTruncated"`
	}
	if err := json.Unmarshal(raw, &blobs); err != nil {
		return unreadAll(fmt.Sprintf("decoding the files of %s at %s: %v", repository, commit, err))
	}
	for i, p := range paths {
		alias := "f" + strconv.Itoa(i)
		if why := reasonAt(errs, "r", alias); why != "" {
			out.Files[p] = CommitFile{Unread: why}
			continue
		}
		b := blobs[alias]
		switch {
		case b == nil:
			out.Files[p] = CommitFile{Unread: fmt.Sprintf("GitHub holds no file at %s in %s", p, commit)}
		case b.Type != "Blob":
			out.Files[p] = CommitFile{Unread: fmt.Sprintf("%s at %s is a %s, not a file", p, commit, b.Type)}
		case b.IsBinary:
			out.Files[p] = CommitFile{Unread: p + " is binary"}
		case b.IsTruncated || b.Text == nil:
			out.Files[p] = CommitFile{Unread: "GitHub gave only part of it: " + p}
		default:
			out.Files[p] = CommitFile{Text: *b.Text}
		}
	}
	return out, nil
}
