package store

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The two sides an example can stand on.
const (
	ExampleCompliant    = "compliant"
	ExampleNonCompliant = "non-compliant"
)

// What a reference names.
const (
	ReferenceCVE  = "cve"
	ReferenceCWE  = "cwe"
	ReferenceGHSA = "ghsa"
	ReferenceURL  = "url"
)

// Whether the operator asked to change a query that exists or to add one.
const (
	DecisionImprove = "improve"
	DecisionCreate  = "create"
)

// The values CodeQL's query metadata accepts, empty for a request that does not ask.
var (
	requestPrecisions = []string{"", "low", "medium", "high", "very-high"}
	requestSeverities = []string{"", "error", "warning", "recommendation"}
)

// QueryRequestPrecisions and QueryRequestSeverities are the vocabularies, for a control to offer.
func QueryRequestPrecisions() []string { return slices.Clone(requestPrecisions[1:]) }

func QueryRequestSeverities() []string { return slices.Clone(requestSeverities[1:]) }

// RequestExample is one example a request gives: pasted, or read from a repository at a commit.
type RequestExample struct {
	Side string `json:"side"`
	Text string `json:"text"`
	// URL is the link the operator gave, empty for a pasted example. The rest say what it was read
	// as: Ref as written in the link, Commit what it named when the text was read.
	URL        string `json:"url"`
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Ref        string `json:"ref"`
	Commit     string `json:"commit"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
}

// RequestReference is a CVE, CWE, GHSA or link a request cites.
type RequestReference struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// RequestedMetadata is the QLDoc a request asks its query to declare. A nil SecuritySeverity asks
// for none, which is not a severity of zero.
type RequestedMetadata struct {
	Description      string   `json:"description"`
	Precision        string   `json:"precision"`
	ProblemSeverity  string   `json:"problemSeverity"`
	SecuritySeverity *float64 `json:"securitySeverity"`
	Tags             []string `json:"tags"`
}

func (m RequestedMetadata) empty() bool {
	return m.Description == "" && m.Precision == "" && m.ProblemSeverity == "" &&
		m.SecuritySeverity == nil && len(m.Tags) == 0
}

var (
	requestSlug = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	metadataTag = regexp.MustCompile(`^[a-z0-9][a-z0-9._/+-]*$`)
	commitSHA   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	repoName    = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)
	cveID       = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)
	cweID       = regexp.MustCompile(`^CWE-(\d+)$`)
	ghsaID      = regexp.MustCompile(`^GHSA(-[23456789CFGHJMPQRVWX]{4}){3}$`)
)

// normalize fills Examples from the two texts for a client that sends only those, then the two
// texts from Examples for a reader that reads only those, and spells each reference and tag once.
func (r *QueryRequest) normalize() error {
	r.Title = strings.TrimSpace(r.Title)
	r.Slug = strings.TrimSpace(r.Slug)
	r.Metadata.Description = strings.TrimSpace(r.Metadata.Description)
	if len(r.Examples) == 0 {
		r.Examples = legacyExamples(r.Compliant, r.NonCompliant)
	}
	r.Compliant, r.NonCompliant = firstExample(r.Examples, ExampleCompliant), firstExample(r.Examples, ExampleNonCompliant)

	refs := []RequestReference{}
	for _, ref := range r.References {
		parsed, err := parseReference(ref.Value)
		if err != nil {
			return err
		}
		if !slices.Contains(refs, parsed) {
			refs = append(refs, parsed)
		}
	}
	r.References = refs
	r.ReferenceTags = referenceTags(refs)

	tags := []string{}
	for _, t := range r.Metadata.Tags {
		if t = strings.TrimSpace(t); !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	r.Metadata.Tags = tags
	return nil
}

func legacyExamples(compliant, nonCompliant string) []RequestExample {
	out := []RequestExample{}
	if strings.TrimSpace(compliant) != "" {
		out = append(out, RequestExample{Side: ExampleCompliant, Text: compliant})
	}
	if strings.TrimSpace(nonCompliant) != "" {
		out = append(out, RequestExample{Side: ExampleNonCompliant, Text: nonCompliant})
	}
	return out
}

func firstExample(examples []RequestExample, side string) string {
	for _, e := range examples {
		if e.Side == side {
			return e.Text
		}
	}
	return ""
}

// parseReference reads a CVE, CWE or GHSA id in any case, or an https link.
func parseReference(raw string) (RequestReference, error) {
	raw = strings.TrimSpace(raw)
	upper := strings.ToUpper(raw)
	switch {
	case cveID.MatchString(upper):
		return RequestReference{Kind: ReferenceCVE, Value: upper}, nil
	case cweID.MatchString(upper):
		n, err := strconv.Atoi(cweID.FindStringSubmatch(upper)[1])
		if err == nil && n > 0 {
			return RequestReference{Kind: ReferenceCWE, Value: fmt.Sprintf("CWE-%d", n)}, nil
		}
	case ghsaID.MatchString(upper):
		return RequestReference{Kind: ReferenceGHSA, Value: "GHSA" + strings.ToLower(upper[4:])}, nil
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		return RequestReference{Kind: ReferenceURL, Value: raw}, nil
	}
	return RequestReference{}, fmt.Errorf("%q is not a CVE, CWE or GHSA id, or an https link", raw)
}

// referenceTags are the tags CodeQL's style guide gives a CVE or a CWE, the CWE padded to three
// digits.
func referenceTags(refs []RequestReference) []string {
	tags := []string{}
	for _, ref := range refs {
		var tag string
		switch ref.Kind {
		case ReferenceCVE:
			tag = "external/cve/" + strings.ToLower(ref.Value)
		case ReferenceCWE:
			n, _ := strconv.Atoi(strings.TrimPrefix(ref.Value, "CWE-"))
			tag = fmt.Sprintf("external/cwe/cwe-%03d", n)
		default:
			continue
		}
		if !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)
	return tags
}

// checkDetails refuses what a query could not be written from: an example on no side, a link not
// pinned to a commit, metadata CodeQL would reject, or a value that would end the QLDoc it is
// written into.
func (r QueryRequest) checkDetails() error {
	if strings.Contains(r.Title, "*/") || strings.ContainsAny(r.Title, "\r\n") {
		return errors.New("a title is one line of a query's QLDoc, so it holds no line break or */")
	}
	if r.Slug != "" && (!requestSlug.MatchString(r.Slug) || len(r.Slug) > 64) {
		return fmt.Errorf("a slug is lower-case words joined by single hyphens, such as "+
			"self-hosted-pr-runner, not %q", r.Slug)
	}
	sides := map[string]int{}
	for i, e := range r.Examples {
		if err := e.check(); err != nil {
			return fmt.Errorf("example %d: %w", i+1, err)
		}
		sides[e.Side]++
	}
	for _, side := range []string{ExampleCompliant, ExampleNonCompliant} {
		if sides[side] == 0 {
			return fmt.Errorf("a request needs a %s example", side)
		}
	}
	if err := r.Metadata.check(); err != nil {
		return err
	}
	switch {
	case r.TargetDecision != "" && r.TargetDecision != DecisionImprove && r.TargetDecision != DecisionCreate:
		return fmt.Errorf("a request asks to %s or %s a query, not to %q", DecisionImprove, DecisionCreate,
			r.TargetDecision)
	case r.TargetDecision != "" && r.QueryID == "":
		return fmt.Errorf("a request asking to %s a query names it", r.TargetDecision)
	case r.TargetDecision == DecisionImprove && !r.Metadata.empty():
		return fmt.Errorf("%s is in the catalog, so its metadata is the catalog's; ask for what it "+
			"should find instead", r.QueryID)
	}
	return nil
}

func (e RequestExample) check() error {
	if e.Side != ExampleCompliant && e.Side != ExampleNonCompliant {
		return fmt.Errorf("an example is %s or %s, not %q", ExampleCompliant, ExampleNonCompliant, e.Side)
	}
	if strings.TrimSpace(e.Text) == "" {
		return errors.New("an example has text")
	}
	if e.URL == "" {
		if e.Repository != "" || e.Path != "" || e.Ref != "" || e.Commit != "" || e.StartLine != 0 || e.EndLine != 0 {
			return errors.New("a pasted example was read from nowhere, so it names no repository, commit or lines")
		}
		return nil
	}
	switch {
	case !strings.HasPrefix(e.URL, "https://"):
		return fmt.Errorf("%s is not an https link", e.URL)
	case !repoName.MatchString(e.Repository) || e.Path == "":
		return fmt.Errorf("%s does not say which file of which repository it was read from", e.URL)
	case !commitSHA.MatchString(e.Commit):
		return fmt.Errorf("%s was not read at a commit, so a test built from it would change when "+
			"the branch moves", e.URL)
	case e.StartLine < 1 || e.EndLine < e.StartLine:
		return fmt.Errorf("%s names lines %d to %d", e.URL, e.StartLine, e.EndLine)
	}
	return nil
}

func (m RequestedMetadata) check() error {
	switch {
	case !slices.Contains(requestPrecisions, m.Precision):
		return fmt.Errorf("@precision is %s, not %q", strings.Join(requestPrecisions[1:], ", "), m.Precision)
	case !slices.Contains(requestSeverities, m.ProblemSeverity):
		return fmt.Errorf("@problem.severity is %s, not %q", strings.Join(requestSeverities[1:], ", "),
			m.ProblemSeverity)
	case strings.Contains(m.Description, "*/"):
		return errors.New("a @description is written into a query's QLDoc, so it cannot hold */")
	}
	for _, tag := range m.Tags {
		if !metadataTag.MatchString(tag) {
			return fmt.Errorf("a tag is lower-case with no spaces, such as external/cwe/cwe-829, not %q", tag)
		}
	}
	if s := m.SecuritySeverity; s != nil {
		if math.IsNaN(*s) || *s < 0 || *s > 10 {
			return fmt.Errorf("@security-severity is from 0.0 to 10.0, not %v", *s)
		}
		if !slices.Contains(m.Tags, "security") {
			return errors.New("@security-severity is read only on a query tagged security")
		}
	}
	return nil
}

// slugFree refuses a slug another request is already named by.
func slugFree(tx *transaction, slug string, self int64) error {
	if slug == "" {
		return nil
	}
	var other int64
	switch err := tx.QueryRow(`SELECT row_id FROM abom_query_request WHERE slug = ? AND row_id <> ?`,
		slug, self).Scan(&other); {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("reading who is named %s: %w", slug, err)
	}
	return fmt.Errorf("%s already names request %d", slug, other)
}

// keepRequestDetails replaces a request's examples, references and tags with r's.
func keepRequestDetails(tx *transaction, id int64, r QueryRequest) error {
	for _, table := range []string{"abom_query_request_example", "abom_query_request_reference",
		"abom_query_request_tag"} {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE request_id = ?`, id); err != nil {
			return fmt.Errorf("clearing %s of request %d: %w", table, id, err)
		}
	}
	for i, e := range r.Examples {
		var start, end any
		if e.URL != "" {
			start, end = e.StartLine, e.EndLine
		}
		if _, err := tx.Exec(`
INSERT INTO abom_query_request_example
  (request_id, ordinal, side, body, url, repository, path, ref, commit_sha, start_line, end_line)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, i, e.Side, e.Text, nullable(e.URL),
			nullable(e.Repository), nullable(e.Path), nullable(e.Ref), nullable(e.Commit), start, end); err != nil {
			return fmt.Errorf("keeping example %d of request %d: %w", i+1, id, err)
		}
	}
	for i, ref := range r.References {
		if _, err := tx.Exec(`
INSERT INTO abom_query_request_reference (request_id, ordinal, kind, value) VALUES (?, ?, ?, ?)`,
			id, i, ref.Kind, ref.Value); err != nil {
			return fmt.Errorf("keeping reference %s of request %d: %w", ref.Value, id, err)
		}
	}
	for i, tag := range r.Metadata.Tags {
		if _, err := tx.Exec(`
INSERT INTO abom_query_request_tag (request_id, ordinal, tag) VALUES (?, ?, ?)`, id, i, tag); err != nil {
			return fmt.Errorf("keeping tag %s of request %d: %w", tag, id, err)
		}
	}
	return nil
}

// requestDetailRows are every request's examples, references and tags, keyed by the request.
type requestDetailRows struct {
	examples   map[int64][]RequestExample
	references map[int64][]RequestReference
	tags       map[int64][]string
}

// fill gives r its details. A request kept before examples had rows reads as the two texts it was
// filed with.
func (d requestDetailRows) fill(r *QueryRequest) {
	r.Examples = append([]RequestExample{}, d.examples[r.RowID]...)
	if len(r.Examples) == 0 {
		r.Examples = legacyExamples(r.Compliant, r.NonCompliant)
	}
	r.References = append([]RequestReference{}, d.references[r.RowID]...)
	r.ReferenceTags = referenceTags(r.References)
	r.Metadata.Tags = append([]string{}, d.tags[r.RowID]...)
}

func (s *Store) requestDetails() (requestDetailRows, error) {
	d := requestDetailRows{examples: map[int64][]RequestExample{},
		references: map[int64][]RequestReference{}, tags: map[int64][]string{}}
	rows, err := s.db.Query(`
		SELECT request_id, side, body, COALESCE(url, ''), COALESCE(repository, ''), COALESCE(path, ''),
		       COALESCE(ref, ''), COALESCE(commit_sha, ''), COALESCE(start_line, 0), COALESCE(end_line, 0)
		FROM abom_query_request_example ORDER BY request_id, ordinal`)
	if err != nil {
		return d, fmt.Errorf("reading request examples: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var e RequestExample
		if err := rows.Scan(&id, &e.Side, &e.Text, &e.URL, &e.Repository, &e.Path, &e.Ref, &e.Commit,
			&e.StartLine, &e.EndLine); err != nil {
			return d, fmt.Errorf("scanning a request example: %w", err)
		}
		d.examples[id] = append(d.examples[id], e)
	}
	if err := rows.Err(); err != nil {
		return d, err
	}

	refs, err := s.db.Query(`
		SELECT request_id, kind, value FROM abom_query_request_reference ORDER BY request_id, ordinal`)
	if err != nil {
		return d, fmt.Errorf("reading request references: %w", err)
	}
	defer refs.Close()
	for refs.Next() {
		var id int64
		var ref RequestReference
		if err := refs.Scan(&id, &ref.Kind, &ref.Value); err != nil {
			return d, fmt.Errorf("scanning a request reference: %w", err)
		}
		d.references[id] = append(d.references[id], ref)
	}
	if err := refs.Err(); err != nil {
		return d, err
	}

	tags, err := s.db.Query(`SELECT request_id, tag FROM abom_query_request_tag ORDER BY request_id, ordinal`)
	if err != nil {
		return d, fmt.Errorf("reading request tags: %w", err)
	}
	defer tags.Close()
	for tags.Next() {
		var id int64
		var tag string
		if err := tags.Scan(&id, &tag); err != nil {
			return d, fmt.Errorf("scanning a request tag: %w", err)
		}
		d.tags[id] = append(d.tags[id], tag)
	}
	return d, tags.Err()
}
