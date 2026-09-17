package normalize

import (
	"strings"
	"testing"
)

// ParseUses is identity resolution only. It must decompose a reference correctly and assert
// nothing about whether that reference is safe, pinned, or current — those are query results.
func TestParseUses(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		pinComment string
		want       Ref
	}{
		{
			name:       "sha reference with version comment",
			raw:        "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
			pinComment: "v4.2.2",
			want: Ref{
				Owner: "actions", Repo: "checkout",
				RefValue:   "11bd71901bbe5b1630ceea73d27597364c9af683",
				NodeKind:   NodeAction,
				IsExternal: true, PinComment: "v4.2.2",
			},
		},
		{
			name: "major tag",
			raw:  "actions/setup-node@v4",
			want: Ref{
				Owner: "actions", Repo: "setup-node", RefValue: "v4",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "full semver tag",
			raw:  "actions/upload-artifact@v4.6.2",
			want: Ref{
				Owner: "actions", Repo: "upload-artifact", RefValue: "v4.6.2",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "branch reference",
			raw:  "actions/stale@main",
			want: Ref{
				Owner: "actions", Repo: "stale", RefValue: "main",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "no reference at all",
			raw:  "actions/labeler",
			want: Ref{
				Owner: "actions", Repo: "labeler",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "subpath action",
			raw:  "github/codeql-action/init@v3",
			want: Ref{
				Owner: "github", Repo: "codeql-action", Subpath: "init", RefValue: "v3",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "local composite action",
			raw:  "./.github/actions/greet",
			want: Ref{Subpath: ".github/actions/greet", NodeKind: NodeLocalAction},
		},
		{
			name: "local reusable workflow",
			raw:  "./.github/workflows/reusable.yml",
			want: Ref{Subpath: ".github/workflows/reusable.yml", NodeKind: NodeLocalWorkflow},
		},
		{
			name: "self reference to an action in the same repository",
			raw:  "$/.github/actions/ci-setup",
			want: Ref{Subpath: ".github/actions/ci-setup", NodeKind: NodeLocalAction},
		},
		{
			name: "self reference to the repository's own root action",
			raw:  "$/",
			want: Ref{NodeKind: NodeLocalAction},
		},
		{
			name: "self reference to a reusable workflow in the same repository",
			raw:  "$/.github/workflows/release.yml",
			want: Ref{Subpath: ".github/workflows/release.yml", NodeKind: NodeLocalWorkflow},
		},
		{
			name: "remote reusable workflow",
			raw:  "octo-org/shared/.github/workflows/build.yml@v1",
			want: Ref{
				Owner: "octo-org", Repo: "shared", Subpath: ".github/workflows/build.yml",
				RefValue: "v1", NodeKind: NodeReusableWorkflow, IsExternal: true,
			},
		},
		{
			name: "docker image with tag",
			raw:  "docker://alpine:3.20",
			want: Ref{RefValue: "3.20", NodeKind: NodeDockerImage, IsExternal: true},
		},
		{
			name: "docker image with registry port and no tag",
			raw:  "docker://registry.local:5000/tools/build",
			want: Ref{NodeKind: NodeDockerImage, IsExternal: true},
		},
		{
			name:       "hash prefix and padding stripped from comment",
			raw:        "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
			pinComment: "#   v4.2.2  ",
			want: Ref{
				Owner: "actions", Repo: "checkout",
				RefValue:   "11bd71901bbe5b1630ceea73d27597364c9af683",
				NodeKind:   NodeAction,
				IsExternal: true, PinComment: "v4.2.2",
			},
		},
		{
			name:       "non-version comment is discarded",
			raw:        "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683",
			pinComment: "renovate: pinned by policy",
			want: Ref{
				Owner: "actions", Repo: "checkout",
				RefValue: "11bd71901bbe5b1630ceea73d27597364c9af683",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
		{
			name: "slash-bearing branch reference",
			raw:  "actions/stale@feature/new-thing",
			want: Ref{
				Owner: "actions", Repo: "stale", RefValue: "feature/new-thing",
				NodeKind: NodeAction, IsExternal: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.want.Raw = tt.raw
			got := ParseUses(tt.raw, tt.pinComment)
			if got != tt.want {
				t.Errorf("ParseUses(%q, %q)\n got: %+v\nwant: %+v", tt.raw, tt.pinComment, got, tt.want)
			}
		})
	}
}

// Reference shape must not influence identity: every version of an action is one graph node.
func TestNodeKeyIsStableAcrossVersions(t *testing.T) {
	v4 := ParseUses("actions/checkout@v4", "").NodeKeyIn("o/r")
	sha := ParseUses("actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683", "v4.2.2").
		NodeKeyIn("o/r")
	if v4 != sha {
		t.Errorf("versions of one action must share a node key: %q != %q", v4, sha)
	}
}

func TestNodeKey(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"actions/checkout@v4", "actions/checkout"},
		{"ACTIONS/CheckOut@v4", "actions/checkout"},
		{"github/codeql-action/init@v3", "github/codeql-action/init"},
		{"octo-org/shared/.github/workflows/build.yml@v1", "octo-org/shared/.github/workflows/build.yml"},
		{"./.github/actions/greet", "octo-org/app/.github/actions/greet"},
		{"./.github/workflows/reusable.yml", "octo-org/app/.github/workflows/reusable.yml"},
		{"$/.github/actions/greet", "octo-org/app/.github/actions/greet"},
		{"$/.github/workflows/reusable.yml", "octo-org/app/.github/workflows/reusable.yml"},
		{"$/", "octo-org/app"},
		{"docker://Alpine:3.20", "docker://alpine:3.20"},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := ParseUses(tt.raw, "").NodeKeyIn("octo-org/app"); got != tt.want {
				t.Errorf("NodeKeyIn(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// Subpaths distinguish sibling actions that share a repository, so they must not collapse.
func TestNodeKeyDistinguishesSubpaths(t *testing.T) {
	init := ParseUses("github/codeql-action/init@v3", "").NodeKeyIn("o/r")
	analyze := ParseUses("github/codeql-action/analyze@v3", "").NodeKeyIn("o/r")
	if init == analyze {
		t.Errorf("sibling subpath actions collapsed onto one key: %q", init)
	}
}

// `$/` is GitHub's self repository reference: the repository of the file it appears in, at the
// running commit. It is fully resolved, and reading the leading `$` as an account name states
// that the organization depends on a third party called `$` — a verdict from ABOM rather than
// from a query. See plans/db/unresolved-references.md.
func TestASelfReferenceNamesTheContainingRepository(t *testing.T) {
	for _, raw := range []string{"$/", "$/setup", "$/.github/actions/ci-setup"} {
		ref := ParseUses(raw, "")
		if ref.Owner != "" || ref.Repo != "" {
			t.Errorf("ParseUses(%q) took %q/%q as an account: `$` is not an owner",
				raw, ref.Owner, ref.Repo)
		}
		if ref.IsExternal {
			t.Errorf("ParseUses(%q) is external: a self reference never leaves its repository", raw)
		}
		if key := ref.NodeKeyIn("changesets/action"); !strings.HasPrefix(key, "changesets/action") {
			t.Errorf("NodeKeyIn(%q) = %q, want it under the containing repository", raw, key)
		}
	}
}

// `$/x` and `./x` name the same file. Their only difference is what they resolve against at run
// time, which is not identity, so one action must not become two nodes.
func TestASelfReferenceAndARelativePathAreOneNode(t *testing.T) {
	self := ParseUses("$/.github/actions/greet", "").NodeKeyIn("octo-org/app")
	relative := ParseUses("./.github/actions/greet", "").NodeKeyIn("octo-org/app")
	if self != relative {
		t.Errorf("one action, two keys: %q != %q", self, relative)
	}
}
