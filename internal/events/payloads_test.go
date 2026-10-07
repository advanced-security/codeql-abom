package events

import (
	"encoding/json"
	"strings"
)

// Payloads follow GitHub's documented schemas, so no test passes by reading a field GitHub never sends.

func repositoryJSON(fullName string) map[string]any {
	owner, name, _ := strings.Cut(fullName, "/")
	return map[string]any{
		"id":             123456789,
		"node_id":        "R_kgDOExample",
		"name":           name,
		"full_name":      fullName,
		"private":        true,
		"owner":          map[string]any{"login": owner, "type": "Organization"},
		"html_url":       "https://github.com/" + fullName,
		"default_branch": "main",
		"archived":       false,
	}
}

func sender() map[string]any {
	return map[string]any{"login": "octocat", "id": 1, "type": "User"}
}

func encode(v map[string]any) []byte {
	out, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return out
}

const zeroSHA = "0000000000000000000000000000000000000000"

func pushPayload(fullName, ref, before, after string) []byte {
	commit := map[string]any{
		"id":        after,
		"tree_id":   "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
		"distinct":  true,
		"message":   "Update ci.yml",
		"timestamp": "2026-10-04T12:00:00-06:00",
		"url":       "https://github.com/" + fullName + "/commit/" + after,
		"author":    map[string]any{"name": "Octo Cat", "email": "octocat@example.com"},
		"committer": map[string]any{"name": "GitHub", "email": "noreply@github.com"},
		"added":     []string{},
		"removed":   []string{},
		"modified":  []string{".github/workflows/ci.yml"},
	}
	return encode(map[string]any{
		"ref":         ref,
		"before":      before,
		"after":       after,
		"created":     before == zeroSHA,
		"deleted":     after == zeroSHA,
		"forced":      false,
		"base_ref":    nil,
		"compare":     "https://github.com/" + fullName + "/compare/" + before[:12] + "..." + after[:12],
		"commits":     []any{commit},
		"head_commit": commit,
		"repository":  repositoryJSON(fullName),
		"pusher":      map[string]any{"name": "octocat", "email": "octocat@example.com"},
		"sender":      sender(),
	})
}

// refPayload is a create or delete event, which name the ref without its refs/heads/ prefix.
func refPayload(fullName, ref, refType string) []byte {
	return encode(map[string]any{
		"ref":         ref,
		"ref_type":    refType,
		"pusher_type": "user",
		"repository":  repositoryJSON(fullName),
		"sender":      sender(),
	})
}

