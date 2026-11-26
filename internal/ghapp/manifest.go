package ghapp

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

// Manifest is GitHub's App manifest, posted from the operator's browser to create the App.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
	// Always false: letting other accounts install the App is decided in GitHub's settings for it.
	Public bool `json:"public"`
}

// HookAttributes is where the App's deliveries go.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// Use is a call needing a permission: its declaration, the request as GitHub names it, and a fragment of it.
type Use struct {
	Caller  string
	Calls   string
	Literal string
}

// Grant is one requested permission, the GitHub page documenting it, and every call that needs it.
type Grant struct {
	Permission string
	Access     string
	Doc        string
	Uses       []Use
}

// Subscription is one event, the permission GitHub requires for it, and the processor method acting on it.
type Subscription struct {
	Event      string
	Permission string
	// Also is a second permission some of its deliveries need: an organization's ruleset, not a repository's.
	Also    string
	Handler string
}

const (
	docPermissions = "https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps"
	docEvents      = "https://docs.github.com/en/webhooks/webhook-events-and-payloads"
)

// Permissions is everything the App requests: reads, and Copilot requests for authoring. Each call is
// minted a token carrying only the subset it needs, so a crawl never holds Copilot. The App writes
// nothing to a repository: a dependency snapshot and default setup are the repository's own to change.
var Permissions = []Grant{
	{Permission: "metadata", Access: "read",
		Doc: docPermissions + `#repository-permissions-for-metadata (GitHub grants it to every App)`,
		Uses: []Use{
			{"catalog.Client.walkREST", "GET /orgs/{org}/repos, GET /users/{username}/repos", "/repos?per_page="},
			{"catalog.repositoriesQuery", "GraphQL repositoryOwner.repositories", "repositories("},
			{"catalog.Client.ReadRepository", "GET /repos/{owner}/{repo}", "%s/repos/%s/%s"},
			{"catalog.Client.sourceAccess", "GET /repos/{owner}/{repo}", "%s/repos/%s/%s"},
			{"catalog.Client.repositoryProperties", "GET /repos/{owner}/{repo}/properties/values",
				"/repos/%s/%s/properties/values"},
		}},
	{Permission: "contents", Access: "read",
		Doc: docPermissions + "#repository-permissions-for-contents",
		Uses: []Use{
			{"catalog.Client.Tree", "GET /repos/{owner}/{repo}/git/trees/{tree_sha}", "/git/trees/"},
			{"catalog.Client.ProtectedBranches", "GET /repos/{owner}/{repo}/branches", "/branches?protected=true"},
			{"catalog.Client.HeadSHA", "GET /repos/{owner}/{repo}/commits/{ref}", "/commits/HEAD"},
			{"catalog.Client.CompareCommits", "GET /repos/{owner}/{repo}/compare/{basehead}", "/compare/"},
			{"catalog.Client.CompareDependencies", "GET /repos/{owner}/{repo}/dependency-graph/compare/{basehead}",
				"/dependency-graph/compare/"},
			{"catalog.Client.FileContent", "GET /repos/{owner}/{repo}/contents/{path}", "/contents/"},
			{"catalog.Client.ListDatabases", "GET /repos/{owner}/{repo}/code-scanning/codeql/databases",
				"/code-scanning/codeql/databases"},
			{"catalog.Client.DownloadDatabase", "GET /repos/{owner}/{repo}/code-scanning/codeql/databases/{language}",
				"/code-scanning/codeql/databases/"},
			{"catalog.repositoriesQuery", "GraphQL object(expression:) reading a tree", "object(expression:"},
			{"catalog.refQuery", "GraphQL repository.ref resolving where a reference points", "ref(qualifiedName:"},
			{"expand.Checkout.clone", "git clone over HTTPS", "clone"},
			{"expand.Checkout.checkout", "git fetch of a pinned commit", "fetch"},
			{"expand.Checkout.Resolve", "git ls-remote", "ls-remote"},
		}},
	{Permission: "security_events", Access: "read",
		Doc: docPermissions + "#repository-permissions-for-code-scanning-alerts",
		Uses: []Use{
			{"catalog.Client.CodeScanningAlerts", "GET /repos/{owner}/{repo}/code-scanning/alerts", "/code-scanning/alerts"},
			{"catalog.Client.CheckScanningAccess", "GET /repos/{owner}/{repo}/code-scanning/alerts", "/code-scanning/alerts"},
			{"catalog.Client.analysesPage", "GET /repos/{owner}/{repo}/code-scanning/analyses", "/code-scanning/analyses"},
			{"catalog.Client.DownloadAnalysis", "GET /repos/{owner}/{repo}/code-scanning/analyses/{analysis_id}",
				"/code-scanning/analyses/"},
		}},
	{Permission: "organization_custom_properties", Access: "read",
		Doc: docPermissions + "#organization-permissions-for-custom-properties",
		Uses: []Use{
			{"catalog.Client.PropertySchema", "GET /orgs/{org}/properties/schema", "/properties/schema"},
			{"catalog.Client.attachRESTProperties", "GET /orgs/{org}/properties/values", "/properties/values"},
		}},
	// No read needs it; GitHub requires it for workflow_run, whose runs say what a workflow called.
	{Permission: "actions", Access: "read", Doc: docEvents + "#workflow_run"},
	// GET /orgs/{org}/rulesets would need write, so rulesets arrive as events; configurations are read.
	{Permission: "organization_administration", Access: "read",
		Doc: docPermissions + "#organization-permissions-for-administration; " + docEvents + "#repository_ruleset",
		Uses: []Use{
			{"catalog.Client.SecurityConfigurations", "GET /orgs/{org}/code-security/configurations",
				"/code-security/configurations?"},
			{"catalog.Client.ConfigurationRepositories",
				"GET /orgs/{org}/code-security/configurations/{configuration_id}/repositories", "/repositories?per_page="},
		}},
	// GET /branches is contents; default setup is read here, and GitHub requires it for the events below.
	{Permission: "administration", Access: "read",
		Doc: docPermissions + "#repository-permissions-for-administration; " + docEvents +
			"#branch_protection_rule, #branch_protection_configuration, #repository_ruleset, #security_and_analysis",
		Uses: []Use{
			{"catalog.Client.DefaultSetup", "GET /repos/{owner}/{repo}/code-scanning/default-setup",
				"/code-scanning/default-setup"},
			{"catalog.Client.RepositoryConfiguration", "GET /repos/{owner}/{repo}/code-security-configuration",
				"/code-security-configuration"},
			{"catalog.Client.ActionsPermissions", "GET /repos/{owner}/{repo}/actions/permissions",
				"/actions/permissions"},
		}},
	// Signing people in with GitHub reads which organization and team each belongs to.
	{Permission: "members", Access: "read",
		Doc: docPermissions + "#organization-permissions-for-members",
		Uses: []Use{
			{"ghapp.App.Role", "GET /orgs/{org}/memberships/{username}", "/memberships/"},
			{"ghapp.App.Role", "GET /orgs/{org}/teams/{team_slug}/memberships/{username}", "/teams/"},
		}},
	// Copilot requests from an installation token are billed to the installing organization.
	{Permission: "organization_copilot_requests", Access: "read",
		Doc: "https://docs.github.com/en/copilot/how-tos/copilot-sdk/auth/server-to-server-tokens",
		Uses: []Use{
			{"harness.Copilot.Run", "Copilot requests from an authoring session", "Copilot token"},
		}},
}

