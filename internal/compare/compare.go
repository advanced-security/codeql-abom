// Package compare sets a run's results against the code scanning alerts GitHub holds now.
//
// A comparison reads and records; it creates nothing. No alert, no finding and no write to any
// repository: what it keeps is a count and a list against the run it was asked about.
package compare

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/store"
)

// Reader is what a comparison reads from GitHub. A catalog.Client is one.
type Reader interface {
	CodeScanningAlerts(ctx context.Context, repository string) ([]catalog.Alert, error)
	ActionsAnalysis(ctx context.Context, repository, ref string) (catalog.Analysis, bool, error)
	HeadSHA(ctx context.Context, repository string) (string, error)
	CompareCommits(ctx context.Context, repository, base, head string) (catalog.Moved, error)
}

// Comparer carries out a recorded comparison.
type Comparer struct {
	Store *store.Store
	Read  Reader
	// Parallel is how many repositories are read at once; zero reads four.
	Parallel int
}

type comparisonWork struct {
	pending []store.DatabaseIdentity
	results map[store.DatabaseIdentity][]store.ComparedResult
	asked   map[string]bool
}

// Compare reads every member a comparison has still to read and records each as it is read, so a
// comparison stopped part way keeps what it established and says it was interrupted.
//
// Every member's results are read before GitHub is: reading them one at a time between paced
// requests would let the run analyzed again part way through mix two runs under one CodeQL.
func (c *Comparer) Compare(ctx context.Context, id int64) error {
	work, err := c.comparisonWork(id)
	if err != nil {
		return err
	}
	failure := c.compareMembers(ctx, id, work)
	return c.finishComparison(ctx, id, failure)
}

func (c *Comparer) comparisonWork(id int64) (comparisonWork, error) {
	comparison, err := c.Store.Comparison(id)
	if err != nil {
		return comparisonWork{}, err
	}
	pending, err := c.Store.PendingComparisonMembers(id)
	if err != nil {
		return comparisonWork{}, err
	}
	asked := map[string]bool{}
	for _, q := range comparison.Queries {
		asked[q.QueryID] = true
	}
	results := make(map[store.DatabaseIdentity][]store.ComparedResult, len(pending))
	for _, member := range pending {
		if results[member], err = c.Store.MemberResults(comparison.RunID, member); err != nil {
			return comparisonWork{}, err
		}
	}
	return comparisonWork{pending: pending, results: results, asked: asked}, nil
}

func (c *Comparer) compareMembers(ctx context.Context, id int64, cw comparisonWork) error {
	parallel := c.Parallel
	if parallel <= 0 {
		parallel = 4
	}
	work := make(chan store.DatabaseIdentity)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for member := range work {
				if err := c.member(ctx, id, member, cw.results[member], cw.asked); err != nil {
					mu.Lock()
					if failure == nil {
						failure = err
					}
					mu.Unlock()
				}
			}
		}()
	}
	for _, member := range cw.pending {
		if ctx.Err() != nil {
			break
		}
		work <- member
	}
	close(work)
	wg.Wait()
	return failure
}

func (c *Comparer) finishComparison(ctx context.Context, id int64, failure error) error {
	switch {
	case ctx.Err() != nil:
		if err := c.Store.InterruptComparison(id); err != nil {
			return err
		}
		return ctx.Err()
	case failure != nil:
		if err := c.Store.InterruptComparison(id); err != nil {
			return err
		}
		return failure
	}
	return c.Store.FinishComparison(id)
}

// member reads one repository and records what it established. An error is ABOM's own failure;
// anything GitHub declined to answer is recorded on the member as why it was not compared.
func (c *Comparer) member(
	ctx context.Context, id int64, member store.DatabaseIdentity, results []store.ComparedResult,
	asked map[string]bool,
) error {
	record, err := c.read(ctx, member, results, asked)
	if err != nil {
		return err
	}
	// A read cut short by the stop says nothing about the repository, so it stays unread.
	if stopped(ctx) {
		return nil
	}
	return c.Store.RecordComparisonMember(id, record)
}

