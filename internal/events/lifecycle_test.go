package events

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// asked records what a processor asked of discovery and the App, in order.
type asked struct{ calls []string }

func (a *asked) hook(p *Processor) *Processor {
	p.List = func(owner string) { a.calls = append(a.calls, "list "+owner) }
	p.ListFully = func(owner string) { a.calls = append(a.calls, "list whole "+owner) }
	p.Read = func(fullName string) { a.calls = append(a.calls, "read "+fullName) }
	p.Advisories = func() { a.calls = append(a.calls, "advisories") }
	p.Installations = func() { a.calls = append(a.calls, "installations") }
	return p
}

func drainWith(t *testing.T, p *Processor) {
	t.Helper()
	if _, err := p.Drain(context.Background()); err != nil {
		t.Fatalf("Drain: %v", err)
	}
}

// Only a full listing records what is gone, and a deletion, an archive, a rename or a transfer is
// exactly that. Anything else changed one repository, which a sweep of a large account would not
// name for a day.
func TestARepositoryLifecycleEventReadsTheRepositoryOrListsItsAccountWhole(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		reason  string
		want    string
	}{
		{"created", repositoryPayload("acme/brand-new", "created"), ReasonReadRequested, "read acme/brand-new"},
		{"unarchived", repositoryPayload("acme/service", "unarchived"), ReasonReadRequested, "read acme/service"},
		{"privatized", repositoryPayload("acme/service", "privatized"), ReasonReadRequested, "read acme/service"},
		{"publicized", repositoryPayload("acme/service", "publicized"), ReasonReadRequested, "read acme/service"},
		{"edited", repositoryPayload("acme/service", "edited"), ReasonReadRequested, "read acme/service"},
		{"archived", repositoryPayload("acme/service", "archived"), ReasonFullListingRequested, "list whole acme"},
		{"deleted", repositoryPayload("acme/service", "deleted"), ReasonFullListingRequested, "list whole acme"},
		{"renamed", repositoryRenamedPayload("acme/renamed", "service"), ReasonFullListingRequested, "list whole acme"},
		{"transferred in", repositoryTransferredPayload("acme/service", "elsewhere"),
			ReasonFullListingRequested, "list whole acme"},
		{"transferred out", repositoryTransferredPayload("elsewhere/service", "acme"),
			ReasonFullListingRequested, "list whole acme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			var a asked
			p := a.hook(f.processor())
			f.deliver("d-1", "repository", tc.payload)
			drainWith(t, p)

			d := f.settled("d-1", store.EventDone, tc.reason)
			if !slices.Equal(a.calls, []string{tc.want}) {
				t.Errorf("asked %v, want %q once", a.calls, tc.want)
			}
			if action := strings.Fields(tc.name)[0]; !strings.Contains(d.Detail, action) {
				t.Errorf("detail %q does not say the repository was %s", d.Detail, action)
			}
		})
	}

	t.Run("outside the declared scope", func(t *testing.T) {
		f := newFixture(t)
		var a asked
		p := a.hook(f.processor())
		f.deliver("d-1", "repository", repositoryPayload("other/thing", "deleted"))
		f.deliver("d-2", "repository", repositoryPayload("other/thing", "edited"))
		drainWith(t, p)

		f.settled("d-1", store.EventIgnored, ReasonNotOwned)
		f.settled("d-2", store.EventIgnored, ReasonNotOwned)
		if len(a.calls) != 0 {
			t.Errorf("asked %v for an account nobody declared", a.calls)
		}
	})
}

