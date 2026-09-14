// Package enumerate turns declared accounts into repositories the corpus holds.
//
// Extracted from the HTTP handler so that a person clicking a button and a loop running on a timer
// take the same path. The rule worth protecting is `DeclaredAccount.Covers`: an includes-only
// declaration covers the repositories it names and no others, and a second implementation of that
// is how an account smuggles the rest of itself in.
package enumerate

import (
	"context"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// DefaultFullListingEvery is how often every page of an account is read.
//
// A trade rather than a tuning: reads spent, against how long a repository that no longer exists is
// still reported as the operator's. A day is short enough that a deletion is stale for one cycle of
// a working week and long enough that a twenty-thousand repository account pays for it once.
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

// Options is one enumeration run.
type Options struct {
	Store *store.Store
	Read  Read
	// Schema is read once per account, not per repository. Without it the bucket a repository
	// lands in by choosing nothing looks like a unit somebody joined.
	Schema ReadSchema
	// QueueOwned may be nil, which records without analyzing.
	QueueOwned func() (int, error)
	// FullListingEvery overrides DefaultFullListingEvery. Sweeping forever is how a corpus keeps
	// dead repositories while looking more current than one that reads everything.
	FullListingEvery time.Duration
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
		if !account.Enabled {
			continue
		}
		since, err := o.marksFor(account.Owner)
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

		one, err := record(o.Store, account, listing, &report)
		if err != nil {
			return Report{}, err
		}
		if err := o.rememberSchema(ctx, account.Owner, &one); err != nil {
			return Report{}, err
		}
		if err := o.remember(account.Owner, listing); err != nil {
			return Report{}, err
		}
		report.Accounts = append(report.Accounts, one)
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

// marksFor decides how much of an account this run reads.
//
// Zero marks mean a full listing, which is the answer in three cases: an account nobody has read,
// one whose last full listing has aged out, and one small enough that reading it whole is cheaper
// than sweeping it. Only a full listing can establish that a repository is gone, so a design that
// sweeps forever keeps dead repositories and looks current doing it.
func (o Options) marksFor(owner string) (catalog.Marks, error) {
	state, known, err := o.Store.EnumerationState(owner)
	if err != nil || !known {
		return catalog.Marks{}, err
	}
	if state.ListedAt.IsZero() || o.clock().Sub(state.ListedAt) >= o.period() {
		return catalog.Marks{}, nil
	}
	// A sweep is two ordered walks, so it costs two reads at minimum. An account whose whole
	// listing fit in one is cheaper read whole — and that read also answers what is gone, which
	// the sweep cannot. Measured on a 27 repository organization: 1 point against 2.
	if state.ListedPages <= sweepWorthIt {
		return catalog.Marks{}, nil
	}
	return catalog.Marks{HighCreated: state.HighCreated, HighPushed: state.HighPushed}, nil
}

// sweepWorthIt is the page count at or below which a full listing is the cheaper read.
//
// Two, because a sweep is two ordered walks of at least one page each. The saving only begins for
// an account that takes three or more pages to list, which is three hundred repositories.
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

// record writes one account's repositories, counting each as new, promoted, or already known.
func record(
	s *store.Store, account store.DeclaredAccount, listing catalog.Listing, report *Report,
) (AccountEnumeration, error) {
	one := AccountEnumeration{
		Owner:            account.Owner,
		Found:            len(listing.Repositories),
		Route:            listing.Route,
		Fallback:         listing.Fallback,
		Full:             listing.Full,
		PropertiesRead:   listing.PropertiesRead,
		PropertiesUnread: listing.PropertiesUnread,
	}
	seen := make(map[string]struct{}, len(listing.Repositories))
	for _, repository := range listing.Repositories {
		fullName := repository.FullName
		owner, repo, ok := strings.Cut(fullName, "/")
		// An includes-only declaration covers the named repositories and no others, so
		// enumerating the account must not smuggle the rest in.
		if !ok || !account.Covers(owner, repo) {
			one.Found--
			continue
		}
		seen[fullName] = struct{}{}
		before, err := s.RepositoryOrigin(fullName)
		if err != nil {
			return one, err
		}
		if _, err := s.InsertOwnedRepository(fullName); err != nil {
			return one, err
		}
		// Only when GitHub named one. An unstated default branch stays unstated rather than
		// becoming a guess about which branch this repository builds from.
		if repository.DefaultBranch != "" {
			if err := s.SetDefaultBranch(fullName, repository.DefaultBranch); err != nil {
				return one, err
			}
		}
		if err := recordProperties(s, repository, listing.PropertiesRead, &one); err != nil {
			return one, err
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

	if !listing.Full {
		return one, nil
	}
	missing, err := absentFrom(s, account.Owner, seen)
	if err != nil {
		return one, err
	}
	one.Missing, one.MissingKnown = missing, true
	return one, nil
}

// rememberSchema records what the account declares its properties to be.
//
// A failure is reported and not fatal: the schema only decides whether a bucket is labeled as the
// default, and losing the whole enumeration over that would trade a large fact for a small one.
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
			Name: d.Name, HasDefault: d.HasDefault, DefaultValue: d.DefaultValue})
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

// absentFrom counts repositories the corpus holds for an account that a full listing did not name.//
// Counted and not removed. Whether such a repository is pruned, kept as coverage or labeled
// orphaned is an open decision, and taking it here — silently, inside an enumeration — would
// destroy coverage nobody agreed to lose.
func absentFrom(s *store.Store, owner string, seen map[string]struct{}) (int, error) {
	held, err := s.OwnedRepositories()
	if err != nil {
		return 0, err
	}
	missing := 0
	for _, fullName := range held {
		account, _, ok := strings.Cut(fullName, "/")
		if !ok || !strings.EqualFold(account, owner) {
			continue
		}
		if _, present := seen[fullName]; !present {
			missing++
		}
	}
	return missing, nil
}
