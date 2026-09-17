package normalize

import (
	"regexp"
	"strings"
)

// actionsPurlType is the package-url type GitHub's dependency graph uses for GitHub Actions.
const actionsPurlType = "githubactions"

// commitSHA matches a full git object name, which GitHub carries into a package version verbatim.
var commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// semverish matches a reference of the form v1, v1.2 or v1.2.3, which is how actions are tagged.
var semverish = regexp.MustCompile(`^[vV](\d+)(?:\.(\d+))?(?:\.(\d+))?$`)

// PackageURL returns the package-url GitHub's dependency graph uses for an Actions dependency, or
// "" for a reference GitHub does not represent as a package.
//
// The form and every conversion below were read out of GitHub's own SBOM exports rather than
// derived, because a purl ABOM invents does not merge with the package already in the graph — it
// lands beside it as a duplicate nothing consumes, behind a submission that still returns 201.
//
// `subpath` is the directory within the repository for an action like `github/codeql-action/init`.
// GitHub keeps it in the package *name*, not in purl's subpath component.
func PackageURL(owner, repo, subpath, ref string) string {
	version := packageVersion(ref)
	if owner == "" || repo == "" || version == "" {
		// Half a coordinate is not a package. `pkg:githubactions//@v1` would assert a
		// dependency on an account with no name.
		return ""
	}
	if strings.ContainsAny(owner, "./$") || strings.Contains(repo, "..") {
		// A reference the crawl could not resolve, such as `${{ github.repository }}/setup`, is an
		// unevaluated expression rather than a dependency. See plans/db/unresolved-references.md.
		return ""
	}

	name := owner + "/" + repo
	if subpath != "" {
		name += "/" + strings.Trim(subpath, "/")
	}
	return "pkg:" + actionsPurlType + "/" + name + "@" + version
}

// packageVersion converts a `uses:` reference into the version GitHub records, or "" when GitHub
// records no package at all.
//
// A local action (`./…`) and a container action (`docker://…`) are both real dependencies that
// GitHub does not represent. Emitting a `githubactions` package for either would put something in
// the graph that no other producer will ever reconcile against.
func packageVersion(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case ref == "", strings.HasPrefix(ref, "./"), strings.HasPrefix(ref, "docker://"):
		return ""
	case commitSHA.MatchString(ref):
		return ref
	}

	m := semverish.FindStringSubmatch(ref)
	if m == nil {
		// A branch or an arbitrary tag. GitHub's spelling for these is unmeasured, and guessing
		// would reintroduce the duplicate-package failure this function exists to avoid.
		return ""
	}
	// Strip the `v`, then pad the components the reference omitted. `@v4` covers every 4.x
	// release, which GitHub records as the range `4.*.*`.
	parts := []string{m[1], m[2], m[3]}
	for i, p := range parts {
		if p == "" {
			parts[i] = "%2A" // `*`, percent-encoded as GitHub encodes it
		}
	}
	return strings.Join(parts, ".")
}