// A repository's custom property values place it in the groups a property defines, and no sweep
// of its account names a repository whose values changed.
func TestACustomPropertyValueChangeReadsThatRepository(t *testing.T) {
	f := newFixture(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("v-1", "custom_property_values", propertyValuesPayload("acme/service", "acme"))
	f.deliver("v-2", "custom_property_values", propertyValuesPayload("other/thing", "other"))
	drainWith(t, p)

	f.settled("v-1", store.EventDone, ReasonReadRequested)
	f.settled("v-2", store.EventIgnored, ReasonNotOwned)
	if !slices.Equal(a.calls, []string{"read acme/service"}) {
		t.Errorf("asked %v, want acme/service read", a.calls)
	}
}

// A definition's new default changes every repository that set no value, and no event names those
// repositories.
func TestAnOrganizationWideChangeListsTheOrganizationWhole(t *testing.T) {
	f := newFixture(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("c-1", "custom_property", propertyDefinitionPayload("updated", "acme"))
	f.deliver("c-2", "custom_property", propertyDefinitionPayload("created", "other"))
	f.deliver("r-1", "repository_ruleset", rulesetPayload("", "other"))
	drainWith(t, p)

	if d := f.settled("c-1", store.EventDone, ReasonFullListingRequested); !strings.Contains(d.Detail, "team") {
		t.Errorf("detail %q does not name the property", d.Detail)
	}
	f.settled("c-2", store.EventIgnored, ReasonNotOwned)
	if d := f.settled("r-1", store.EventIgnored, ReasonNotOwned); !strings.Contains(d.Detail, "not a declared account") {
		t.Errorf("detail %q does not say the organization is not declared", d.Detail)
	}
	if !slices.Equal(a.calls, []string{"list whole acme"}) {
		t.Errorf("asked %v, want acme listed whole once", a.calls)
	}
}

// A declaration is the operator's statement about which account is theirs, so a rename is reported
// for them to act on and never followed: whoever takes the old login would otherwise inherit it.
func TestAnAccountRenameIsReportedAndTheDeclarationLeftAlone(t *testing.T) {
	f := newFixture(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("t-1", "installation_target", installationTargetPayload("acme-industries", "acme"))
	f.deliver("t-2", "installation_target", installationTargetPayload("other-two", "other"))
	drainWith(t, p)

	d := f.settled("t-1", store.EventDone, ReasonAccountRenamed)
	for _, login := range []string{"acme", "acme-industries"} {
		if !strings.Contains(d.Detail, login) {
			t.Errorf("detail %q does not name %s", d.Detail, login)
		}
	}
	f.settled("t-2", store.EventIgnored, ReasonNotOwned)
	if !slices.Equal(a.calls, []string{"installations", "installations"}) {
		t.Errorf("asked %v, want the installations re-read for each and nothing listed", a.calls)
	}
	declared, err := f.store.OwnedScope()
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) != 1 || declared[0].Owner != "acme" {
		t.Errorf("declared %+v, want acme alone and unchanged", declared)
	}
}

// GitHub sends every ecosystem's advisories, and only an Actions advisory can match a node. That one
// changes exposure with no repository changing, which no poll or push would notice.
func TestOnlyAnActionsAdvisoryAsksForARefresh(t *testing.T) {
	f := newFixture(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("a-1", "security_advisory", advisoryPayload("published", "npm", "ACTIONS"))
	f.deliver("a-2", "security_advisory", advisoryPayload("updated", "npm"))
	drainWith(t, p)

	f.settled("a-1", store.EventDone, ReasonAdvisoryRefresh)
	f.settled("a-2", store.EventIgnored, ReasonNotActions)
	if !slices.Equal(a.calls, []string{"advisories"}) {
		t.Errorf("asked %v, want one refresh", a.calls)
	}
}

// A completed run says which reusable workflows a workflow actually called and at which commit,
// which is what the graph's edges are checked against. One still running says nothing yet, and a
// redelivery adds nothing.
func TestACompletedWorkflowRunIsRecorded(t *testing.T) {
	f := newFixture(t)
	const path = ".github/workflows/ci.yml"
	f.deliver("w-1", "workflow_run", workflowRunPayload("acme/service", "in_progress", path, shaB))
	f.deliver("w-2", "workflow_run", workflowRunPayload("acme/service", "completed", path, shaB))
	f.deliver("w-3", "workflow_run", workflowRunPayload("acme/service", "completed", path, shaB))
	f.deliver("w-4", "workflow_run", workflowRunPayload("other/thing", "completed", path, shaB))
	f.drain()

	f.settled("w-1", store.EventIgnored, ReasonNotCompleted)
	f.settled("w-2", store.EventDone, ReasonRunRecorded)
	f.settled("w-3", store.EventDone, ReasonRunRecorded)
	f.settled("w-4", store.EventIgnored, ReasonNotOwned)
	runs, err := f.store.RecentWorkflowRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].RunID != 30433642 || runs[0].Attempt != 2 || runs[0].HeadSHA != shaB ||
		runs[0].Conclusion != "success" || len(runs[0].Referenced) != 1 || runs[0].Referenced[0].SHA != shaC {
		t.Errorf("runs = %+v, want one completed attempt and the workflow it called", runs)
	}
}

// Code scanning switched on or off, or a run of it finishing, changes whether GitHub holds the
// repository's actions database. Either re-asks, and the answer replaces what discovery recorded.
func TestCodeScanningChangesReaskForTheRepositorysDatabase(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/quiet", "main", shaA)
	var dbAsked []string
	p := f.processor()
	p.Databases = func(_ context.Context, repository, language string) (catalog.Database, error) {
		dbAsked = append(dbAsked, repository+" "+language)
		if repository == "acme/service" {
			return catalog.Database{Repository: repository, Language: language, CommitOID: shaB}, nil
		}
		return catalog.Database{}, fmt.Errorf("%s has no %s database: %w", repository, language, catalog.ErrNoDatabase)
	}
	f.deliver("s-1", "security_and_analysis", securityAndAnalysisPayload("acme/service"))
	f.deliver("s-2", "workflow_run",
		workflowRunPayload("acme/quiet", "completed", "dynamic/github-code-scanning/codeql", shaA))
	f.deliver("s-3", "workflow_run", workflowRunPayload("acme/service", "completed", ".github/workflows/ci.yml", shaB))
	drainWith(t, p)

	f.settled("s-1", store.EventDone, ReasonDatabaseChecked)
	f.settled("s-2", store.EventDone, ReasonDatabaseChecked)
	f.settled("s-3", store.EventDone, ReasonRunRecorded)
	if want := []string{"acme/service actions", "acme/quiet actions"}; !slices.Equal(dbAsked, want) {
		t.Errorf("asked for %v, want %v: a run that is not code scanning asks nothing", dbAsked, want)
	}
	if commit, found, err := f.store.CatalogCommit("acme/service", "actions"); err != nil || !found || commit != shaB {
		t.Errorf("acme/service recorded at %q (found %v, %v), want %s", commit, found, err, shaB)
	}
	withDB, total, err := f.store.CatalogCoverage("actions")
	if err != nil || withDB != 1 || total != 2 {
		t.Errorf("catalog holds %d of %d with a database (%v), want acme/quiet recorded as holding none",
			withDB, total, err)
	}
}

// The App's deletion takes its hook and installations with it, so no event follows this one, and the
// loss is recorded as a fact rather than inferred from tokens that stop working.
func TestTheAppsDeletionIsRecorded(t *testing.T) {
	f := newFixture(t)
	var a asked
	p := a.hook(f.processor())
	f.deliver("m-1", "meta", metaPayload())
	drainWith(t, p)

	if d := f.settled("m-1", store.EventDone, ReasonAppDeleted); !strings.Contains(d.Detail, "554188") {
		t.Errorf("detail %q does not name the deleted hook", d.Detail)
	}
	if !slices.Equal(a.calls, []string{"installations"}) {
		t.Errorf("asked %v, want the installations re-read", a.calls)
	}
}

// A tag moves like a branch, and a workflow elsewhere pinning it reads it through a row of its own:
// has-ghas/x@v1 used to move only for the sweep.
func TestATagMoveRequeuesTheRowsStandingAtIt(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "v1", 1, shaA)
	f.answers["acme/service@v1"] = shaB
	f.deliver("t-1", "push", pushPayload("acme/service", "refs/tags/v1", shaA, shaB))
	f.deliver("t-2", "create", refPayload("acme/service", "v2", "tag"))
	f.drain()

	f.settled("t-1", store.EventDone, ReasonRequeued)
	f.settled("t-2", store.EventIgnored, ReasonNotReferenced)
	if r := f.row("acme/service", "v1"); r.Status != frontier.StatusPending || !r.Expedited {
		t.Errorf("acme/service@v1 is %s (expedited %v), want requeued ahead of the ranking", r.Status, r.Expedited)
	}
	if !slices.Equal(f.calls, []string{"probe acme/service@v1"}) {
		t.Errorf("calls = %v, want v1 probed once and nothing for a tag no row stands at", f.calls)
	}
}

// A deleted branch or tag that another repository names breaks that reference, and the event says
// so a day before the sweep would. Re-created, the reference is read again.
func TestADeletedReferenceIsMarkedGoneAndReadAgainWhenRecreated(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "v1", 1, shaA)
	f.analyzed("acme/service", "feature", 2, shaA)
	f.probeErrs["acme/service@v1"] = missing("v1")
	f.probeErrs["acme/service@feature"] = missing("feature")
	f.deliver("d-1", "delete", refPayload("acme/service", "v1", "tag"))
	f.deliver("d-2", "delete", refPayload("acme/service", "feature", "branch"))
	f.drain()

	f.settled("d-1", store.EventDone, ReasonReferenceGone)
	f.settled("d-2", store.EventDone, ReasonReferenceGone)
	for _, ref := range []string{"v1", "feature"} {
		if r := f.row("acme/service", ref); r.Status != frontier.StatusUnavailable {
			t.Errorf("acme/service@%s is %s, want unavailable", ref, r.Status)
		}
	}
	if !slices.Contains(f.calls, "branches acme/service") {
		t.Errorf("calls = %v, want the branch deletion to re-read protection too", f.calls)
	}

	delete(f.probeErrs, "acme/service@v1")
	f.answers["acme/service@v1"] = shaB
	f.deliver("c-1", "create", refPayload("acme/service", "v1", "tag"))
	f.drain()

	f.settled("c-1", store.EventDone, ReasonBack)
	if r := f.row("acme/service", "v1"); r.Status != frontier.StatusPending || !r.Expedited {
		t.Errorf("acme/service@v1 is %s (expedited %v), want read again", r.Status, r.Expedited)
	}
}

