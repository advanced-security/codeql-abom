// Package normalize resolves raw GitHub Actions `uses:` strings into stable graph identities.
//
// This package deliberately makes NO judgements. Whether a reference is pinned, vulnerable or
// otherwise risky is decided by CodeQL queries and stored as findings attributed to the query
// that produced them. What lives here is only the identity resolution needed to join results
// across repositories, which no single-database query can do.
package normalize

import (
	"regexp"
	"strings"
)

// NodeKind classifies the target of a dependency edge. It is structural, not evaluative: it
// selects which key form the target gets, because a local reference must be repository-scoped
// while a published one must not.
type NodeKind string

const (
	NodeAction           NodeKind = "action"
	NodeLocalAction      NodeKind = "local_action"
	NodeReusableWorkflow NodeKind = "reusable_workflow"
	NodeLocalWorkflow    NodeKind = "local_workflow"
	NodeDockerImage      NodeKind = "docker_image"
)

// Ref is a `uses:` reference decomposed into its identity components.
//
// Every field is either raw extraction or a structural split of it. Nothing here asserts
// anything about the reference's safety.
type Ref struct {
	Raw        string
	Owner      string
	Repo       string
	Subpath    string
	RefValue   string
	NodeKind   NodeKind
	IsExternal bool

	// PinComment is the trailing YAML comment (`# v4.2.2`) as extracted by abom/edges, with
	// the marker and padding removed. It is reported, never interpreted.
	PinComment string
}

var (
	// Reusable workflows are referenced by a path to a YAML file, not a directory.
	workflowPattern = regexp.MustCompile(`(?i)\.ya?ml$`)
	versionPattern  = regexp.MustCompile(`^v?\d+(\.\d+){0,2}([-+][0-9A-Za-z.-]+)?$`)
)

// NodeKeyIn returns the canonical, stable identity of the reference target as seen from
// repository.
//
// The key deliberately excludes the ref so that every version of an action collapses onto one
// graph node; version detail lives on the edge. Local references are qualified by repository
// because `./.github/actions/build` names a different action in every repository, and they
// reuse the container key helpers so a reference and its definition resolve to one node.
func (r Ref) NodeKeyIn(repository string) string {
	switch r.NodeKind {
	case NodeDockerImage:
		return "docker://" + strings.ToLower(r.Raw[len("docker://"):])
	case NodeLocalAction:
		return CompositeActionKey(repository, r.Subpath)
	case NodeLocalWorkflow:
		return WorkflowKey(repository, r.Subpath)
	default:
		key := strings.ToLower(r.Owner + "/" + r.Repo)
		if r.Subpath != "" {
			key += "/" + strings.ToLower(r.Subpath)
		}
		return key
	}
}

// ParseUses decomposes a raw `uses:` value and its optional trailing comment into identity
// components.
//
// It never returns an error: an unrecognized reference still yields a usable Ref so that a
// single malformed workflow cannot drop an entire repository from the graph.
func ParseUses(raw, pinComment string) Ref {
	raw = strings.TrimSpace(raw)
	r := Ref{Raw: raw, PinComment: normalizeComment(pinComment)}

	switch {
	case strings.HasPrefix(raw, "docker://"):
		r.NodeKind = NodeDockerImage
		r.IsExternal = true
		image := raw[len("docker://"):]
		if tag, ok := splitDockerTag(image); ok {
			r.RefValue = tag
		}
		return r

	case strings.HasPrefix(raw, "./"), strings.HasPrefix(raw, "../"):
		r.Subpath = strings.TrimPrefix(strings.TrimPrefix(raw, "./"), "../")
		if workflowPattern.MatchString(r.Subpath) {
			r.NodeKind = NodeLocalWorkflow
		} else {
			r.NodeKind = NodeLocalAction
		}
		return r
	}

	r.IsExternal = true
	path, ref, _ := strings.Cut(raw, "@")
	r.RefValue = ref

	segments := strings.Split(path, "/")
	if len(segments) >= 1 {
		r.Owner = segments[0]
	}
	if len(segments) >= 2 {
		r.Repo = segments[1]
	}
	if len(segments) > 2 {
		r.Subpath = strings.Join(segments[2:], "/")
	}

	if workflowPattern.MatchString(r.Subpath) {
		r.NodeKind = NodeReusableWorkflow
	} else {
		r.NodeKind = NodeAction
	}
	return r
}

// normalizeComment reduces a trailing comment to a bare version string, or "" when the comment
// carries no version (for example `# renovate: pinned`).
func normalizeComment(comment string) string {
	comment = strings.TrimSpace(strings.TrimLeft(comment, "# \t"))
	if comment == "" {
		return ""
	}
	if field, _, _ := strings.Cut(comment, " "); versionPattern.MatchString(field) {
		return field
	}
	return ""
}

// splitDockerTag separates the tag from a docker image reference, ignoring the colon inside a
// host:port prefix.
func splitDockerTag(image string) (string, bool) {
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return image[colon+1:], true
	}
	return "", false
}