// Events omits installation, installation_repositories and ping, which every App gets unasked.
var Events = []Subscription{
	{Event: "push", Permission: "contents", Handler: "events.Processor.push"},
	{Event: "create", Permission: "contents", Handler: "events.Processor.refChange"},
	{Event: "delete", Permission: "contents", Handler: "events.Processor.refChange"},
	{Event: "repository", Permission: "metadata", Handler: "events.Processor.repositoryChange"},
	{Event: "branch_protection_rule", Permission: "administration", Handler: "events.Processor.protectionChange"},
	{Event: "branch_protection_configuration", Permission: "administration", Handler: "events.Processor.protectionChange"},
	{Event: "repository_ruleset", Permission: "administration", Also: "organization_administration",
		Handler: "events.Processor.protectionChange"},
	{Event: "security_and_analysis", Permission: "administration", Handler: "events.Processor.securityAndAnalysis"},
	{Event: "workflow_run", Permission: "actions", Handler: "events.Processor.workflowRun"},
	{Event: "custom_property_values", Permission: "organization_custom_properties",
		Handler: "events.Processor.propertyValues"},
	{Event: "custom_property", Permission: "organization_custom_properties",
		Handler: "events.Processor.propertyDefinition"},
	{Event: "installation_target", Handler: "events.Processor.accountRenamed"},
	{Event: "security_advisory", Handler: "events.Processor.advisory"},
	{Event: "meta", Handler: "events.Processor.appDeleted"},
}

// NewManifest is the manifest for an App with exactly the permissions and events above.
func NewManifest(name, homepage, webhookURL, redirectURL string) Manifest {
	m := Manifest{
		Name: name, URL: homepage, RedirectURL: redirectURL,
		Description: "Reads repositories for codeql-abom, the Actions Bill of Materials, and makes " +
			"Copilot requests for query authoring. Requests no write access.",
		HookAttributes:     HookAttributes{URL: webhookURL, Active: true},
		DefaultPermissions: map[string]string{},
		DefaultEvents:      []string{},
	}
	maps.Copy(m.DefaultPermissions, permissionSetOf(Permissions))
	for _, s := range Events {
		m.DefaultEvents = append(m.DefaultEvents, s.Event)
	}
	return m
}

