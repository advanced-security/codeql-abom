package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// Version reports the CodeQL CLI version, asked once and remembered.
//
// It belongs in the identity of every analysis: extraction and evaluation both change between
// releases, so the same query against the same commit can legitimately differ across CLI
// versions. Reading `.codeql-version` instead would record what is pinned rather than what ran.
func (r *Runner) Version(ctx context.Context) (string, error) {
	r.versionOnce.Do(func() {
		out, err := r.output(ctx, "version", "--format=json")
		if err != nil {
			r.versionErr = fmt.Errorf("codeql version: %w", err)
			return
		}
		var v struct {
			SemanticVersion string `json:"semanticVersion"`
			Version         string `json:"version"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &v); err != nil {
			r.versionErr = fmt.Errorf("reading the CodeQL version: %w", err)
			return
		}
		r.version = v.SemanticVersion
		if r.version == "" {
			r.version = v.Version
		}
		if r.version == "" {
			r.versionErr = fmt.Errorf("codeql version reported no version")
		}
	})
	return r.version, r.versionErr
}

// versionState is embedded in Runner so the identity concern stays in one file.
type versionState struct {
	versionOnce sync.Once
	version     string
	versionErr  error
}