func stopped(ctx context.Context) bool {
	return ctx.Err() != nil
}

func (c *Comparer) read(
	ctx context.Context, member store.DatabaseIdentity, results []store.ComparedResult, asked map[string]bool,
) (store.ComparisonRecord, error) {
	record := store.ComparisonRecord{
		DatabaseIdentity: member, Status: store.ComparisonNotCompared,
		Counted: true, ResultCount: len(results),
	}
	alerts, err := c.Read.CodeScanningAlerts(ctx, member.Repository)
	switch {
	case errors.Is(err, catalog.ErrNoAnalysis):
		record.Reason = store.ReasonNoCodeScanning
		record.Detail = fmt.Sprintf("code scanning holds no analysis here (GitHub: %s), so nothing would be raised",
			noAnalysisReason(err))
		return record, nil
	case err != nil:
		record.Reason = store.ReasonUnread
		record.Detail = "its alerts could not be read: " + githubReason(err)
		return record, nil
	}

	ref, err := c.defaultRef(member.Repository, alerts)
	if err != nil {
		return record, err
	}
	analysis, found, err := c.Read.ActionsAnalysis(ctx, member.Repository, ref)
	switch {
	case err != nil:
		record.Reason = store.ReasonUnread
		record.Detail = "its analyses could not be read, so whether Actions analysis runs here is " +
			"not known: " + githubReason(err)
		return record, nil
	case !found:
		analysis, found = actionsAlerted(alerts)
	}
	if !found {
		record.Reason = store.ReasonNoActionsAnalysis
		record.Detail = "code scanning holds no Actions analysis of its default branch among its " +
			"newest analyses here, so nothing this run found would be raised until one runs"
		return record, nil
	}

	record.Status = store.ComparisonCompared
	record.AlertedCommit, record.AlertedAt = analysis.CommitSHA, analysis.CreatedAt
	record.AlertedCLI = analysis.ToolVersion
	record.Results = Match(results, alerts)
	record.Alerts = countAlerts(alerts, rulesOf(results, asked))
	c.staleness(ctx, member, &record)
	return record, nil
}

// defaultRef is the branch the alerts were read at: the ref their own instances name, since the
// alerts endpoint answers for the default branch, or the one enumeration recorded where there is
// no alert to name it. Empty where neither says, which reads every ref but a pull request's.
func (c *Comparer) defaultRef(repository string, alerts []catalog.Alert) (string, error) {
	for _, a := range alerts {
		if a.Ref != "" {
			return a.Ref, nil
		}
	}
	branch, known, err := c.Store.DefaultBranch(repository)
	if err != nil || !known {
		return "", err
	}
	return branch, nil
}

// actionsAlerted takes an Actions alert as evidence that Actions analysis ran, for a repository
// whose newest page of analyses held none, at the commit its most recent instance names.
func actionsAlerted(alerts []catalog.Alert) (catalog.Analysis, bool) {
	for _, a := range alerts {
		if strings.Contains(a.Category, "language:actions") {
			return catalog.Analysis{
				CommitSHA: a.CommitSHA, Ref: a.Ref, Category: a.Category, ToolVersion: a.ToolVersion,
			}, true
		}
	}
	return catalog.Analysis{}, false
}

// staleness says how the commit analyzed relates to the default branch as it is now. A branch that
// could not be read is unknown, never current.
func (c *Comparer) staleness(ctx context.Context, member store.DatabaseIdentity, record *store.ComparisonRecord) {
	record.Staleness = store.StalenessUnknown
	if member.Commit == "" {
		return
	}
	head, err := c.Read.HeadSHA(ctx, member.Repository)
	if err != nil || head == "" {
		return
	}
	record.HeadCommit = head
	if head == member.Commit {
		record.Staleness = store.StalenessCurrent
		return
	}
	moved, err := c.Read.CompareCommits(ctx, member.Repository, member.Commit, head)
	if err != nil {
		record.Staleness = store.StalenessMoved
		return
	}
	switch moved.Status {
	case "identical":
		record.Staleness = store.StalenessCurrent
	case "ahead":
		record.Staleness = store.StalenessBehind
		record.BehindBy = &moved.AheadBy
	case "behind":
		record.Staleness = store.StalenessAhead
	case "diverged":
		record.Staleness = store.StalenessDiverged
		record.BehindBy = &moved.AheadBy
	default:
		record.Staleness = store.StalenessMoved
	}
}