func protectionRulePayload(fullName, action, branch string) []byte {
	return encode(map[string]any{
		"action": action,
		"rule": map[string]any{
			"id": 21796960, "repository_id": 123456789, "name": branch,
			"pull_request_reviews_enforcement_level":   "non_admins",
			"required_status_checks_enforcement_level": "non_admins",
		},
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

// rulesetPayload is a repository ruleset event; an organization's ruleset carries no repository.
func rulesetPayload(fullName, org string) []byte {
	payload := map[string]any{
		"action": "edited",
		"repository_ruleset": map[string]any{
			"id": 42, "name": "protect release", "target": "branch", "enforcement": "active",
			"source_type": "Repository", "source": fullName,
		},
		"organization": map[string]any{"login": org},
		"sender":       sender(),
	}
	if fullName == "" {
		payload["repository_ruleset"].(map[string]any)["source_type"] = "Organization"
		payload["repository_ruleset"].(map[string]any)["source"] = org
	} else {
		payload["repository"] = repositoryJSON(fullName)
	}
	return encode(payload)
}

func repositoryPayload(fullName, action string) []byte {
	return encode(map[string]any{
		"action":     action,
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

func installationPayload(action, account string) []byte {
	return encode(map[string]any{
		"action": action,
		"installation": map[string]any{
			"id": 1, "account": map[string]any{"login": account, "type": "Organization"},
			"repository_selection": "selected",
		},
		"repositories": []any{map[string]any{"full_name": account + "/service", "private": true}},
		"sender":       sender(),
	})
}

func pingPayload() []byte {
	return encode(map[string]any{
		"zen":     "Design for failure.",
		"hook_id": 1,
		"hook":    map[string]any{"type": "Repository", "events": []string{"push"}, "active": true},
	})
}

func repositoryRenamedPayload(fullName, from string) []byte {
	return encode(map[string]any{
		"action":     "renamed",
		"changes":    map[string]any{"repository": map[string]any{"name": map[string]any{"from": from}}},
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

func repositoryTransferredPayload(fullName, fromOrganization string) []byte {
	return encode(map[string]any{
		"action": "transferred",
		"changes": map[string]any{"owner": map[string]any{"from": map[string]any{
			"organization": map[string]any{"login": fromOrganization, "id": 2}}}},
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

func propertyValuesPayload(fullName, organization string) []byte {
	return encode(map[string]any{
		"action":              "updated",
		"repository":          repositoryJSON(fullName),
		"organization":        map[string]any{"login": organization},
		"new_property_values": []any{map[string]any{"property_name": "team", "value": "red"}},
		"old_property_values": []any{map[string]any{"property_name": "team", "value": nil}},
		"sender":              sender(),
	})
}

func propertyDefinitionPayload(action, organization string) []byte {
	return encode(map[string]any{
		"action": action,
		"definition": map[string]any{"property_name": "team", "value_type": "single_select",
			"required": false, "default_value": "blue", "allowed_values": []string{"red", "blue"}},
		"organization": map[string]any{"login": organization},
		"sender":       sender(),
	})
}

func installationTargetPayload(login, from string) []byte {
	return encode(map[string]any{
		"action":       "renamed",
		"account":      map[string]any{"login": login, "id": 2, "type": "Organization"},
		"changes":      map[string]any{"login": map[string]any{"from": from}},
		"target_type":  "Organization",
		"installation": map[string]any{"id": 1, "node_id": "MDIzOkludGVncmF0aW9uSW5zdGFsbGF0aW9uMQ=="},
		"sender":       sender(),
	})
}

func advisoryPayload(action string, ecosystems ...string) []byte {
	vulnerabilities := []any{}
	for _, ecosystem := range ecosystems {
		vulnerabilities = append(vulnerabilities, map[string]any{
			"package":                  map[string]any{"ecosystem": ecosystem, "name": "acme/action"},
			"severity":                 "high",
			"vulnerable_version_range": "< 2.0.0",
			"first_patched_version":    map[string]any{"identifier": "2.0.0"},
		})
	}
	return encode(map[string]any{
		"action": action,
		"security_advisory": map[string]any{
			"ghsa_id": "GHSA-abcd-efgh-ijkl", "summary": "Code injection", "severity": "high",
			"vulnerabilities": vulnerabilities,
		},
	})
}

// securityAndAnalysisPayload names no action: GitHub sends the change and the repository.
func securityAndAnalysisPayload(fullName string) []byte {
	return encode(map[string]any{
		"changes": map[string]any{"from": map[string]any{"security_and_analysis": map[string]any{
			"code_security": map[string]any{"status": "disabled"}}}},
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

func workflowRunPayload(fullName, action, path, headSHA string) []byte {
	status, conclusion := "in_progress", any(nil)
	if action == "completed" {
		status, conclusion = "completed", "success"
	}
	return encode(map[string]any{
		"action": action,
		"workflow_run": map[string]any{
			"id": 30433642, "run_attempt": 2, "name": "CI", "path": path,
			"head_branch": "main", "head_sha": headSHA, "event": "push",
			"status": status, "conclusion": conclusion,
			"run_started_at": "2026-10-04T18:00:00Z", "updated_at": "2026-10-04T18:03:00Z",
			"referenced_workflows": []any{map[string]any{
				"path": "acme/shared/.github/workflows/build.yml@v1", "sha": shaC, "ref": "refs/tags/v1"}},
		},
		"workflow":   map[string]any{"id": 1, "path": path},
		"repository": repositoryJSON(fullName),
		"sender":     sender(),
	})
}

func metaPayload() []byte {
	return encode(map[string]any{
		"action":  "deleted",
		"hook_id": 554188,
		"hook":    map[string]any{"type": "App", "id": 554188, "active": true, "events": []string{"push"}},
	})
}
