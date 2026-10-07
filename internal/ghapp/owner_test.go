package ghapp

import "testing"

// A URL names whose token reads it, and whether it reads the account itself, which only that account's installation sees whole.
func TestEachRequestNamesTheAccountWhoseTokenReadsIt(t *testing.T) {
	type subject struct {
		owner   string
		account bool
	}
	for url, want := range map[string]subject{
		"https://api.github.com/repos/has-ghas/widgets/git/trees/abc?recursive=1": {"has-ghas", false},
		"https://api.github.com/orgs/Advanced-Security/properties/schema":         {"Advanced-Security", true},
		"https://api.github.com/users/octocat/repos?per_page=100":                 {"octocat", true},
		"https://ghes.example.com/api/v3/repos/acme/tool/branches?protected=true": {"acme", false},
		"https://api.github.com/advisories?ecosystem=actions":                     {"", false},
		"https://api.github.com/graphql":                                          {"", false},
		"https://api.github.com/repos/":                                           {"", false},
		"not a url %":                                                             {"", false},
	} {
		if owner, account := Subject(url); owner != want.owner || account != want.account {
			t.Errorf("Subject(%q) = %q, %v; want %q, %v", url, owner, account, want.owner, want.account)
		}
	}
}