// place is where a rule reported something; column zero is the whole line.
type place struct {
	rule, path string
	line, col  int
}

// Match sets each result against the alert that already stands for it, one alert to a result.
//
// An alert stands for a result reported by the same rule at the same place: the path, line and
// column its most recent instance names. Where no alert is at that exact place, one on the same
// line under the same rule stands for it, since a CodeQL release can move a column without
// moving the alert. An open alert is preferred to a dismissed one, and a dismissed one to a fixed
// one, because that is the order a developer would meet them in.
func Match(results []store.ComparedResult, alerts []catalog.Alert) []store.ComparedResult {
	order := make([]int, len(alerts))
	for i := range alerts {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		x, y := alerts[order[a]], alerts[order[b]]
		if statePreference(x.State) != statePreference(y.State) {
			return statePreference(x.State) < statePreference(y.State)
		}
		return x.Number < y.Number
	})
	exact, line := map[place][]int{}, map[place][]int{}
	for _, i := range order {
		a := alerts[i]
		at := place{a.RuleID, a.Path, a.StartLine, a.StartColumn}
		exact[at] = append(exact[at], i)
		at.col = 0
		line[at] = append(line[at], i)
	}
	taken := make([]bool, len(alerts))

	out := make([]store.ComparedResult, len(results))
	copy(out, results)
	matched := make([]bool, len(out))
	for _, byLine := range []bool{false, true} {
		for i := range out {
			if matched[i] {
				continue
			}
			at := place{out[i].RuleID, out[i].FilePath, out[i].StartLine, out[i].StartColumn}
			candidates := exact[at]
			if byLine {
				at.col = 0
				candidates = line[at]
			}
			for _, j := range candidates {
				if !taken[j] {
					taken[j], matched[i] = true, true
					out[i].AlertNumber, out[i].AlertState = alerts[j].Number, alerts[j].State
					break
				}
			}
		}
	}
	for i := range out {
		out[i].Match = store.MatchOf(out[i].AlertState)
	}
	return out
}

func statePreference(state string) int {
	switch state {
	case "open":
		return 0
	case "dismissed":
		return 1
	}
	return 2
}

// rulesOf is every rule the run asked for or reported, which are the rules whose alerts count.
func rulesOf(results []store.ComparedResult, asked map[string]bool) map[string]bool {
	out := make(map[string]bool, len(asked))
	for rule := range asked {
		out[rule] = true
	}
	for _, r := range results {
		out[r.RuleID] = true
	}
	return out
}

func countAlerts(alerts []catalog.Alert, rules map[string]bool) []store.RuleAlerts {
	type key struct{ rule, state string }
	counts := map[key]int{}
	for _, a := range alerts {
		if rules[a.RuleID] {
			counts[key{a.RuleID, a.State}]++
		}
	}
	out := make([]store.RuleAlerts, 0, len(counts))
	for k, n := range counts {
		out = append(out, store.RuleAlerts{RuleID: k.rule, State: k.state, Alerts: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RuleID != out[j].RuleID {
			return out[i].RuleID < out[j].RuleID
		}
		return out[i].State < out[j].State
	})
	return out
}

// noAnalysisReason is GitHub's reason for holding no analysis, without the repository around it.
func noAnalysisReason(err error) string {
	var none *catalog.NoAnalysisError
	if errors.As(err, &none) {
		return none.Reason
	}
	return err.Error()
}

// githubReason is GitHub's own words where it gave some, and the error otherwise.
func githubReason(err error) string {
	var status *catalog.StatusError
	if errors.As(err, &status) && status.Message != "" {
		return fmt.Sprintf("%d, %s", status.Code, status.Message)
	}
	return err.Error()
}
