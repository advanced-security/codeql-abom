// Package enumerate turns declared accounts into repositories the corpus holds.
package enumerate

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// DefaultFullListingEvery is how often every page of an account is read.
const DefaultFullListingEvery = 24 * time.Hour

// Report is what declaring an account turned out to mean.
//
// Declaring names accounts; enumerating turns them into repositories. Until it runs, a declaration
// changes what the corpus would accept and nothing about what it holds — which is how an operator
// can declare every account they own and still be shown an empty report.
type Report struct {
	// Added is repositories the corpus had never seen. Promoted is repositories it held only as
	// somebody else's dependency and now holds as the operator's own. They are separate because
	// a corpus that is entirely promotions has found nothing new, and one that is entirely
	// additions was not crawling the operator's work at all.
	Added    int `json:"added"`
	Promoted int `json:"promoted"`
	// Unchanged is repositories already recorded as the operator's, which is what a second run
	// should be almost entirely made of.
	Unchanged int `json:"unchanged"`

	Accounts []AccountEnumeration `json:"accounts"`
	// Failed names the accounts that could not be reached, so that a short report is read as an
	// incomplete answer rather than a small estate.
	Failed []AccountFailure `json:"failed"`

	// Queued is repositories put in the analysis queue. Recording a repository is not analyzing
	// it, and a corpus full of rows nothing will ever read looks the same as an empty one.
	Queued int `json:"queued"`
	// QueueError is set when the repositories were recorded and could not be queued. They are
	// two facts and the declaration survives either way.
	QueueError string `json:"queueError,omitempty"`
}

// AccountEnumeration is one declared account's contribution.
type AccountEnumeration struct {
	Owner string `json:"owner"`
	Found int    `json:"found"`
	// Promoted counts repositories this account owns that the corpus had only as a dependency.
	Added    int `json:"added"`
	Promoted int `json:"promoted"`

	// Route names which API answered, and Fallback why the preferred one did not. Recorded
	// rather than inferred, so a figure that differs between routes can be attributed.
	Route    string `json:"route,omitempty"`
	Fallback string `json:"fallback,omitempty"`
	// Full says whether every page was read. A sweep reads a prefix of a sorted order, which is
	// cheaper and cannot see a deletion.
	Full bool `json:"full"`
	// Missing counts repositories the corpus holds for this account that the read did not
	// mention. MissingKnown is false after a sweep: a prefix can never establish absence, and
	// reporting zero there would say nothing is gone when the truth is that nobody looked.
	Missing      int  `json:"missing"`
	MissingKnown bool `json:"missingKnown"`

	// PropertiesUnjudged counts repositories the read named whose values it did not carry whole,
	// in an account a property scope decides. They are neither taken in nor let go: unknown is
	// not a mismatch.
	PropertiesUnjudged int `json:"propertiesUnjudged"`
	// Left counts owned repositories this read showed a property scope no longer covers. What came
	// in is Added or Promoted; this is the other direction, which nothing else would report.
	Left int `json:"left"`
	// PropertiesRead says the custom property values arrived with the listing. False leaves the
	// grouping unknown rather than empty — a token that cannot read them is a permission problem,
	// not an organization that divides its repositories into nothing.
	PropertiesRead bool `json:"propertiesRead"`
	// PropertiesUnread says why they did not, when they did not.
	PropertiesUnread string `json:"propertiesUnread,omitempty"`
	// PropertiesTruncated counts repositories whose values could not be carried whole. Their
	// stored values are left as they were, because half a dividing line is a placement nobody
	// chose.
	PropertiesTruncated int `json:"propertiesTruncated"`
	// SchemaRead says the organization's property definitions were read, which is what separates
	// the bucket a repository lands in by choosing nothing from a unit somebody joined.
	SchemaRead bool `json:"schemaRead"`
	// SchemaUnread says why they were not.
	SchemaUnread string `json:"schemaUnread,omitempty"`

	// BranchesRead counts repositories whose protected branches were read and recorded, and
	// ProtectedBranches the branches they named. BranchesUnread counts reads that failed, which left
	// those repositories as they were: a read that failed is not a repository protecting nothing.
	BranchesRead      int    `json:"branchesRead"`
	ProtectedBranches int    `json:"protectedBranches"`
	BranchesUnread    int    `json:"branchesUnread"`
	BranchesUnreadWhy string `json:"branchesUnreadWhy,omitempty"`
	// BranchesQueued and BranchesDequeued are what recording them did to the analysis queue: a
	// protected branch is analyzed as its own row, and one that lost protection stops being.
	BranchesQueued   int `json:"branchesQueued"`
	BranchesDequeued int `json:"branchesDequeued"`

	// SetupRead counts owned repositories whose code scanning setup was read and recorded;
	// SetupUnread those whose read failed and kept what was recorded before.
	SetupRead      int    `json:"setupRead"`
	SetupUnread    int    `json:"setupUnread"`
	SetupUnreadWhy string `json:"setupUnreadWhy,omitempty"`
	// ConfigurationsRead says the account's code security configurations were read whole, which
	// only a full listing asks.
	ConfigurationsRead   bool   `json:"configurationsRead"`
	ConfigurationsUnread string `json:"configurationsUnread,omitempty"`
}

