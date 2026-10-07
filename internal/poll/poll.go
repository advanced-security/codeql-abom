// Package poll asks whether external references moved, most reach first, since nobody else's repository can say so.
package poll

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/catalog"
	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/store"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// Defaults: a hundred references a query, none asked twice within an hour, the order reread every five minutes.
const (
	DefaultBatch = 100
	DefaultFloor = time.Hour
	DefaultEvery = 5 * time.Minute
)

// Resolver says where a batch of references point now.
type Resolver interface {
	ResolveRefs(ctx context.Context, qs []catalog.RefQuestion) (catalog.RefBatch, error)
}

// Poller asks about external references at its pool's pace, independent of the crawl's progress.
type Poller struct {
	Store   *store.Store
	Queue   *frontier.Queue
	Resolve Resolver
	// Pool is what polling spends, in GraphQL points. Nil spends without limit or record.
	Pool  *work.TokenPool
	Batch int
	Floor time.Duration
	Every time.Duration
	// Active says whether polling may spend now; nil always may.
	Active func() bool
	Log    io.Writer
	Now    func() time.Time

	// unreadUntil holds back an unread reference within the floor, since checked_at records only answered probes.
	unreadUntil map[catalog.RefQuestion]time.Time
}

// Pass is what one pass asked and learned: Moved and Unchanged count queue rows, the rest count references.
type Pass struct {
	Asked    int
	Queries  int
	Charged  int
	Unpriced int
	Moved    int
	// Unchanged rows had only their probe recorded.
	Unchanged int
	// Unread references are unknown: nothing about them was written.
	Unread int
	// Left counts references due and not asked; More says the pass stopped early to reread the order.
	Left int
	More bool
}

func (p *Poller) now() time.Time {
	if p.Now != nil {
		return p.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Poller) batch() int {
	if p.Batch <= 0 {
		return DefaultBatch
	}
	return p.Batch
}

func (p *Poller) floor() time.Duration {
	if p.Floor <= 0 {
		return DefaultFloor
	}
	return p.Floor
}

func (p *Poller) every() time.Duration {
	if p.Every <= 0 {
		return DefaultEvery
	}
	return p.Every
}

func (p *Poller) active() bool { return p.Active == nil || p.Active() }

// inside keeps the references the crawl would act on: a move found outside the owned closure
// queues work that is never claimed.
func (p *Poller) inside(rows []store.PollCandidate) ([]store.PollCandidate, error) {
	in, err := p.Queue.Inside()
	if err != nil || in == nil {
		return rows, err
	}
	kept := rows[:0]
	for _, row := range rows {
		if in(row.Owner + "/" + row.Repo) {
			kept = append(kept, row)
		}
	}
	return kept, nil
}

// Pass asks about up to limit due references in poll order; zero asks about all, stopping to reread the order.
func (p *Poller) Pass(ctx context.Context, limit int) (Pass, error) {
	var pass Pass
	start := p.now()
	rows, err := p.Store.PollDue(start.Add(-p.floor()), -1)
	if err != nil {
		return pass, err
	}
	if rows, err = p.inside(rows); err != nil {
		return pass, err
	}
	qs, rowsOf := p.questions(rows, start)
	if limit > 0 && len(qs) > limit {
		pass.Left, qs = len(qs)-limit, qs[:limit]
	}
	for len(qs) > 0 {
		if !p.active() || (limit == 0 && p.now().Sub(start) >= p.every()) {
			pass.More = true
			break
		}
		batch := qs[:min(p.batch(), len(qs))]
		qs = qs[len(batch):]
		if err := p.Pool.Wait(ctx); err != nil {
			return pass, err
		}
		answered, err := p.Resolve.ResolveRefs(ctx, batch)
		p.Pool.Spend(answered.Requests, answered.Cost)
		p.Pool.Observe(answered.Reading)
		pass.Asked += len(batch)
		pass.Queries += answered.Requests
		pass.Charged += answered.Cost
		if answered.Requests > 0 && !answered.Priced {
			pass.Unpriced++
		}
		if err != nil {
			if ctx.Err() != nil {
				return pass, ctx.Err()
			}
			p.logf("asking where %d external references point: %v; all of them stay unknown", len(batch), err)
			answered.Answers = nil
		}
		for i, q := range batch {
			if i >= len(answered.Answers) || answered.Answers[i].Commit == "" {
				pass.Unread++
				p.unreadUntil[q] = start.Add(p.floor())
				continue
			}
			for _, row := range rowsOf[q] {
				if err := p.settle(row, answered.Answers[i].Commit, &pass); err != nil {
					return pass, err
				}
			}
		}
	}
	pass.Left += len(qs)
	return pass, nil
}

// questions groups due rows by what is asked, in each one's first row's order, minus what was unread within the floor.
func (p *Poller) questions(rows []store.PollCandidate, now time.Time) (
	[]catalog.RefQuestion, map[catalog.RefQuestion][]store.PollCandidate) {
	if p.unreadUntil == nil {
		p.unreadUntil = map[catalog.RefQuestion]time.Time{}
	}
	for q, until := range p.unreadUntil {
		if !until.After(now) {
			delete(p.unreadUntil, q)
		}
	}
	var qs []catalog.RefQuestion
	rowsOf := map[catalog.RefQuestion][]store.PollCandidate{}
	for _, row := range rows {
		q := catalog.RefQuestion{Owner: row.Owner, Repo: row.Repo, Ref: row.Ref}
		if _, skip := p.unreadUntil[q]; skip {
			continue
		}
		if _, seen := rowsOf[q]; !seen {
			qs = append(qs, q)
		}
		rowsOf[q] = append(rowsOf[q], row)
	}
	return qs, rowsOf
}

// settle hands the answer to the recheck's drift handling, unless the crawl claimed or re-analyzed the row meanwhile.
func (p *Poller) settle(row store.PollCandidate, commit string, pass *Pass) error {
	now, found, err := p.Store.QueueRow(row.NodeKey, row.Ref)
	if err != nil {
		return err
	}
	if !found || now.Status != frontier.StatusDone || now.ResolvedSHA != row.ResolvedSHA {
		return nil
	}
	key := frontier.Key{NodeKey: row.NodeKey, Ref: row.Ref}
	if strings.EqualFold(commit, row.ResolvedSHA) {
		pass.Unchanged++
		return p.Queue.Touch(key)
	}
	pass.Moved++
	return p.Queue.MarkStale(key, commit)
}

// Run polls until ctx ends: whatever is due, as fast as the pool allows, then every period.
func (p *Poller) Run(ctx context.Context) {
	for ctx.Err() == nil {
		var pass Pass
		if p.active() {
			var err error
			pass, err = p.Pass(ctx, 0)
			if err != nil && ctx.Err() == nil {
				p.logf("polling external references: %v", err)
			}
			if pass.Asked > 0 {
				p.logf("polled %d external references in %d queries for %d GraphQL points%s: "+
					"%d rows moved and requeued, %d unchanged, %d references unread",
					pass.Asked, pass.Queries, pass.Charged, unpriced(pass.Unpriced),
					pass.Moved, pass.Unchanged, pass.Unread)
			}
		}
		if pass.More && p.active() {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(p.every()):
		}
	}
}

func unpriced(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d queries GitHub named no price for)", n)
}

func (p *Poller) logf(format string, args ...any) {
	if p.Log == nil {
		return
	}
	fmt.Fprintf(p.Log, "%s serve: %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
}
