package checks

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

func (f *fakeGitHub) Tree(_ context.Context, repository, commit string) (catalog.Tree, error) {
	f.read = append(f.read, repository+"@"+commit)
	tree, ok := f.trees[repository+"@"+commit]
	if !ok {
		return catalog.Tree{}, fmt.Errorf("GET /repos/%s/git/trees/%s returned 404", repository, commit)
	}
	return tree, nil
}

func (f *fakeGitHub) CommitFiles(_ context.Context, repository, commit string, paths []string) (catalog.FileBatch, error) {
	f.fileQueries++
	b := catalog.FileBatch{Files: map[string]catalog.CommitFile{}, Requests: 1, Cost: f.cost, Priced: f.cost > 0}
	for _, p := range paths {
		text, ok := f.files[repository+"@"+commit][p]
		if !ok {
			b.Files[p] = catalog.CommitFile{Unread: "GitHub holds no file at " + p}
			continue
		}
		b.Files[p] = catalog.CommitFile{Text: text}
	}
	return b, nil
}

// The independent reading is a YAML walk of its own and GitHub's parsing of a `uses:` and of an
// action's metadata, so it shares nothing with the CodeQL extraction it is compared with.
func TestTheIndependentReaderReadsEveryDeclaringLocation(t *testing.T) {
	got, unread := readDeclared(map[string]string{
		".github/workflows/ci.yml": `on: push
jobs:
  call:
    uses: octo/shared/.github/workflows/build.yml@v2
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: echo hi
      - uses: ./.github/actions/local
      - uses: docker://alpine:3
      - uses: actions/checkout@v4
`,
		"action.yml": `name: composite
runs:
  using: composite
  steps:
    - uses: actions/cache/save@v4
`,
		"node/action.yaml": "name: node\nruns:\n  using: node20\n  main: index.js\n",
	})
	if len(unread) != 0 {
		t.Fatalf("unread %v", unread)
	}
	want := []declared{
		{file: ".github/workflows/ci.yml", kind: store.EdgeKindJobUsesReusableWorkflow,
			raw:    "octo/shared/.github/workflows/build.yml@v2",
			target: "octo/shared/.github/workflows/build.yml", ref: "v2"},
		{file: ".github/workflows/ci.yml", kind: store.EdgeKindStepUsesAction, raw: "./.github/actions/local"},
		{file: ".github/workflows/ci.yml", kind: store.EdgeKindStepUsesAction,
			raw: "actions/checkout@v4", target: "actions/checkout", ref: "v4"},
		{file: ".github/workflows/ci.yml", kind: store.EdgeKindStepUsesAction,
			raw: "actions/checkout@v4", target: "actions/checkout", ref: "v4"},
		{file: ".github/workflows/ci.yml", kind: store.EdgeKindStepUsesAction, raw: "docker://alpine:3"},
		{file: "action.yml", kind: store.EdgeKindCompositeStepUsesAction, raw: "actions/cache/save@v4",
			target: "actions/cache/save", ref: "v4"},
	}
	if !slices.Equal(sortedDeclared(got), sortedDeclared(want)) {
		t.Errorf("read\n%v\nwant\n%v", sortedDeclared(got), sortedDeclared(want))
	}
}

// Check 1 reads what the extractor indexes by default, not what ABOM fetches, so a sparse filter
// that drops a file the extractor would read shows as a declaration ABOM does not record.
func TestOnlyTheFilesTheExtractorIndexesByDefaultAreRead(t *testing.T) {
	for path, want := range map[string]bool{
		".github/workflows/ci.yml": true, ".github/workflows/ci.yaml": true,
		".github/workflows/nested/ci.yml": false, ".github/workflows/README.md": false,
		".github/reusable_workflows/octo/build.yml": true,
		"action.yml": true, "deep/inside/action.yaml": true,
		"action.yml.orig": false, "docs/ci.yml": false, "actions.lock": false,
	} {
		if got := indexed(path); got != want {
			t.Errorf("indexed(%q) = %v, want %v", path, got, want)
		}
	}
}

// A file the reader cannot parse says nothing either way, so it leaves the commit unread rather
// than compared as if it declared nothing.
func TestAFileTheReaderCannotParseIsUnread(t *testing.T) {
	_, unread := readDeclared(map[string]string{
		".github/workflows/ci.yml": "jobs: [unclosed\n",
		"action.yml":               "name: a\nruns: &r\n  using: composite\n",
	})
	if len(unread) != 2 {
		t.Errorf("unread %v, want both files", unread)
	}
}

