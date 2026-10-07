package setup

import (
	"fmt"
	"slices"

	"github.com/advanced-security/codeql-abom/internal/store"
)

// PatchBody is what a write sends to default setup.
type PatchBody struct {
	State       string   `json:"state"`
	Languages   []string `json:"languages"`
	QuerySuite  string   `json:"query_suite,omitempty"`
	ThreatModel string   `json:"threat_model,omitempty"`
	RunnerType  string   `json:"runner_type,omitempty"`
	RunnerLabel string   `json:"runner_label,omitempty"`
}

// Body is what a write sends for a repository in class c. A fresh enablement is actions alone, at
// the extended suite and the remote_and_local threat model; anything already configured keeps every
// setting it was read with and gains actions. Every other class is never written.
func Body(c store.SetupClass) (PatchBody, error) {
	switch c.Class {
	case store.SetupEnable:
		return PatchBody{State: "configured", Languages: []string{"actions"}, QuerySuite: "extended",
			ThreatModel: "remote_and_local"}, nil
	case store.SetupAdd:
	default:
		return PatchBody{}, fmt.Errorf("%s is never written", c.Class)
	}
	languages := WriteNames(append(slices.Clone(c.Languages), "actions"))
	for _, l := range languages {
		if !slices.Contains(store.SetupWriteLanguages, l) {
			return PatchBody{}, fmt.Errorf("default setup analyzes %s here, which GitHub's write refuses, "+
				"so actions cannot be added without removing it", l)
		}
	}
	body := PatchBody{State: "configured", Languages: languages, QuerySuite: c.QuerySuite, ThreatModel: c.ThreatModel,
		RunnerType: c.RunnerType}
	if c.RunnerType == "labeled" {
		body.RunnerLabel = c.RunnerLabel
	}
	return body, nil
}

// WriteNames is a language list as the write names it: a read reports javascript and typescript
// beside javascript-typescript, and all three are the one analysis the write calls the last.
func WriteNames(languages []string) []string {
	out := slices.Clone(languages)
	if slices.Contains(out, "javascript-typescript") {
		out = slices.DeleteFunc(out, func(l string) bool { return l == "javascript" || l == "typescript" })
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Removes lists the languages before held that after does not, compared as the write names them.
func Removes(before, after []string) []string {
	kept := WriteNames(after)
	out := []string{}
	for _, l := range WriteNames(before) {
		if !slices.Contains(kept, l) {
			out = append(out, l)
		}
	}
	return out
}