// AccountFailure is an account that could not be enumerated, named with the reason.
type AccountFailure struct {
	Owner string `json:"owner"`
	Error string `json:"error"`
}

// Read lists an account, bounded by what a previous read established. Zero marks read everything.
type Read func(ctx context.Context, account string, since catalog.Marks) (catalog.Listing, error)

// ReadSchema reads what an account declares its custom properties to be. Nil skips it, which leaves
// whether a value is the organization's default unknown rather than assumed either way.
type ReadSchema func(ctx context.Context, account string) ([]catalog.PropertyDefinition, error)

// ReadBranches names the branches one repository protects. One request per repository, so it is
// asked only of what a listing named; see rememberBranches.
type ReadBranches func(ctx context.Context, repository string) ([]string, error)

// ReadRepository reads one repository as a listing that names it alone.
type ReadRepository func(ctx context.Context, fullName string) (catalog.Listing, error)

// Options is one enumeration run.
type Options struct {
	Store *store.Store
	Read  Read
	// Schema is read once per account, not per repository. Without it the bucket a repository
	// lands in by choosing nothing looks like a unit somebody joined.
	Schema ReadSchema
	// Branches is read per owned repository the listing named. Nil leaves what was recorded as it
	// was, since a repository nobody asked is not one that protects nothing.
	Branches ReadBranches
	// QueueOwned may be nil, which records without analyzing.
	QueueOwned func() (int, error)
	// FullListingEvery overrides DefaultFullListingEvery. Sweeping forever is how a corpus keeps
	// dead repositories while looking more current than one that reads everything.
	FullListingEvery time.Duration
	// Owners names the declared accounts to list as their marks allow. A run naming nothing lists
	// every enabled declaration.
	Owners []string
	// Full names declared accounts to list whole whatever their marks say: only that records what
	// is gone, and a deletion, an archive or a rename is exactly that.
	Full []string
	// Repositories names repositories to read one at a time, because an event named them and a
	// sweep would not. A run naming only repositories lists no account.
	Repositories []string
	// ReadRepository reads one named repository. Nil leaves them to the next listing.
	ReadRepository ReadRepository
	// Setup reads each owned repository a listing named. Nil leaves what was recorded as it was.
	Setup ReadSetup
	// Configurations reads an account's code security configurations on a full listing.
	Configurations ReadConfigurations
	// now exists so a test can move the clock rather than wait for it.
	now func() time.Time
}

func (o Options) clock() time.Time {
	if o.now == nil {
		return time.Now().UTC()
	}
	return o.now()
}

func (o Options) period() time.Duration {
	if o.FullListingEvery <= 0 {
		return DefaultFullListingEvery
	}
	return o.FullListingEvery
}