func TestComparingDeclarations(t *testing.T) {
	checkout := declared{file: "ci.yml", kind: store.EdgeKindStepUsesAction, raw: "actions/checkout@v4",
		target: "actions/checkout", ref: "v4"}
	cache := declared{file: "action.yml", kind: store.EdgeKindCompositeStepUsesAction,
		raw: "actions/cache@v4", target: "actions/cache", ref: "v4"}
	local := declared{file: "ci.yml", kind: store.EdgeKindStepUsesAction, raw: "./"}
	misread := checkout
	misread.target, misread.ref = "actions/check-out", "v4"
	for _, c := range []struct {
		name          string
		ours, theirs  []declared
		class         string
		claim, theirS string
	}{
		{name: "every declaration matched, a local one by its value alone",
			ours:   []declared{checkout, cache, {file: "ci.yml", kind: local.kind, raw: "./", target: "octo/app"}},
			theirs: []declared{cache, local, checkout}},
		{name: "a declaration ABOM does not record is extraction",
			ours: []declared{checkout}, theirs: []declared{checkout, cache}, class: "extraction",
			claim:  "records nothing for action.yml: actions/cache@v4",
			theirS: "action.yml declares actions/cache@v4"},
		{name: "one written twice and recorded once is extraction",
			ours: []declared{checkout}, theirs: []declared{checkout, checkout}, class: "extraction",
			claim: "records nothing for ci.yml: actions/checkout@v4", theirS: "ci.yml declares"},
		{name: "a declaration the files do not hold is extraction",
			ours: []declared{checkout, cache}, theirs: []declared{checkout}, class: "extraction",
			claim: "records action.yml: actions/cache@v4", theirS: "action.yml declares no actions/cache@v4"},
		{name: "the same value read as another target is normalization",
			ours: []declared{misread}, theirs: []declared{checkout}, class: "normalization",
			claim:  "reads ci.yml: actions/checkout@v4 as actions/check-out@v4",
			theirS: "GitHub's parser reads it as actions/checkout@v4"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := compareDeclared(c.ours, c.theirs)
			if got.class != c.class {
				t.Fatalf("class %q, want %q (%+v)", got.class, c.class, got)
			}
			if !strings.Contains(got.claim, c.claim) || !strings.Contains(got.independent, c.theirS) {
				t.Errorf("claim %q / independent %q, want %q / %q", got.claim, got.independent, c.claim, c.theirS)
			}
		})
	}
}

// declaredGitHub is fakeGitHub with commits' trees and files.
func declaredGitHub(files map[string]map[string]string) *fakeGitHub {
	f := &fakeGitHub{refs: map[string]catalog.RefAnswer{}, protected: map[string][]string{}, cost: 1,
		trees: map[string]catalog.Tree{}, files: map[string]map[string]string{}}
	for at, byPath := range files {
		var tree catalog.Tree
		for path := range byPath {
			tree.Entries = append(tree.Entries, catalog.TreeEntry{Path: path, Type: "blob", Mode: "100644"})
		}
		tree.Entries = append(tree.Entries, catalog.TreeEntry{Path: "README.md", Type: "blob", Mode: "100644"})
		f.trees[at], f.files[at] = tree, byPath
	}
	return f
}

// Check 1 over the fixture: octo/app@a1 declares what ABOM recorded, and vendor/act@c1's action
// declares one dependency more than ABOM recorded for it.
func TestCheckOneComparesEachCommitWithItsOwnFiles(t *testing.T) {
	s := fixture(t)
	gh := declaredGitHub(map[string]map[string]string{
		"octo/app@a1": {".github/workflows/ci.yml": "on: push\njobs:\n  build:\n    runs-on: x\n" +
			"    steps:\n      - uses: vendor/act@v1\n"},
		"vendor/act@c1": {"action.yml": "runs:\n  using: composite\n  steps:\n" +
			"    - uses: actions/cache@v4\n    - uses: actions/setup-node@v4\n"},
	})
	c, pools := checkerWithPools(t, s, gh)
	runs := runChecker(t, c)

	owned := partOf(t, runs, 1, "1c owned commits, sampled at random")
	if owned.Compared != 1 || len(owned.Agreed) != 1 || owned.Agreed[0].Subject != "octo/app@a1" {
		t.Errorf("owned part %+v: want octo/app@a1 compared and agreeing", owned)
	}
	external := partOf(t, runs, 1, "1d external commits, sampled at random")
	if external.Compared != 1 || len(external.Disagreements) != 1 {
		t.Fatalf("external part %+v: want one disagreement", external)
	}
	d := external.Disagreements[0]
	if d.Subject != "vendor/act@c1" || d.Class != "extraction" ||
		!strings.Contains(d.Independent, "action.yml declares actions/setup-node@v4") {
		t.Errorf("disagreement %+v", d)
	}
	if got := slices.Sorted(slices.Values(gh.read)); !slices.Equal(got, []string{"octo/app@a1", "vendor/act@c1"}) {
		t.Errorf("read %v, want each sampled commit once", got)
	}
	var graphql work.Spend
	for _, sp := range pools.Drain() {
		if sp.Unit == work.UnitGraphQL {
			graphql.Requests += sp.Requests
		}
	}
	if gh.fileQueries != 2 || graphql.Requests != gh.fileQueries+len(gh.queries) {
		t.Errorf("%d file queries and %d GraphQL requests charged: want each commit's files read in "+
			"one query, charged beside check 3's %d", gh.fileQueries, graphql.Requests, len(gh.queries))
	}
}

// A tree GitHub would not list whole cannot show that nothing is missing, so the commit is unread.
func TestATruncatedTreeLeavesTheCommitUnread(t *testing.T) {
	s := fixture(t)
	gh := declaredGitHub(map[string]map[string]string{
		"octo/app@a1":   {".github/workflows/ci.yml": "jobs: {}\n"},
		"vendor/act@c1": {"action.yml": "runs:\n  using: composite\n  steps: []\n"},
	})
	tree := gh.trees["vendor/act@c1"]
	tree.Truncated = true
	gh.trees["vendor/act@c1"] = tree
	runs := runChecker(t, checker(t, s, gh))

	external := partOf(t, runs, 1, "1d external commits, sampled at random")
	if external.Compared != 0 || skipped(external)["unread: GitHub listed only part of the tree"] != 1 {
		t.Errorf("external part %+v: want the commit unread", external)
	}
}

func sortedDeclared(ds []declared) []declared {
	out := slices.Clone(ds)
	slices.SortFunc(out, func(a, b declared) int {
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	})
	return out
}
