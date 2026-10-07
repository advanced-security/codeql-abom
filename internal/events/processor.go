package events

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/enumerate"
	"github.com/advanced-security/codeql-abom/internal/expand"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Reasons a delivery settles the way it does. Fixed phrases, so the status can count by them.
const (
	ReasonRequeued             = "moved; requeued ahead of the ranking"
	ReasonBack                 = "back; requeued ahead of the ranking"
	ReasonExpedited            = "already queued; now ahead of the ranking"
	ReasonUnchanged            = "unchanged"
	ReasonBranchesRefreshed    = "protected branches refreshed"
	ReasonNotOwned             = "not owned"
	ReasonNotEnumerated        = "not enumerated yet"
	ReasonNotBranch            = "not a branch"
	ReasonNotLive              = "not a live branch"
	ReasonNotQueued            = "live branch not queued"
	ReasonNotReferenced        = "not referenced"
	ReasonDefaultUnknown       = "default branch not recorded"
	ReasonRefDeleted           = "reference deleted"
	ReasonReferenceGone        = "reference gone"
	ReasonNotActedOn           = "not acted on yet"
	ReasonListingRequested     = "listing requested"
	ReasonFullListingRequested = "full listing requested"
	ReasonReadRequested        = "repository read requested"
	ReasonAccountRenamed       = "account renamed"
	ReasonAdvisoryRefresh      = "advisory refresh requested"
	ReasonNotActions           = "not an Actions advisory"
	ReasonDatabaseChecked      = "code scanning database checked"
	ReasonDatabaseCheckFailed  = "code scanning database check failed"
	ReasonRunRecorded          = "workflow run recorded"
	ReasonNotCompleted         = "run not completed"
	ReasonAppDeleted           = "App deleted"
	ReasonPing                 = "ping"
	ReasonUnhandled            = "event not handled"
	ReasonAnalysisRunning      = "analysis running"
	ReasonUnparseable          = "unparseable payload"
	ReasonProbeFailed          = "probe failed"
	ReasonBranchReadFailed     = "branch read failed"
	ReasonProtectsNoBranch     = "protects no branch"
)

// databaseLanguage is the CodeQL language whose database code scanning may hold for ABOM.
const databaseLanguage = "actions"

// codeScanningRuns prefixes the workflow path of code scanning's default setup runs.
const codeScanningRuns = "dynamic/github-code-scanning/"

// Processor settles the log in order; every action re-reads its subject, so a repeat is harmless.
type Processor struct {
	Store *store.Store
	Queue *frontier.Queue
	// Probe is the recheck's remote probe. It, not the payload, says whether a branch moved.
	Probe expand.RefProbe
	// Pace is the event follow-up pool each probe draws on. Nil probes without limit or record.
	Pace *work.TokenPool
	// Branches is enumeration's protected-branch reader. Nil fails protection events.
	Branches enumerate.ReadBranches
	// List has a declared account listed on the next cycle. Nil leaves it to the period.
	List func(owner string)
	// ListFully has a declared account listed whole on the next cycle, which alone records what is
	// gone. Nil leaves it to the full listing's period.
	ListFully func(owner string)
	// Read has one repository read on the next cycle. Nil leaves it to a listing that names it.
	Read func(fullName string)
	// Advisories has the advisory set refreshed once its floor allows. Nil leaves it to its ceiling.
	Advisories func()
	// Databases asks code scanning for a repository's database. Nil leaves the catalog to discover.
	Databases func(ctx context.Context, repository, language string) (catalog.Database, error)
	// Setup re-reads one repository's code scanning setup. Nil leaves it to the next listing.
	Setup func(ctx context.Context, repository string) error
	// Installations re-reads the App's installations. Nil leaves them to their period.
	Installations func()
	// Wake starts a discovery cycle once a delivery queued work. Nil leaves it to the next cycle.
	Wake func()
	// Dependencies asks dependency review what moved between two commits of an owned repository,
	// which orders a push among the others. Nil orders every push alike.
	Dependencies func(ctx context.Context, repository, base, head string) (catalog.DependencyComparison, error)
	// ReviewTimeout bounds how long a push waits for its comparison; zero is DefaultReviewTimeout.
	ReviewTimeout time.Duration
	// Log receives one line per settled delivery. Nil discards them.
	Log io.Writer
}

// Pass is what one pass over the log did.
type Pass struct {
	Settled int
	// Deferred counts deliveries left pending until what they name is no longer being analyzed.
	Deferred int
}

type outcome struct {
	// status is empty for a delivery that has to wait.
	status, reason, detail string
}

func settle(status, reason, format string, args ...any) outcome {
	return outcome{status: status, reason: reason, detail: fmt.Sprintf(format, args...)}
}