// PublisherGrants is everything the publisher App requests, each traced to the calls that need it:
// one repository's contents and pull requests, which it writes, and the metadata every App reads. It
// is a second App so the reader above can keep writing nothing.
var PublisherGrants = []Grant{
	{Permission: "contents", Access: "write",
		Doc: docPermissions + "#repository-permissions-for-contents",
		Uses: []Use{
			{"publish.Publisher.Open", "GET /repos/{owner}/{repo}/git/ref/{ref}", "/git/ref/heads/"},
			{"publish.Publisher.commit", "POST /repos/{owner}/{repo}/git/trees", "/git/trees"},
			{"publish.Publisher.commit", "POST /repos/{owner}/{repo}/git/commits", "/git/commits"},
			{"publish.Publisher.commit", "POST /repos/{owner}/{repo}/git/refs", "/git/refs"},
		}},
	{Permission: "pull_requests", Access: "write",
		Doc: docPermissions + "#repository-permissions-for-pull-requests",
		Uses: []Use{
			{"publish.Publisher.Open", "POST /repos/{owner}/{repo}/pulls", "/pulls"},
			{"publish.Publisher.Open", "GET /repos/{owner}/{repo}/pulls", "/pulls?"},
		}},
	{Permission: "metadata", Access: "read",
		Doc:  docPermissions + `#repository-permissions-for-metadata (GitHub grants it to every App)`,
		Uses: []Use{{"publish.Publisher.Open", "GET /repos/{owner}/{repo}", "/repos/"}}},
}

// PublisherPermissions is what each of the publisher's tokens carries: PublisherGrants, as a set.
var PublisherPermissions = permissionSetOf(PublisherGrants)

func permissionSetOf(grants []Grant) PermissionSet {
	out := PermissionSet{}
	for _, g := range grants {
		out[g.Permission] = g.Access
	}
	return out
}

// NewPublisherManifest is the manifest for the publisher: PublisherPermissions, no events, and no
// deliveries, because it reads nothing and is told nothing.
func NewPublisherManifest(name, homepage, redirectURL string) Manifest {
	m := Manifest{
		Name: name, URL: homepage, RedirectURL: redirectURL,
		Description: "Opens pull requests of codeql-abom query exports in the one repository it is " +
			"installed on. Install it on that repository only; it is refused anywhere else.",
		HookAttributes:     HookAttributes{URL: homepage, Active: false},
		DefaultPermissions: map[string]string{},
		DefaultEvents:      []string{},
	}
	maps.Copy(m.DefaultPermissions, PublisherPermissions)
	return m
}

// NewAppURL is the organization's new-App page, or the signed-in user's for a personal account.
func NewAppURL(webURL, owner, kind, state string) string {
	base := strings.TrimSuffix(webURL, "/")
	if base == "" {
		base = "https://github.com"
	}
	page := base + "/settings/apps/new"
	if kind != "user" {
		page = base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/new"
	}
	return page + "?state=" + url.QueryEscape(state)
}

// Convert exchanges the single-use manifest code, which appears in no error, for the App and its secrets.
func Convert(ctx context.Context, hc *http.Client, apiURL, code string) (Conversion, error) {
	var out Conversion
	app := &App{API: apiURL, HTTP: hc}
	path := "/app-manifests/" + url.PathEscape(code) + "/conversions"
	if err := app.call(ctx, http.MethodPost, path, "", &out, code); err != nil {
		return Conversion{}, fmt.Errorf("exchanging the manifest code: %w", err)
	}
	if out.ID == 0 || out.PEM == "" {
		return Conversion{}, fmt.Errorf("exchanging the manifest code: GitHub answered without an App or a key")
	}
	return out, nil
}

// requested renders the table for the operator to read before creating the App.
// requestedFrom is what the setup page lists for grants: each permission, and every call and event
// that needs it.
func requestedFrom(grants []Grant, events []Subscription) []map[string]string {
	out := []map[string]string{}
	for _, g := range grants {
		var calls []string
		for _, u := range g.Uses {
			calls = append(calls, u.Calls)
		}
		for _, s := range events {
			if s.Permission == g.Permission || s.Also == g.Permission {
				calls = append(calls, s.Event+" events")
			}
		}
		out = append(out, map[string]string{"permission": g.Permission, "access": g.Access,
			"why": strings.Join(calls, "; ")})
	}
	return out
}
