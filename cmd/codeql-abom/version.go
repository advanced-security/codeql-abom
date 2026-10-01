package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
)

// Stamped at link time with -X by `make build` and the release workflow. A build that was not
// given a version is a development build, whatever commit it came from.
var (
	version       string
	codeqlVersion string
)

// buildIdentity is what this executable is. A release archive's build-info.json must agree with
// it field for field, so the JSON names are that manifest's contract.
type buildIdentity struct {
	Version     string `json:"version"`
	Development bool   `json:"development"`
	// Revision is empty when the build recorded no version control information.
	Revision string `json:"revision"`
	// Dirty is null, not false, when nothing recorded whether the tree was modified.
	Dirty     *bool  `json:"dirty"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"goVersion"`
	// CodeQLVersion is the CLI version this build was made against, from .codeql-version.
	CodeQLVersion string `json:"codeqlVersion"`
}

func identityFrom(info *debug.BuildInfo, ok bool, stamped, codeql string) buildIdentity {
	id := buildIdentity{
		Version:       stamped,
		Development:   stamped == "",
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		GoVersion:     runtime.Version(),
		CodeQLVersion: codeql,
	}
	if !ok || info == nil {
		return id
	}
	id.GoVersion = info.GoVersion
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			id.Revision = s.Value
		case "vcs.modified":
			dirty := s.Value == "true"
			id.Dirty = &dirty
		case "GOOS":
			id.OS = s.Value
		case "GOARCH":
			id.Arch = s.Value
		}
	}
	return id
}

func (id buildIdentity) String() string {
	name := "codeql-abom " + id.Version
	if id.Development {
		name = "codeql-abom development build"
	}
	revision := "revision not recorded"
	if id.Revision != "" {
		revision = id.Revision
		if len(revision) > 12 {
			revision = revision[:12]
		}
		if id.Dirty != nil && *id.Dirty {
			revision += ", modified"
		}
	}
	codeql := "CodeQL version not recorded"
	if id.CodeQLVersion != "" {
		codeql = "CodeQL " + id.CodeQLVersion
	}
	return fmt.Sprintf("%s (%s, %s/%s, %s) for %s",
		name, revision, id.OS, id.Arch, id.GoVersion, codeql)
}

func runVersion(_ context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the identity as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info, ok := debug.ReadBuildInfo()
	id := identityFrom(info, ok, version, codeqlVersion)
	if !*asJSON {
		_, err := fmt.Fprintln(stdout, id.String())
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(id)
}