const drainBatch = 100

// Drain settles pending deliveries oldest first; one that must wait does not hold up the rest.
func (p *Processor) Drain(ctx context.Context) (Pass, error) {
	var pass Pass
	var after int64
	for {
		pending, err := p.Store.PendingDeliveries(after, drainBatch)
		if err != nil || len(pending) == 0 {
			return pass, err
		}
		for _, d := range pending {
			if err := ctx.Err(); err != nil {
				return pass, err
			}
			after = d.ID
			o, err := p.handle(ctx, d)
			if err != nil {
				return pass, fmt.Errorf("processing delivery %s: %w", d.DeliveryID, err)
			}
			if o.status == "" {
				if err := p.Store.DeferDelivery(d.ID, o.reason, o.detail); err != nil {
					return pass, err
				}
				pass.Deferred++
				if d.Attempts == 0 {
					p.logf(d, "waiting", o)
				}
				continue
			}
			if _, err := p.Store.SettleDelivery(d.ID, o.status, o.reason, o.detail); err != nil {
				return pass, err
			}
			pass.Settled++
			p.logf(d, o.status, o)
		}
	}
}

// Run drains at once, on wake and every period until ctx ends; a failed pass is retried.
func (p *Processor) Run(ctx context.Context, wake <-chan struct{}, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		if _, err := p.Drain(ctx); err != nil && ctx.Err() == nil && p.Log != nil {
			fmt.Fprintf(p.Log, "%s serve: processing webhook deliveries: %v\n", stamp(), err)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
	}
}

func stamp() string { return time.Now().UTC().Format(time.RFC3339) }

func (p *Processor) logf(d store.EventDelivery, status string, o outcome) {
	if p.Log == nil {
		return
	}
	fmt.Fprintf(p.Log, "%s serve: event %s %s: %s (%s) %s\n",
		stamp(), d.Event, d.DeliveryID, status, o.reason, o.detail)
}

func (p *Processor) handle(ctx context.Context, d store.EventDelivery) (outcome, error) {
	switch d.Event {
	case "push":
		return p.push(ctx, d)
	case "create", "delete":
		return p.refChange(ctx, d)
	case "branch_protection_rule", "branch_protection_configuration", "repository_ruleset":
		return p.protectionChange(ctx, d)
	case "repository":
		return p.repositoryChange(d)
	case "installation", "installation_repositories":
		return p.accountChange(d)
	case "custom_property_values":
		return p.propertyValues(d)
	case "custom_property":
		return p.propertyDefinition(d)
	case "installation_target":
		return p.accountRenamed(d)
	case "security_advisory":
		return p.advisory(d)
	case "security_and_analysis":
		return p.securityAndAnalysis(ctx, d)
	case "workflow_run":
		return p.workflowRun(ctx, d)
	case "meta":
		return p.appDeleted(d)
	case "ping":
		return settle(store.EventIgnored, ReasonPing, "ping: the hook reaches this receiver"), nil
	default:
		return settle(store.EventIgnored, ReasonUnhandled, "%s events are not handled", d.Event), nil
	}
}

type repositoryRef struct {
	FullName string `json:"full_name"`
}

type account struct {
	Login string `json:"login"`
}

type pushEvent struct {
	Ref        string        `json:"ref"`
	After      string        `json:"after"`
	Deleted    bool          `json:"deleted"`
	Repository repositoryRef `json:"repository"`
}

type refEvent struct {
	Ref        string        `json:"ref"`
	RefType    string        `json:"ref_type"`
	Repository repositoryRef `json:"repository"`
}

type actionEvent struct {
	Action       string         `json:"action"`
	Repository   *repositoryRef `json:"repository"`
	Organization *account       `json:"organization"`
	Installation *struct {
		Account account `json:"account"`
	} `json:"installation"`
}

func decode(d store.EventDelivery, into any) (outcome, bool) {
	if err := json.Unmarshal(d.Payload, into); err != nil {
		return settle(store.EventFailed, ReasonUnparseable, "%s payload: %v", d.Event, err), false
	}
	return outcome{}, true
}

// ours says whether a repository is the operator's to act on, and why not when it is not.
func (p *Processor) ours(fullName string) (outcome, bool, error) {
	if fullName == "" {
		return settle(store.EventIgnored, ReasonNotOwned, "the payload names no repository"), false, nil
	}
	owned, err := p.Store.RepositoryIsOwned(fullName)
	if err != nil {
		return outcome{}, false, err
	}
	if !owned {
		return settle(store.EventIgnored, ReasonNotOwned,
			"%s is outside the declared scope", fullName), false, nil
	}
	origin, err := p.Store.RepositoryOrigin(fullName)
	if err != nil {
		return outcome{}, false, err
	}
	if origin == "" {
		why := fullName + " is declared and no listing has named it yet"
		if p.Read == nil {
			return settle(store.EventIgnored, ReasonNotEnumerated, "%s", why), false, nil
		}
		return p.readNamed(fullName, why), false, nil
	}
	return outcome{}, true, nil
}

