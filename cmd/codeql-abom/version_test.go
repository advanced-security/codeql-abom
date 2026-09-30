package main

import (
	"context"
	"encoding/json"
	"runtime/debug"
	"strings"
	"testing"
)

func buildWith(settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{GoVersion: "go1.26.0", Settings: settings}
}

// A build nobody stamped is a development build, and saying a version it was never given would
// let a checkout pass for a release.
func TestAnUnstampedBuildSaysItIsADevelopmentBuild(t *testing.T) {
	id := identityFrom(buildWith(), true, "", "2.27.1")

	if !id.Development || id.Version != "" {
		t.Errorf("identity = %+v, want a development build with no version", id)
	}
	if got := id.String(); !strings.Contains(got, "development build") {
		t.Errorf("String() = %q, want it to say development build", got)
	}
}

func TestAStampedBuildReportsItsVersionAndCodeQL(t *testing.T) {
	id := identityFrom(buildWith(
		debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
		debug.BuildSetting{Key: "vcs.modified", Value: "false"},
		debug.BuildSetting{Key: "GOOS", Value: "linux"},
		debug.BuildSetting{Key: "GOARCH", Value: "arm64"},
	), true, "2.27.1-next.1", "2.27.1")

	if id.Development || id.Version != "2.27.1-next.1" || id.CodeQLVersion != "2.27.1" {
		t.Errorf("identity = %+v, want release 2.27.1-next.1 for CodeQL 2.27.1", id)
	}
	if id.Revision != "0123456789abcdef0123456789abcdef01234567" || id.Dirty == nil || *id.Dirty {
		t.Errorf("revision=%q dirty=%v, want the full commit and a clean tree", id.Revision, id.Dirty)
	}
	if id.OS != "linux" || id.Arch != "arm64" {
		t.Errorf("target = %s/%s, want linux/arm64", id.OS, id.Arch)
	}
	if got := id.String(); !strings.Contains(got, "2.27.1-next.1") || !strings.Contains(got, "CodeQL 2.27.1") {
		t.Errorf("String() = %q, want the version and the CodeQL it was built for", got)
	}
}

func TestAModifiedTreeIsReportedAsDirty(t *testing.T) {
	id := identityFrom(buildWith(
		debug.BuildSetting{Key: "vcs.revision", Value: "abc"},
		debug.BuildSetting{Key: "vcs.modified", Value: "true"},
	), true, "2.27.1", "2.27.1")

	if id.Dirty == nil || !*id.Dirty {
		t.Fatalf("dirty = %v, want true", id.Dirty)
	}
	if !strings.Contains(id.String(), "modified") {
		t.Errorf("String() = %q, want it to say the tree was modified", id.String())
	}
}

// A binary built without version control information cannot say whether its tree was clean, and
// false would claim that it was.
func TestAnUnrecordedTreeStateIsNullRatherThanClean(t *testing.T) {
	id := identityFrom(buildWith(), true, "2.27.1", "2.27.1")

	raw, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"dirty":null`) {
		t.Errorf("JSON = %s, want dirty null when nothing recorded it", raw)
	}
}

// The release manifest is checked against this output field by field, so the fields are the
// contract.
func TestVersionJSONCarriesTheFieldsTheReleaseManifestChecks(t *testing.T) {
	var stdout, stderr strings.Builder
	if err := run(context.Background(), []string{"version", "--json"}, &stdout, &stderr); err != nil {
		t.Fatalf("version --json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("decoding %q: %v", stdout.String(), err)
	}
	for _, key := range []string{"version", "development", "revision", "dirty", "os", "arch",
		"goVersion", "codeqlVersion"} {
		if _, ok := got[key]; !ok {
			t.Errorf("version --json has no %q: %s", key, stdout.String())
		}
	}
}