// Run records every enabled declaration's repositories, then queues what is now owned.
func Run(ctx context.Context, o Options) (Report, error) {
	declared, err := o.Store.OwnedScope()
	if err != nil {
		return Report{}, err
	}

	report := Report{Accounts: []AccountEnumeration{}, Failed: []AccountFailure{}}
	for _, account := range declared {
		// A declaration that is recorded but switched off is an intention, not an instruction.
		if !account.Enabled || !o.lists(account.Owner) {
			continue
		}
		since, err := o.marksFor(account)
		if err != nil {
			return Report{}, err
		}
		listing, err := o.Read(ctx, account.Owner, since)
		if err != nil {
			// One unreachable account must not discard the ones that answered: enumeration is
			// several independent calls and a partial answer is still worth keeping.
			report.Failed = append(report.Failed, AccountFailure{
				Owner: account.Owner, Error: err.Error()})
			continue
		}
		// Before recording, because the schema decides which property scopes count: read after,
		// a scope saved a moment ago would take nothing in until the next cycle.
		var schema AccountEnumeration
		if err := o.rememberSchema(ctx, account.Owner, &schema); err != nil {
			return Report{}, err
		}
		if account.HasProperties() {
			judged := []store.DeclaredAccount{account}
			if err := o.Store.JudgePropertyScopes(judged); err != nil {
				return Report{}, err
			}
			account = judged[0]
		}

		one, listed, err := record(o.Store, account, listing, &report, o.clock())
		if err != nil {
			return Report{}, err
		}
		if err := o.rememberBranches(ctx, listed, &one); err != nil {
			return Report{}, err
		}
		if err := o.rememberSetup(ctx, listed, &one); err != nil {
			return Report{}, err
		}
		if listing.Full {
			o.rememberConfigurations(ctx, account.Owner, &one)
		}
		one.SchemaRead, one.SchemaUnread = schema.SchemaRead, schema.SchemaUnread
		if err := o.remember(account.Owner, listing); err != nil {
			return Report{}, err
		}
		report.Accounts = append(report.Accounts, one)
	}
	if err := o.readRepositories(ctx, declared, &report); err != nil {
		return Report{}, err
	}

	// After recording, not beside it: queueing first would enqueue the previous enumeration and
	// miss everything this one found.
	if o.QueueOwned != nil {
		queued, err := o.QueueOwned()
		if err != nil {
			report.QueueError = err.Error()
		} else {
			report.Queued = queued
		}
	}
	return report, nil
}

// lists reports whether this run lists an account: one it names, or every one when it names nothing.
func (o Options) lists(owner string) bool {
	if len(o.Owners)+len(o.Full)+len(o.Repositories) == 0 {
		return true
	}
	return named(o.Owners, owner) || named(o.Full, owner)
}

func named(names []string, owner string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, owner) })
}

// readRepositories reads each named repository a declaration covers. Not a full listing, so it
// calls nothing missing, and no marks move: one repository says nothing about the rest.
func (o Options) readRepositories(ctx context.Context, declared []store.DeclaredAccount, report *Report) error {
	if o.ReadRepository == nil {
		return nil
	}
	for _, fullName := range o.Repositories {
		owner, repo, _ := strings.Cut(fullName, "/")
		// A property scope can only be judged from the repository's values, so every repository
		// of an account holding one is read; otherwise only what the declaration already names.
		i := slices.IndexFunc(declared, func(a store.DeclaredAccount) bool {
			return a.Enabled && strings.EqualFold(a.Owner, owner) &&
				(a.HasProperties() || a.Covers(owner, repo, nil))
		})
		if i < 0 {
			continue
		}
		listing, err := o.ReadRepository(ctx, fullName)
		if err != nil {
			report.Failed = append(report.Failed, AccountFailure{Owner: fullName, Error: err.Error()})
			continue
		}
		one, listed, err := record(o.Store, declared[i], listing, report, o.clock())
		if err != nil {
			return err
		}
		if err := o.rememberBranches(ctx, listed, &one); err != nil {
			return err
		}
		if err := o.rememberSetup(ctx, listed, &one); err != nil {
			return err
		}
		report.Accounts = append(report.Accounts, one)
	}
	return nil
}

