package authz

import (
	"encoding/json"
	"fmt"
	"sync"
)

// LogReader reads entries after a sequence number, oldest first; a limit of zero reads all.
type LogReader interface {
	Assertions(after int64, limit int) ([]Entry, error)
}

// pinPage is how many entries one read of the chain takes. A checkpoint lies within
// DefaultCheckpointEvery of any entry, so one read usually reaches it.
const pinPage = 2 * DefaultCheckpointEvery

// Pins remembers the entries a retired key signed that the chain has been shown to commit to. The
// zero value is ready to use, and it is safe for concurrent use.
type Pins struct {
	mu   sync.Mutex
	held map[string]bool
}

// vouch shows that the log's chain commits to e: from the entry before it, each entry chains from
// the last, up to a checkpoint signed by a key nothing has retired that names the head before it. A
// retired key's signature verifies wherever the key was trusted, so a copy of it, with the
// append-only triggers removed, could rewrite such a row; the chain is all that records the
// original. An entry is remembered by its position, digest and chain, so a row changed since reads
// as another entry and is walked again.
func (p *Pins) vouch(b Bundle, e Entry, log LogReader) error {
	key := fmt.Sprintf("%d %s %s", e.Seq, e.Digest, e.Chain)
	if p != nil {
		p.mu.Lock()
		held := p.held[key]
		p.mu.Unlock()
		if held {
			return nil
		}
	}
	if log == nil {
		return fmt.Errorf("authz: entry %d was signed by a retired key, and no log was given to read the chain from", e.Seq)
	}
	if err := walkToCheckpoint(b, e, log); err != nil {
		return err
	}
	if p != nil {
		p.mu.Lock()
		if p.held == nil {
			p.held = map[string]bool{}
		}
		p.held[key] = true
		p.mu.Unlock()
	}
	return nil
}

func walkToCheckpoint(b Bundle, e Entry, log LogReader) error {
	prev := Genesis
	if e.Seq > 1 {
		before, err := log.Assertions(e.Seq-2, 1)
		if err != nil {
			return err
		}
		if len(before) != 1 || before[0].Seq != e.Seq-1 {
			return fmt.Errorf("authz: entry %d has no entry before it to chain from", e.Seq)
		}
		prev = before[0].Chain
	}
	want, after := e.Seq, e.Seq-1
	for {
		page, err := log.Assertions(after, pinPage)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return fmt.Errorf("authz: entry %d was signed by a retired key, and no checkpoint by a key still "+
				"deciding covers it yet", e.Seq)
		}
		for _, x := range page {
			switch {
			case x.Seq != want:
				return fmt.Errorf("authz: entry %d is where entry %d should be", x.Seq, want)
			case x.Seq == e.Seq && (x.Digest != e.Digest || x.Chain != e.Chain):
				return fmt.Errorf("authz: entry %d changed while its chain was read", e.Seq)
			case NextChain(prev, x.Digest) != x.Chain:
				return fmt.Errorf("authz: the chain breaks at entry %d, between entry %d, which a retired key "+
					"signed, and the checkpoint that would vouch for it", x.Seq, e.Seq)
			}
			if x.PredicateType == PredicateCheckpoint {
				final, err := b.checkpointCovers(x, prev)
				if err != nil {
					return err
				}
				if final {
					return nil
				}
			}
			prev, want = x.Chain, want+1
		}
		after = page[len(page)-1].Seq
	}
}

// checkpointCovers verifies a checkpoint naming the head before it, prev, and reports whether a key
// nothing has retired signed it, which ends the walk. One a retired key signed is itself vouched for
// only by a later one.
func (b Bundle) checkpointCovers(x Entry, prev string) (bool, error) {
	var env Envelope
	if err := json.Unmarshal(x.Envelope, &env); err != nil {
		return false, fmt.Errorf("authz: the checkpoint at entry %d holds no envelope: %w", x.Seq, err)
	}
	if digest, err := env.Digest(); err != nil || digest != x.Digest {
		return false, fmt.Errorf("authz: the checkpoint at entry %d is not the assertion it names", x.Seq)
	}
	st, by, err := b.openLogged(env, x.Seq)
	if err != nil {
		return false, fmt.Errorf("authz: the checkpoint at entry %d does not verify: %w", x.Seq, err)
	}
	var cp checkpointPredicate
	if st.PredicateType != PredicateCheckpoint || json.Unmarshal(st.Predicate, &cp) != nil ||
		cp.Seq != x.Seq-1 || cp.Chain != prev {
		return false, fmt.Errorf("authz: the checkpoint at entry %d does not name the head before it", x.Seq)
	}
	return !b.retired(by), nil
}

// retired reports whether the bundle has retired the key id names.
func (b Bundle) retired(id string) bool {
	for _, k := range b.Keys {
		if k.KeyID == id {
			return k.NotAfter != ""
		}
	}
	return true
}
