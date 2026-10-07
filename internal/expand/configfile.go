package expand

import (
	"context"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Why default setup's work could not answer for every file, beside the database's and analysis's reasons.
const (
	refusedConfigFile   = "a configuration file narrows its paths"
	refusedConfigUnread = "configuration file not readable"
)

// configMemoFor bounds how long one file's answer stands in for reading it again: an organization's
// repositories usually share one, and a round would otherwise ask once per repository.
const configMemoFor = 5 * time.Minute

type configMemo struct {
	mu      sync.Mutex
	answers map[string]configAnswer
}

type configAnswer struct {
	refusal string
	at      time.Time
}

// configFileRefusal says why default setup's work at commit cannot answer for every file ABOM
// reads, or nothing. `paths` and `paths-ignore` narrow what default setup extracts, and its
// analysis records neither, so the file itself has to be read.
func (e *Expander) configFileRefusal(ctx context.Context, repository, commit string) string {
	value, err := e.Store.CodeQLConfigFile(repository)
	if err != nil {
		return refusedConfigUnread
	}
	if value == "" {
		return ""
	}
	repo, path, ref := configReferenceOf(value, repository, commit)
	key := repo + "|" + path + "|" + ref
	if refusal, ok := e.configs.lookup(key); ok {
		return refusal
	}
	refusal := refusedConfigUnread
	if body, err := e.CodeScanning.FileContent(ctx, repo, path, ref); err == nil {
		if narrows, err := narrowsPaths(body); err == nil {
			refusal = ""
			if narrows {
				refusal = refusedConfigFile
			}
		}
	}
	if ctx.Err() == nil {
		e.configs.remember(key, refusal)
	}
	return refusal
}

// configReferenceOf reads `remote=[<owner>/]<repository>[@<ref>][:<file-path>]` with GitHub's
// defaults, or a path in the repository itself at the commit being read.
func configReferenceOf(value, repository, commit string) (repo, path, ref string) {
	rest, remote := strings.CutPrefix(value, "remote=")
	if !remote {
		return repository, strings.TrimPrefix(value, "/"), commit
	}
	named, path, _ := strings.Cut(rest, ":")
	named, ref, _ = strings.Cut(named, "@")
	if !strings.Contains(named, "/") {
		owner, _, _ := strings.Cut(repository, "/")
		named = owner + "/" + named
	}
	return named, cmpOr(path, ".github/codeql-config.yml"), cmpOr(ref, "main")
}

// narrowsPaths reports whether a configuration file names paths to read or to ignore.
func narrowsPaths(body []byte) (bool, error) {
	var config map[string]any
	if err := yaml.Unmarshal(body, &config); err != nil {
		return false, err
	}
	for _, key := range []string{"paths", "paths-ignore"} {
		if v, ok := config[key]; ok && v != nil {
			return true, nil
		}
	}
	return false, nil
}

func (m *configMemo) lookup(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.answers[key]
	if !ok || time.Since(a.at) > configMemoFor {
		return "", false
	}
	return a.refusal, true
}

func (m *configMemo) remember(key, refusal string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.answers == nil {
		m.answers = map[string]configAnswer{}
	}
	m.answers[key] = configAnswer{refusal: refusal, at: time.Now()}
}

func cmpOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