// marksFor decides how much of an account this run reads.
//
// Zero marks mean a full listing, which is the answer in four cases: an account an event asked to
// have listed whole, one nobody has read, one whose last full listing has aged out, and one small
// enough that reading it whole is cheaper than sweeping it. Only a full listing can establish that a repository is gone, so a design that
// sweeps forever keeps dead repositories and looks current doing it.
func (o Options) marksFor(account store.DeclaredAccount) (catalog.Marks, error) {
	owner := account.Owner
	if named(o.Full, owner) {
		return catalog.Marks{}, nil
	}
	state, known, err := o.Store.EnumerationState(owner)
	if err != nil || !known {
		return catalog.Marks{}, err
	}
	if state.ListedAt.IsZero() || o.clock().Sub(state.ListedAt) >= o.period() {
		return catalog.Marks{}, nil
	}
	// A sweep is two ordered walks, so an account whose whole listing fits in one page is cheaper
	// read whole. That read also answers what is gone, which a sweep cannot.
	if state.ListedPages <= sweepWorthIt {
		return catalog.Marks{}, nil
	}
	// A property scope saved since the last full listing has never been judged over the whole
	// account, and a sweep reads only what moved, which retagging does not.
	if account.HasProperties() && scopeNewerThan(account, state.ListedAt) {
		return catalog.Marks{}, nil
	}
	return catalog.Marks{HighCreated: state.HighCreated, HighPushed: state.HighPushed}, nil
}

// scopeNewerThan reports whether any property scope was declared after t. One whose time cannot be
// read counts as newer, which costs a full listing rather than a scope nobody judged.
func scopeNewerThan(account store.DeclaredAccount, t time.Time) bool {
	for _, c := range account.Properties {
		at, err := time.Parse(time.RFC3339Nano, c.DeclaredAt)
		if err != nil || at.After(t) {
			return true
		}
	}
	return false
}

// sweepWorthIt is the page count at or below which a full listing is the cheaper read.
const sweepWorthIt = 2

// remember writes back what the read established, as the kind of read it was.
func (o Options) remember(owner string, listing catalog.Listing) error {
	if listing.Full {
		return o.Store.RecordFullListing(owner, listing.Route, listing.Pages,
			listing.Marks.HighCreated, listing.Marks.HighPushed)
	}
	return o.Store.RecordSweep(
		owner, listing.Route, listing.Marks.HighCreated, listing.Marks.HighPushed)
}

// record writes one account's repositories, counting each as new, promoted, or already known, and
// names the ones the declaration covers.
func record(
	s *store.Store, account store.DeclaredAccount, listing catalog.Listing, report *Report, at time.Time,
) (AccountEnumeration, []string, error) {
	one := AccountEnumeration{
		Owner:            account.Owner,
		Found:            len(listing.Repositories),
		Route:            listing.Route,
		Fallback:         listing.Fallback,
		Full:             listing.Full,
		PropertiesRead:   listing.PropertiesRead,
		PropertiesUnread: listing.PropertiesUnread,
	}
	seen := make([]string, 0, len(listing.Repositories))
	// Every repository the read named, which is what absence is measured against: one a property
	// scope let go still exists, and one whose values were unreadable was not judged at all.
	named := make([]string, 0, len(listing.Repositories))
	var forget []string
	facts := make([]store.ListedFacts, 0, len(listing.Repositories))
	for _, repository := range listing.Repositories {
		fullName := repository.FullName
		owner, repo, ok := strings.Cut(fullName, "/")
		if ok {
			named = append(named, fullName)
		}
		values, known := listedValues(repository, listing.PropertiesRead)
		// An includes-only declaration covers the named repositories and no others, so
		// enumerating the account must not smuggle the rest in.
		if !ok || !account.Covers(owner, repo, values) {
			one.Found--
			if ok && account.HasProperties() {
				if known {
					// Values are kept for owned repositories alone; these decided only that it is not.
					forget = append(forget, fullName)
				} else {
					one.PropertiesUnjudged++
				}
			}
			continue
		}
		seen = append(seen, fullName)
		facts = append(facts, store.ListedFacts{Repository: fullName, Fork: repository.Fork,
			Visibility: repository.Visibility, HasWorkflows: repository.HasWorkflows,
			HasActionsDir: repository.HasActionsDir, HasRootAction: repository.HasRootAction})
		before, err := s.RepositoryOrigin(fullName)
		if err != nil {
			return one, nil, err
		}
		if _, err := s.InsertOwnedRepository(fullName); err != nil {
			return one, nil, err
		}
		// Only when GitHub named one. An unstated default branch stays unstated rather than
		// becoming a guess about which branch this repository builds from.
		if repository.DefaultBranch != "" {
			if err := s.SetDefaultBranch(fullName, repository.DefaultBranch); err != nil {
				return one, nil, err
			}
		}
		if err := recordProperties(s, repository, listing.PropertiesRead, &one); err != nil {
			return one, nil, err
		}
		switch before {
		case "":
			one.Added++
			report.Added++
		case store.OriginUpstream:
			one.Promoted++
			report.Promoted++
		default:
			report.Unchanged++
		}
	}

	// Counted before the values go, while the predicate can still say they were owned.
	if len(forget) > 0 {
		was, err := s.OwnedAmong(forget)
		if err != nil {
			return one, nil, err
		}
		one.Left = len(was)
	}
	if err := s.ForgetPropertyValues(forget); err != nil {
		return one, nil, err
	}
	if err := s.RecordListedFacts(facts, at); err != nil {
		return one, nil, err
	}
	// Named and never removed here: what a missing repository alone reached is what the prune in
	// Settings offers, which says what it would take before anybody accepts it. A listing that
	// under-reports without failing would otherwise take out real evidence on a timer.
	missing, err := s.RecordRepositoryListing(account.Owner, named, listing.Full)
	if err != nil {
		return one, nil, err
	}
	one.Missing, one.MissingKnown = missing, listing.Full
	// Only a full listing judged the whole account; a sweep's count would describe a prefix.
	if listing.Full && account.HasProperties() {
		if err := s.RecordPropertyJudgement(account.Owner, one.PropertiesUnjudged); err != nil {
			return one, nil, err
		}
	}
	return one, seen, nil
}