// readNamed has a repository an event named read on the next cycle; the read records and queues it.
func (p *Processor) readNamed(repo, why string) outcome {
	if p.Read == nil {
		return settle(store.EventIgnored, ReasonNotActedOn, "%s", why)
	}
	p.Read(repo)
	return settle(store.EventDone, ReasonReadRequested, "%s; %s is read on the next discovery cycle", why, repo)
}

// readOwned reads a repository an event named, if the declared scope covers it or could: in an
// account a property scope decides, a repository not owned yet may be the one the event brings in.
func (p *Processor) readOwned(repo, what string) (outcome, error) {
	owned, err := p.Store.RepositoryIsOwned(repo)
	if err != nil {
		return outcome{}, err
	}
	if !owned {
		owner, _, _ := strings.Cut(repo, "/")
		scoped, err := p.propertyScoped(owner)
		if err != nil || !scoped {
			return settle(store.EventIgnored, ReasonNotOwned, "%s, which is outside the declared scope", what), err
		}
	}
	return p.readNamed(repo, what), nil
}

// propertyScoped reports whether an account's declaration holds a property scope.
func (p *Processor) propertyScoped(owner string) (bool, error) {
	declared, err := p.Store.OwnedScope()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(declared, func(a store.DeclaredAccount) bool {
		return strings.EqualFold(a.Owner, owner) && a.HasProperties()
	}), nil
}

// declaredAccount reports whether an account is declared and enabled.
func (p *Processor) declaredAccount(owner string) (bool, error) {
	declared, err := p.Store.OwnedScope()
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(declared, func(a store.DeclaredAccount) bool {
		return a.Enabled && strings.EqualFold(a.Owner, owner)
	}), nil
}

