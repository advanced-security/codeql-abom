// Package submit projects ABOM's dependency closure into GitHub's dependency submission format.
//
// A submission that names a path GitHub analyzes replaces GitHub's reading of that path, so ABOM
// submits only what GitHub cannot see, under a path of its own.
package submit

import (
	"github.com/advanced-security/codeql-abom/internal/normalize"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// SyntheticManifestPath is deliberately not a file in the repository. GitHub's static analysis
// never claims it, which is what makes a submission additive instead of destructive.
const SyntheticManifestPath = ".abom/transitive-dependencies.json"

// Correlator and DetectorName are the pair GitHub supersedes on, so they must not vary between
// runs: a drifting correlator leaves the previous submission live forever.
const (
	Correlator   = "abom-transitive-actions"
	DetectorName = "codeql-abom"
	DetectorURL  = "https://github.com/advanced-security/codeql-abom"
)

// Metadata is what a snapshot needs about the repository it describes.
type Metadata struct {
	Repository      string
	SHA             string
	Ref             string
	ScannedAt       string
	DetectorVersion string
	JobID           string
}

type Snapshot struct {
	Version   int                 `json:"version"`
	Job       Job                 `json:"job"`
	SHA       string              `json:"sha"`
	Ref       string              `json:"ref"`
	Detector  Detector            `json:"detector"`
	Scanned   string              `json:"scanned"`
	Manifests map[string]Manifest `json:"manifests"`
}

type Job struct {
	Correlator string `json:"correlator"`
	ID         string `json:"id"`
}

type Detector struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
}

type Manifest struct {
	Name     string              `json:"name"`
	File     ManifestFile        `json:"file"`
	Resolved map[string]Resolved `json:"resolved"`
}

type ManifestFile struct {
	SourceLocation string `json:"source_location"`
}

type Resolved struct {
	PackageURL   string `json:"package_url"`
	Relationship string `json:"relationship"`
	Scope        string `json:"scope"`
}

// Build projects a closure into a snapshot carrying only the dependencies GitHub cannot derive.
//
// A closure with nothing transitive yields no manifests, which is not a no-op: an empty manifests
// object releases a path ABOM previously claimed back to GitHub's static analysis. That is the
// only mechanism that does so — an empty `resolved` inside a manifest removes the packages but
// keeps the path owned, leaving it permanently blank.
func Build(m Metadata, closure []store.ClosureEntry) Snapshot {
	version := m.DetectorVersion
	if version == "" {
		version = "0.0.0"
	}
	jobID := m.JobID
	if jobID == "" {
		jobID = m.SHA
	}

	snap := Snapshot{
		Version:   0,
		Job:       Job{Correlator: Correlator, ID: jobID},
		SHA:       m.SHA,
		Ref:       m.Ref,
		Detector:  Detector{Name: DetectorName, Version: version, URL: DetectorURL},
		Scanned:   m.ScannedAt,
		Manifests: map[string]Manifest{},
	}

	resolved := map[string]Resolved{}
	for _, e := range closure {
		if e.Relationship != "indirect" {
			continue
		}
		// What runs: the commit a lock selects over the tag the workflow wrote, and the verified
		// version of either when there is one. GitHub matches advisories on semantic versions and
		// never on commits, so a pinned reference is unalertable until it can be named.
		ref := e.Ref
		if e.LockedCommit != "" {
			ref = e.LockedCommit
		}
		if e.Version != "" {
			ref = e.Version
		}
		purl := normalize.PackageURL(e.Owner, e.Repo, e.Subpath, ref)
		if purl == "" {
			continue
		}
		resolved[purl] = Resolved{PackageURL: purl, Relationship: "indirect", Scope: "runtime"}
	}
	if len(resolved) == 0 {
		return snap
	}

	snap.Manifests[SyntheticManifestPath] = Manifest{
		Name:     SyntheticManifestPath,
		File:     ManifestFile{SourceLocation: SyntheticManifestPath},
		Resolved: resolved,
	}
	return snap
}