// listedValues are a repository's values as this read carried them, or nil and false when it did
// not carry them whole.
func listedValues(r catalog.Repository, read bool) ([]store.PropertyValue, bool) {
	if !read || r.PropertiesTruncated {
		return nil, false
	}
	out := make([]store.PropertyValue, 0, len(r.Properties))
	for _, p := range r.Properties {
		out = append(out, store.PropertyValue{Name: p.Name, Value: p.Value})
	}
	return out, true
}

// rememberSchema records what the account declares its properties to be.
//
// A failure is reported and not fatal: an unread schema labels no bucket as the default and holds
// the account's property scopes, and losing the whole enumeration over it would trade a large
// fact for a small one.
func (o Options) rememberSchema(ctx context.Context, owner string, one *AccountEnumeration) error {
	if o.Schema == nil {
		return nil
	}
	defs, why := schemaOrReason(ctx, o.Schema, owner)
	if why != "" {
		one.SchemaUnread = why
		return nil
	}
	stored := make([]store.PropertyDefinition, 0, len(defs))
	for _, d := range defs {
		stored = append(stored, store.PropertyDefinition{
			Name: d.Name, HasDefault: d.HasDefault, DefaultValue: d.DefaultValue, DefaultValues: d.DefaultValues,
			ValueType: d.ValueType, ValuesEditableBy: d.ValuesEditableBy})
	}
	one.SchemaRead = true
	return o.Store.ReplacePropertySchema(owner, stored)
}

// schemaOrReason turns a refused schema read into a reason rather than an error, because not
// knowing the organization's defaults is a smaller fact than the listing it arrived with.
func schemaOrReason(
	ctx context.Context, read ReadSchema, owner string,
) ([]catalog.PropertyDefinition, string) {
	defs, err := read(ctx, owner)
	if err != nil {
		return nil, err.Error()
	}
	return defs, ""
}

// recordProperties writes what the organization says a repository is.
//
// Nothing is written when the read did not carry the values: overwriting with an empty set would
// turn "nobody could ask" into "the organization puts this in no unit", and the second is a claim.
// A truncated answer is treated the same way, because half a dividing line places a repository
// somewhere the organization did not.
func recordProperties(
	s *store.Store, repository catalog.Repository, read bool, one *AccountEnumeration,
) error {
	if !read {
		return nil
	}
	if repository.PropertiesTruncated {
		one.PropertiesTruncated++
		return nil
	}
	values := make([]store.PropertyValue, 0, len(repository.Properties))
	for _, p := range repository.Properties {
		values = append(values, store.PropertyValue{Name: p.Name, Value: p.Value})
	}
	return s.ReplacePropertyValues(repository.FullName, values)
}