// Protection decides a branch's own rows, and its push re-resolves them; a branch event probes only
// the references to the branch from elsewhere.
func TestABranchEventProbesOnlyTheReferencesToIt(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.SetProtectedBranches("acme/service", []string{"release"}); err != nil {
		t.Fatalf("SetProtectedBranches: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service", Ref: "release"},
		frontier.Resolution{Ref: "release", SHA: shaA}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := f.queue.Enqueue([]frontier.Item{{NodeKey: "acme/service/lint", Ref: "release",
		Owner: "acme", Repo: "service", Subpath: "lint", Depth: 2}}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := f.queue.Complete(frontier.Key{NodeKey: "acme/service/lint", Ref: "release"},
		frontier.Resolution{Ref: "release", SHA: shaA}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	f.branches["acme/service"] = []string{"release"}
	f.answers["acme/service@release"] = shaB

	f.deliver("c-1", "create", refPayload("acme/service", "release", "branch"))
	f.drain()

	f.settled("c-1", store.EventDone, ReasonRequeued)
	if r := f.row("acme/service/lint", "release"); r.Status != frontier.StatusPending {
		t.Errorf("the reference from elsewhere is %s, want requeued", r.Status)
	}
	if r := f.row("acme/service", "release"); r.Status != frontier.StatusDone || r.Depth != 0 {
		t.Errorf("the branch's own row is %s at depth %d, want it left to protection and its push", r.Status, r.Depth)
	}
}

// missing is what the probe says of a reference the repository does not hold.
func missing(ref string) error {
	return fmt.Errorf("%w: %w: acme/service has no %s", expand.ErrUnavailable, expand.ErrReferenceMissing, ref)
}

// A tag deleted and pushed again at the same commit is back; the probe finding the commit already
// analyzed must not leave its references marked gone.
func TestAReferenceRecreatedAtTheSameCommitIsReadAgain(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "v1", 1, shaA)
	f.probeErrs["acme/service@v1"] = missing("v1")
	f.deliver("d-1", "delete", refPayload("acme/service", "v1", "tag"))
	f.drain()
	f.settled("d-1", store.EventDone, ReasonReferenceGone)

	delete(f.probeErrs, "acme/service@v1")
	f.answers["acme/service@v1"] = shaA
	f.deliver("c-1", "create", refPayload("acme/service", "v1", "tag"))
	f.drain()

	// Not "moved": the commit is the one analyzed, and a count of moves would include it.
	f.settled("c-1", store.EventDone, ReasonBack)
	if r := f.row("acme/service", "v1"); r.Status != frontier.StatusPending || !r.Expedited {
		t.Errorf("acme/service@v1 is %s (expedited %v), want it out of unavailable and read again", r.Status, r.Expedited)
	}
}

// A probe that could not ask says nothing about the reference: before events marked rows gone, a
// failed probe on a push left them alone, and it still must.
func TestAProbeThatCouldNotAskMarksNothingGone(t *testing.T) {
	f := newFixture(t)
	f.analyzed("acme/service", "v1", 1, shaA)
	f.probeErrs["acme/service@v1"] = fmt.Errorf("%w: resolving acme/service@v1: Could not resolve host", expand.ErrUnavailable)
	f.deliver("d-1", "delete", refPayload("acme/service", "v1", "tag"))
	f.drain()

	f.settled("d-1", store.EventFailed, ReasonProbeFailed)
	if r := f.row("acme/service", "v1"); r.Status != frontier.StatusDone {
		t.Errorf("acme/service@v1 is %s after a probe that could not ask, want done", r.Status)
	}
}

// A code scanning run starts when default setup changes, and security and analysis changes decide
// whether it can be read at all: both re-read the repository's setup, and nothing else does.
func TestCodeScanningChangesRereadTheRepositorysSetup(t *testing.T) {
	f := newFixture(t)
	f.owned("acme/quiet", "main", shaA)
	p := f.processor()
	p.Databases = func(_ context.Context, repository, language string) (catalog.Database, error) {
		return catalog.Database{}, fmt.Errorf("%s: %w", repository, catalog.ErrNoDatabase)
	}
	var reread []string
	p.Setup = func(_ context.Context, repository string) error {
		reread = append(reread, repository)
		if repository == "acme/quiet" {
			return fmt.Errorf("502 Bad Gateway")
		}
		return nil
	}
	f.deliver("s-1", "security_and_analysis", securityAndAnalysisPayload("acme/service"))
	f.deliver("s-2", "workflow_run",
		workflowRunPayload("acme/quiet", "completed", "dynamic/github-code-scanning/codeql", shaA))
	f.deliver("s-3", "workflow_run", workflowRunPayload("acme/service", "completed", ".github/workflows/ci.yml", shaB))
	drainWith(t, p)

	if want := []string{"acme/service", "acme/quiet"}; !slices.Equal(reread, want) {
		t.Errorf("re-read setup for %v, want %v", reread, want)
	}
	if d := f.settled("s-1", store.EventDone, ReasonDatabaseChecked); !strings.Contains(d.Detail, "default setup re-read") {
		t.Errorf("detail %q does not say the setup was re-read", d.Detail)
	}
	// The database answer stands; a setup read that failed is said, not hidden behind it.
	if d := f.settled("s-2", store.EventDone, ReasonDatabaseChecked); !strings.Contains(d.Detail, "502 Bad Gateway") {
		t.Errorf("detail %q does not say the setup read failed", d.Detail)
	}
}