// listWhole has each declared account owning one of these repositories, under any name an event
// gave it, listed whole on the next cycle; only a full listing records what is gone.
func (p *Processor) listWhole(what string, repositories ...string) (outcome, error) {
	if p.ListFully == nil {
		return settle(store.EventIgnored, ReasonNotActedOn, "%s", what), nil
	}
	var owners []string
	for _, repo := range repositories {
		owned, err := p.Store.RepositoryIsOwned(repo)
		if err != nil {
			return outcome{}, err
		}
		owner, _, _ := strings.Cut(repo, "/")
		if owned && !slices.ContainsFunc(owners, func(o string) bool { return strings.EqualFold(o, owner) }) {
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 {
		return settle(store.EventIgnored, ReasonNotOwned, "%s, which is outside the declared scope", what), nil
	}
	for _, owner := range owners {
		p.ListFully(owner)
	}
	return settle(store.EventDone, ReasonFullListingRequested,
		"%s; %s listed whole on the next discovery cycle", what, strings.Join(owners, ", ")), nil
}

// listAccountWhole lists a declared account whole for an event about the account, not a repository.
func (p *Processor) listAccountWhole(what, owner string) (outcome, error) {
	if p.ListFully == nil {
		return settle(store.EventIgnored, ReasonNotActedOn, "%s", what), nil
	}
	declared, err := p.declaredAccount(owner)
	if err != nil || !declared {
		return settle(store.EventIgnored, ReasonNotOwned, "%s, which is not a declared account", what), err
	}
	p.ListFully(owner)
	return settle(store.EventDone, ReasonFullListingRequested,
		"%s; %s listed whole on the next discovery cycle", what, owner), nil
}

// listing has a declared account listed on the next cycle; the listing queues what it names.
func (p *Processor) listing(owner, why string) outcome {
	p.List(owner)
	return settle(store.EventDone, ReasonListingRequested,
		"%s; %s is listed on the next discovery cycle", why, owner)
}

// accountChange re-reads the App's installations, which are evidence for any account, and lists a
// declared account whose repositories the App reaches changed.
func (p *Processor) accountChange(d store.EventDelivery) (outcome, error) {
	var e actionEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	if p.Installations != nil {
		p.Installations()
	}
	if p.List == nil {
		return notActedOn(d), nil
	}
	what := d.Event + " " + e.Action
	if e.Installation == nil || e.Installation.Account.Login == "" {
		return settle(store.EventIgnored, ReasonNotOwned, "%s names no account", what), nil
	}
	owner := e.Installation.Account.Login
	declared, err := p.declaredAccount(owner)
	if err != nil || !declared {
		return settle(store.EventIgnored, ReasonNotOwned,
			"%s for %s, which is not a declared account", what, owner), err
	}
	return p.listing(owner, what+" for "+owner), nil
}

type repositoryEvent struct {
	Action     string        `json:"action"`
	Repository repositoryRef `json:"repository"`
	Changes    struct {
		Repository struct {
			Name struct {
				From string `json:"from"`
			} `json:"name"`
		} `json:"repository"`
		Owner struct {
			From struct {
				User         account `json:"user"`
				Organization account `json:"organization"`
			} `json:"from"`
		} `json:"owner"`
	} `json:"changes"`
}

// repositoryChange lists each account a deletion, archive, rename or transfer touched whole, since
// only a full listing records what is gone; anything else changed one repository, which is read.
func (p *Processor) repositoryChange(d store.EventDelivery) (outcome, error) {
	var e repositoryEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	repo := e.Repository.FullName
	what := "repository " + e.Action + " " + repo
	switch e.Action {
	case "deleted", "archived", "renamed", "transferred":
		owner, name, _ := strings.Cut(repo, "/")
		named := []string{repo}
		if was := e.Changes.Repository.Name.From; was != "" {
			named = append(named, owner+"/"+was)
		}
		from := e.Changes.Owner.From
		if was := cmp.Or(from.Organization.Login, from.User.Login); was != "" {
			named = append(named, was+"/"+name)
		}
		return p.listWhole(what, named...)
	}
	return p.readOwned(repo, what)
}

// propertyValues reads a repository whose custom property values changed, which moves it between
// the groups a property defines; no sweep of its account would name it.
func (p *Processor) propertyValues(d store.EventDelivery) (outcome, error) {
	var e actionEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	if e.Repository == nil || e.Repository.FullName == "" {
		return settle(store.EventIgnored, ReasonNotOwned, "%s %s names no repository", d.Event, e.Action), nil
	}
	return p.readOwned(e.Repository.FullName, "custom property values "+e.Action+" on "+e.Repository.FullName)
}

// propertyDefinition lists the organization whole: a new default changes every repository that set
// no value, and no event names those.
func (p *Processor) propertyDefinition(d store.EventDelivery) (outcome, error) {
	var e struct {
		Action       string  `json:"action"`
		Organization account `json:"organization"`
		Definition   struct {
			PropertyName string `json:"property_name"`
		} `json:"definition"`
	}
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	return p.listAccountWhole(fmt.Sprintf("custom property %s %s in %s",
		e.Definition.PropertyName, e.Action, e.Organization.Login), e.Organization.Login)
}

// accountRenamed re-reads the installations and names the declaration still holding the old login.
// A declaration is the operator's statement, so it is never moved to the new one.
func (p *Processor) accountRenamed(d store.EventDelivery) (outcome, error) {
	var e struct {
		Account account `json:"account"`
		Changes struct {
			Login struct {
				From string `json:"from"`
			} `json:"login"`
			Slug struct {
				From string `json:"from"`
			} `json:"slug"`
		} `json:"changes"`
	}
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	if p.Installations != nil {
		p.Installations()
	}
	was, now := cmp.Or(e.Changes.Login.From, e.Changes.Slug.From), e.Account.Login
	declared, err := p.declaredAccount(was)
	if err != nil || !declared {
		return settle(store.EventIgnored, ReasonNotOwned,
			"%s was renamed %s and is not a declared account", was, now), err
	}
	return settle(store.EventDone, ReasonAccountRenamed,
		"%s is now %s; its declaration still names %s, so declare %s to keep reading it", was, now, was, now), nil
}

// advisory asks for an advisory refresh when one concerns Actions; GitHub sends every ecosystem's,
// and only an Actions advisory can match a node.
func (p *Processor) advisory(d store.EventDelivery) (outcome, error) {
	var e struct {
		Action   string `json:"action"`
		Advisory struct {
			GHSA            string `json:"ghsa_id"`
			Vulnerabilities []struct {
				Package struct {
					Ecosystem string `json:"ecosystem"`
				} `json:"package"`
			} `json:"vulnerabilities"`
		} `json:"security_advisory"`
	}
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	what := "advisory " + e.Advisory.GHSA + " " + e.Action
	if !slices.ContainsFunc(e.Advisory.Vulnerabilities, func(v struct {
		Package struct {
			Ecosystem string `json:"ecosystem"`
		} `json:"package"`
	}) bool {
		return strings.EqualFold(v.Package.Ecosystem, "actions")
	}) {
		return settle(store.EventIgnored, ReasonNotActions, "%s concerns no Actions package", what), nil
	}
	if p.Advisories == nil {
		return settle(store.EventIgnored, ReasonNotActedOn, "%s", what), nil
	}
	p.Advisories()
	return settle(store.EventDone, ReasonAdvisoryRefresh,
		"%s concerns Actions; advisories are refreshed once their floor allows", what), nil
}

// securityAndAnalysis re-asks for the database of a repository whose code scanning was switched.
func (p *Processor) securityAndAnalysis(ctx context.Context, d store.EventDelivery) (outcome, error) {
	var e actionEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	repo := ""
	if e.Repository != nil {
		repo = e.Repository.FullName
	}
	if o, ok, err := p.ours(repo); !ok || err != nil {
		return o, err
	}
	checked, err := p.recheckDatabase(ctx, repo, "security and analysis changed on "+repo)
	return p.rereadSetup(ctx, repo, checked), err
}

// rereadSetup reads a repository's default setup again after something that changes it, saying
// in the outcome whether that worked: a failed read leaves the last reading, not the event, wrong.
func (p *Processor) rereadSetup(ctx context.Context, repo string, o outcome) outcome {
	if p.Setup == nil || o.status != store.EventDone {
		return o
	}
	if err := p.Setup(ctx, repo); err != nil {
		o.detail += "; default setup could not be re-read: " + err.Error()
		return o
	}
	o.detail += "; default setup re-read"
	return o
}

// recheckDatabase asks code scanning whether it holds the repository's actions database and records
// the answer in place of what discovery recorded; a failed ask leaves that standing.
func (p *Processor) recheckDatabase(ctx context.Context, repo, what string) (outcome, error) {
	if p.Databases == nil {
		return settle(store.EventIgnored, ReasonNotActedOn, "%s", what), nil
	}
	db, err := p.Databases(ctx, repo, databaseLanguage)
	if err != nil && !errors.Is(err, catalog.ErrNoDatabase) {
		return settle(store.EventFailed, ReasonDatabaseCheckFailed,
			"%s: %v; the recorded answer stands", what, err), nil
	}
	if err := p.Store.UpsertCatalogEntry(store.CatalogEntry{
		Repository: repo, Language: databaseLanguage, CommitOID: db.CommitOID, SizeBytes: db.Size,
		HasDatabase: db.CommitOID != "",
	}); err != nil {
		return outcome{}, err
	}
	held := "holds no actions database for it"
	if db.CommitOID != "" {
		held = "holds its actions database at " + db.CommitOID
	}
	return settle(store.EventDone, ReasonDatabaseChecked, "%s: code scanning %s", what, held), nil
}

type workflowRunEvent struct {
	Action      string        `json:"action"`
	Repository  repositoryRef `json:"repository"`
	WorkflowRun struct {
		ID         int64                      `json:"id"`
		RunAttempt int                        `json:"run_attempt"`
		Path       string                     `json:"path"`
		HeadBranch string                     `json:"head_branch"`
		HeadSHA    string                     `json:"head_sha"`
		Event      string                     `json:"event"`
		Conclusion string                     `json:"conclusion"`
		UpdatedAt  time.Time                  `json:"updated_at"`
		Referenced []store.ReferencedWorkflow `json:"referenced_workflows"`
	} `json:"workflow_run"`
}

// workflowRun records a completed run, which says what a workflow actually called; a code scanning
// run also re-asks for the database it may have left.
func (p *Processor) workflowRun(ctx context.Context, d store.EventDelivery) (outcome, error) {
	var e workflowRunEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	run, repo := e.WorkflowRun, e.Repository.FullName
	what := fmt.Sprintf("run %d of %s in %s", run.ID, run.Path, repo)
	switch {
	case e.Action != "completed":
		return settle(store.EventIgnored, ReasonNotCompleted, "%s is %s", what, e.Action), nil
	case run.ID < 1 || run.RunAttempt < 0:
		return settle(store.EventFailed, ReasonUnparseable, "%s names attempt %d", what, run.RunAttempt), nil
	}
	owned, err := p.Store.RepositoryIsOwned(repo)
	if err != nil || !owned {
		return settle(store.EventIgnored, ReasonNotOwned, "%s, which is outside the declared scope", what), err
	}
	added, err := p.Store.RecordWorkflowRun(store.WorkflowRun{
		Repository: repo, RunID: run.ID, Attempt: run.RunAttempt, WorkflowPath: run.Path,
		HeadBranch: run.HeadBranch, HeadSHA: run.HeadSHA, Event: run.Event, Conclusion: run.Conclusion,
		CompletedAt: run.UpdatedAt, Referenced: run.Referenced,
	})
	if err != nil {
		return outcome{}, err
	}
	said := "recorded"
	if !added {
		said = "already recorded"
	}
	recorded := settle(store.EventDone, ReasonRunRecorded, "%s %s at %s, %s, calling %d workflows",
		what, run.Conclusion, run.HeadSHA, said, len(run.Referenced))
	if !strings.HasPrefix(run.Path, codeScanningRuns) {
		return recorded, nil
	}
	checked, err := p.recheckDatabase(ctx, repo, what)
	checked.detail = recorded.detail + "; " + checked.detail
	return p.rereadSetup(ctx, repo, checked), err
}

// appDeleted records that the App is gone: its hook and installations went with it, so no event
// follows this one and its tokens stop working.
func (p *Processor) appDeleted(d store.EventDelivery) (outcome, error) {
	var e struct {
		Action string `json:"action"`
		HookID int64  `json:"hook_id"`
	}
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	if e.Action != "deleted" {
		return settle(store.EventIgnored, ReasonUnhandled, "meta %s is not handled", e.Action), nil
	}
	if p.Installations != nil {
		p.Installations()
	}
	return settle(store.EventDone, ReasonAppDeleted,
		"the App and its webhook %d were deleted; no event follows, and its tokens stop working", e.HookID), nil
}

func (p *Processor) push(ctx context.Context, d store.EventDelivery) (outcome, error) {
	var e pushEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	repo := e.Repository.FullName
	if o, ok, err := p.ours(repo); !ok || err != nil {
		return o, err
	}
	branch, isBranch := strings.CutPrefix(e.Ref, "refs/heads/")
	tag, isTag := strings.CutPrefix(e.Ref, "refs/tags/")
	switch {
	case branch == "" && tag == "":
		return settle(store.EventIgnored, ReasonNotBranch, "push to %s of %s", e.Ref, repo), nil
	case e.Deleted:
		return settle(store.EventIgnored, ReasonRefDeleted,
			"%s of %s was deleted; its delete event acts on it", e.Ref, repo), nil
	case isTag:
		return p.probeReferences(ctx, repo, tag, e.After, 0, "push to tag "+tag)
	case !isBranch:
		return settle(store.EventIgnored, ReasonNotBranch, "push to %s of %s", e.Ref, repo), nil
	}
	row, o, err := p.liveRow(repo, branch)
	if err != nil {
		return outcome{}, err
	}
	// A reference elsewhere to this branch stands at a row of its own, and the poller leaves it to events.
	rows, err := p.Store.QueueRowsAt(repo, branch)
	if err != nil {
		return outcome{}, err
	}
	if o.status == "" && !slices.ContainsFunc(rows, func(r store.QueueRow) bool {
		return r.NodeKey == row.NodeKey && r.Ref == row.Ref
	}) {
		rows = append([]store.QueueRow{row}, rows...)
	}
	if len(rows) == 0 {
		return o, nil
	}
	return p.probeRows(ctx, rows, e.After)
}

// precedence orders what an event did to the rows it reached; the delivery reports the weightiest.
var precedence = []string{ReasonUnchanged, ReasonBranchesRefreshed, ReasonExpedited, ReasonRequeued,
	ReasonBack, ReasonReferenceGone, ReasonProbeFailed, ReasonBranchReadFailed}

// weightier is the outcome that says more, carrying what both said.
func weightier(a, b outcome) outcome {
	out := a
	if slices.Index(precedence, b.reason) > slices.Index(precedence, a.reason) {
		out = b
	}
	out.detail = a.detail + "; " + b.detail
	return out
}

// probeRows probes every row standing at a reference, asking once per reference and comparing once
// per commit the rows were analyzed at.
func (p *Processor) probeRows(ctx context.Context, rows []store.QueueRow, claimed string) (outcome, error) {
	var out outcome
	resolved, reviewed := map[string]probed{}, map[string]review{}
	for i, row := range rows {
		o, err := p.probe(ctx, row, claimed, resolved, reviewed)
		if err != nil || o.status == "" {
			return o, err
		}
		if i == 0 {
			out = o
			continue
		}
		out = weightier(out, o)
	}
	return out, nil
}

// probeReferences probes the rows standing at a reference at or below a depth: another
// repository's reference to it reads it through a row of its own, which polling leaves to events.
func (p *Processor) probeReferences(
	ctx context.Context, repo, ref, claimed string, depth int, what string,
) (outcome, error) {
	rows, err := p.Store.QueueRowsAt(repo, ref)
	if err != nil {
		return outcome{}, err
	}
	rows = slices.DeleteFunc(rows, func(r store.QueueRow) bool { return r.Depth < depth })
	if len(rows) == 0 {
		return settle(store.EventIgnored, ReasonNotReferenced, "%s: nothing queued stands at %s of %s",
			what, ref, repo), nil
	}
	return p.probeRows(ctx, rows, claimed)
}

// liveRow finds the depth-0 row a branch is live at: the default branch's, or a protected branch's.
func (p *Processor) liveRow(repo, branch string) (store.QueueRow, outcome, error) {
	defaultBranch, known, err := p.Store.DefaultBranch(repo)
	if err != nil {
		return store.QueueRow{}, outcome{}, err
	}
	if !known {
		return store.QueueRow{}, settle(store.EventIgnored, ReasonDefaultUnknown,
			"%s has no default branch on record, so which of its branches is live is unknown",
			repo), nil
	}
	ref := branch
	if branch == defaultBranch {
		ref = ""
	}
	row, found, err := p.Store.QueueRow(repo, ref)
	if err != nil {
		return store.QueueRow{}, outcome{}, err
	}
	switch {
	case found && row.Depth == 0:
		return row, outcome{}, nil
	case ref == "":
		return store.QueueRow{}, settle(store.EventIgnored, ReasonNotQueued,
			"%s is %s's default branch and nothing is queued for it", branch, repo), nil
	default:
		return store.QueueRow{}, settle(store.EventIgnored, ReasonNotLive,
			"%s is not %s's default branch (%s) and is not queued as a protected branch",
			branch, repo, defaultBranch), nil
	}
}

// probed is one reference's answer, kept so rows standing at it ask once.
type probed struct {
	sha string
	err error
}

// probe acts on where the reference points now; the payload's claim is recorded and decides nothing.
func (p *Processor) probe(
	ctx context.Context, row store.QueueRow, claimed string, resolved map[string]probed,
	reviewed map[string]review,
) (outcome, error) {
	where := row.NodeKey + "@" + row.Ref
	if row.Ref == "" {
		where = row.NodeKey + " (default branch)"
	}
	if row.Status == frontier.StatusRunning {
		return outcome{reason: ReasonAnalysisRunning,
			detail: where + " is being analyzed; probed once that settles"}, nil
	}
	if p.Probe == nil {
		return settle(store.EventFailed, ReasonProbeFailed, "%s: this server has no probe", where), nil
	}
	owner, repo := row.Owner, row.Repo
	if owner == "" || repo == "" {
		owner, repo, _ = strings.Cut(row.NodeKey, "/")
	}
	asked := owner + "/" + repo + "@" + row.Ref
	got, known := resolved[asked]
	if !known {
		if err := p.Pace.Take(ctx); err != nil {
			return outcome{}, err
		}
		got.sha, got.err = p.Probe.Resolve(ctx, owner, repo, row.Ref)
		if got.err == nil && got.sha == "" {
			got.err = errors.New("the probe named no commit")
		}
		resolved[asked] = got
	}
	key := frontier.Key{NodeKey: row.NodeKey, Ref: row.Ref}
	// A failed probe settles the delivery; it is not a failure to process the log. Only a repository
	// answering that it holds no such reference says the reference is gone.
	if failed := got.err; failed != nil {
		if !expand.IsReferenceMissing(failed) {
			return settle(store.EventFailed, ReasonProbeFailed, "probing %s: %v", where, failed), nil
		}
		// What the recheck sweep would conclude a day later: the reference is gone.
		if err := p.Queue.Unavailable(key, failed.Error()); err != nil {
			return outcome{}, err
		}
		return settle(store.EventDone, ReasonReferenceGone, "%s: %v", where, failed), nil
	}
	sha := got.sha

	analyzed := "never analyzed"
	if row.ResolvedSHA != "" {
		analyzed = "analyzed at " + row.ResolvedSHA
	}
	said := fmt.Sprintf("%s: probe found %s, %s, the payload named %s",
		where, sha, analyzed, orNone(claimed))
	// A reference marked gone that answers again is back, whatever commit it names.
	if sha == row.ResolvedSHA && row.Status != frontier.StatusUnavailable {
		if row.Status == frontier.StatusDone || row.Status == frontier.StatusNothing {
			if err := p.Queue.Touch(key); err != nil {
				return outcome{}, err
			}
		}
		return settle(store.EventDone, ReasonUnchanged, "%s", said), nil
	}
	reason := ReasonExpedited
	if row.Status != frontier.StatusPending {
		if err := p.Queue.MarkStale(key, sha); err != nil {
			return outcome{}, err
		}
		reason = ReasonRequeued
	}
	if row.Status == frontier.StatusUnavailable {
		reason = ReasonBack
	}
	// Before the expedite and the wake, so a cycle the wake starts claims in the order it sets.
	r, err := p.reviewDependencies(ctx, owner+"/"+repo, row.ResolvedSHA, sha, reviewed)
	if err != nil {
		return outcome{}, err
	}
	if r.read {
		if err := p.Queue.ReviewDependencies(key, r.moved); err != nil {
			return outcome{}, err
		}
	}
	if _, err := p.Queue.ExpediteRefs([]frontier.NodeRef{{NodeKey: row.NodeKey, Ref: row.Ref}}); err != nil {
		return outcome{}, err
	}
	p.wake()
	return settle(store.EventDone, reason, "%s%s", said, r.detail), nil
}

// wake starts a cycle for work an event queued; wakes coalesce, so one per row costs nothing.
func (p *Processor) wake() {
	if p.Wake != nil {
		p.Wake()
	}
}

func orNone(sha string) string {
	if sha == "" {
		return "nothing"
	}
	return sha
}

func (p *Processor) refChange(ctx context.Context, d store.EventDelivery) (outcome, error) {
	var e refEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	repo := e.Repository.FullName
	if o, ok, err := p.ours(repo); !ok || err != nil {
		return o, err
	}
	what := d.Event + " of " + e.RefType + " " + e.Ref
	switch e.RefType {
	case "tag":
		return p.probeReferences(ctx, repo, e.Ref, "", 0, what)
	case "branch":
	default:
		return settle(store.EventIgnored, ReasonNotBranch, "%s in %s", what, repo), nil
	}
	// Protection decides the branch's own rows; references to it from elsewhere are probed.
	refreshed, err := p.refreshBranches(ctx, repo, what)
	if err != nil {
		return outcome{}, err
	}
	rows, err := p.Store.QueueRowsAt(repo, e.Ref)
	if err != nil || !slices.ContainsFunc(rows, func(r store.QueueRow) bool { return r.Depth > 0 }) {
		return refreshed, err
	}
	probed, err := p.probeReferences(ctx, repo, e.Ref, "", 1, what)
	if err != nil || probed.status == "" {
		return probed, err
	}
	return weightier(refreshed, probed), nil
}

func (p *Processor) protectionChange(ctx context.Context, d store.EventDelivery) (outcome, error) {
	var e actionEvent
	if o, ok := decode(d, &e); !ok {
		return o, nil
	}
	if e.Repository == nil || e.Repository.FullName == "" {
		org := ""
		if e.Organization != nil {
			org = e.Organization.Login
		}
		var r rulesetEvent
		if err := json.Unmarshal(d.Payload, &r); err != nil {
			// A full listing re-reads what each of the organization's repositories protects.
			return p.listAccountWhole(fmt.Sprintf("%s %s across organization %s", d.Event, e.Action, org), org)
		}
		return p.organizationRuleset(ctx, d, r, org)
	}
	repo := e.Repository.FullName
	if o, ok, err := p.ours(repo); !ok || err != nil {
		return o, err
	}
	return p.refreshBranches(ctx, repo, d.Event+" "+e.Action)
}

// refreshBranches re-reads protection with enumeration's reader; a failed read records nothing.
func (p *Processor) refreshBranches(ctx context.Context, repo, what string) (outcome, error) {
	if p.Branches == nil {
		return settle(store.EventFailed, ReasonBranchReadFailed,
			"%s: this server has no branch reader", what), nil
	}
	branches, err := p.Branches(ctx, repo)
	if err != nil {
		return settle(store.EventFailed, ReasonBranchReadFailed,
			"%s: reading %s's protected branches: %v; the recorded answer stands", what, repo, err), nil
	}
	queue, err := p.Store.SetProtectedBranches(repo, branches)
	if err != nil {
		return outcome{}, err
	}
	if queue.Queued > 0 {
		p.wake()
	}
	return settle(store.EventDone, ReasonBranchesRefreshed,
		"%s: %s protects %d branches [%s]; queued %d, removed %d",
		what, repo, len(branches), strings.Join(branches, ", "), queue.Queued, queue.Removed), nil
}

// notActedOn names lifecycle and installation events; the listing and the sweep notice them, late.
func notActedOn(d store.EventDelivery) outcome {
	var e actionEvent
	if o, ok := decode(d, &e); !ok {
		return o
	}
	subject := ""
	switch {
	case e.Repository != nil:
		subject = e.Repository.FullName
	case e.Installation != nil:
		subject = e.Installation.Account.Login
	}
	return settle(store.EventIgnored, ReasonNotActedOn, "%s %s %s", d.Event, e.Action, subject)
}
