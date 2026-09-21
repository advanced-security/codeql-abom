package expand

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/advanced-security/codeql-abom/internal/frontier"
	"github.com/advanced-security/codeql-abom/internal/work"
)

// RefProbe resolves what a ref currently points at.
type RefProbe interface {
	Resolve(ctx context.Context, owner, repo, ref string) (string, error)
}

// Resolve reports the commit a ref points at without fetching any objects. An empty ref means
// the default branch.
//
// This is what makes drift detection affordable: a ref lookup costs a fraction of the clone and
// database build it decides whether to spend.
//
// The ref is not optional. Probing the default branch for a row that asked about `v4` compares
// two unrelated commits, so an unmoved tag reads as drifted, re-analyzes to the identical result
// and is due again next pass — a treadmill indistinguishable from real drift.
//
// An annotated tag reaches the same treadmill by its own route: it has an object of its own, and
// `ls-remote` reports that object, not the commit under it. `^{}` is asked for alongside the ref
// so the answer is comparable with the commit a checkout produces.
func (c *Checkout) Resolve(ctx context.Context, owner, repo, ref string) (string, error) {
	if ref == "" {
		ref = "HEAD"
	}
	out, err := c.run(ctx, "", "ls-remote", c.cloneURL(owner, repo), ref, ref+"^{}")
	if err != nil {
		return "", fmt.Errorf("%w: resolving %s/%s@%s: %s", ErrUnavailable, owner, repo, ref, out)
	}
	sha, ok := dereferenced(out)
	if !ok {
		return "", fmt.Errorf("%w: %s/%s has no %s", ErrUnavailable, owner, repo, ref)
	}
	return sha, nil
}

// dereferenced picks the commit out of `ls-remote` output, preferring the `^{}` line an
// annotated tag adds. Only a tag has one, so everything else still answers from its single line.
func dereferenced(out string) (string, bool) {
	var fallback string
	for line := range strings.SplitSeq(out, "\n") {
		sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || sha == "" {
			continue
		}
		if strings.HasSuffix(name, "^{}") {
			return sha, true
		}
		if fallback == "" {
			fallback = sha
		}
	}
	return fallback, fallback != ""
}

// RecheckStats summarizes a recheck pass.
type RecheckStats struct {
	Checked   int
	Drifted   int
	Unchanged int
	// Failed counts probes that could not be answered; the stored analysis stays valid.
	Failed      int
	Unavailable int
}

// Rechecker finds analyzes whose ref has moved and returns them to the queue.
type Rechecker struct {
	Queue   *frontier.Queue
	Probe   RefProbe
	Pace    *work.Limiter
	OnEvent func(nodeKey, outcome string)
}

// Run probes dependencies whose ref may have moved since they were analyzed.
func (r *Rechecker) Run(ctx context.Context, ttl time.Duration, limit int) (RecheckStats, error) {
	due, err := r.Queue.DueForRecheck(ttl, limit)
	if err != nil {
		return RecheckStats{}, err
	}

	var stats RecheckStats
	for _, item := range due {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if err := r.Pace.Wait(ctx); err != nil {
			return stats, err
		}

		stats.Checked++
		sha, err := r.Probe.Resolve(ctx, item.Owner, item.Repo, item.Requested)
		switch {
		case IsUnavailable(err):
			stats.Unavailable++
			r.emit(item.NodeKey, "unavailable")
			if err := r.Queue.Unavailable(item.Key(), reasonOf(err)); err != nil {
				return stats, err
			}
		case err != nil:
			// The stored analysis is still valid; only knowledge of its freshness is missing,
			// so the item stays done and is probed again next pass.
			stats.Failed++
			r.emit(item.NodeKey, "failed")
		case sha == item.SHA:
			stats.Unchanged++
			r.emit(item.NodeKey, "unchanged")
			if err := r.Queue.Touch(item.Key()); err != nil {
				return stats, err
			}
		default:
			stats.Drifted++
			r.emit(item.NodeKey, "drifted")
			if err := r.Queue.MarkStale(item.Key(), sha); err != nil {
				return stats, err
			}
		}
	}
	return stats, nil
}

func (r *Rechecker) emit(nodeKey, outcome string) {
	if r.OnEvent != nil {
		r.OnEvent(nodeKey, outcome)
	}
}
