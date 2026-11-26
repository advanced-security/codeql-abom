package authz

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Genesis is the chain value before the first entry.
var Genesis = strings.Repeat("0", 64)

// Entry is one assertion in the log, with the chain value that covers it and everything before it.
type Entry struct {
	Seq           int64           `json:"seq"`
	PredicateType string          `json:"predicateType"`
	Digest        string          `json:"digest"`
	Chain         string          `json:"chain"`
	Envelope      json.RawMessage `json:"envelope"`
	RecordedAt    string          `json:"recordedAt"`
}

// Ledger is the append-only log. The store implements it; nothing here writes anywhere else.
type Ledger interface {
	// AppendAssertion records an assertion after the head, chaining it with NextChain.
	AppendAssertion(predicateType, digest string, envelope []byte, at time.Time) (Entry, error)
	// Assertions reads entries after a sequence number, oldest first; a limit of zero reads all.
	Assertions(after int64, limit int) ([]Entry, error)
	// Head is the newest entry, and false when the log is empty.
	Head() (Entry, bool, error)
}

// NextChain is the chain value after an assertion: both are 64 hex digits, so concatenating them
// is unambiguous.
func NextChain(prev, digest string) string { return sha256Hex([]byte(prev + digest)) }

// Report is what verifying a log found.
type Report struct {
	Verified    int      `json:"verified"`
	Checkpoints int      `json:"checkpoints"`
	Head        string   `json:"head"`
	Failure     *Failure `json:"failure,omitempty"`
}

// Failure is the first entry that did not verify. Nothing after it is counted.
type Failure struct {
	Seq int64  `json:"seq"`
	Why string `json:"why"`
}

// VerifyLog walks a log from its first entry: each must be where its sequence says, carry the
// assertion it names, verify with a key not retired before the log reached it, be recorded no
// earlier than the one before, and chain from it; a checkpoint must name the head before it. It
// stops at the first that does not.
func VerifyLog(entries []Entry, b Bundle) Report {
	r := Report{Head: Genesis}
	prev, prevSeq, prevAt := Genesis, int64(0), time.Time{}
	for i, e := range entries {
		want := int64(i + 1)
		at, why := verifyEntry(e, want, prev, prevSeq, prevAt, b)
		if why != "" {
			r.Failure = &Failure{Seq: want, Why: why}
			return r
		}
		if e.PredicateType == PredicateCheckpoint {
			r.Checkpoints++
		}
		prev, prevSeq, prevAt = e.Chain, e.Seq, at
		r.Verified++
		r.Head = e.Chain
	}
	return r
}

func verifyEntry(e Entry, want int64, prev string, prevSeq int64, prevAt time.Time, b Bundle) (time.Time, string) {
	if e.Seq != want {
		return time.Time{}, fmt.Sprintf("entry %d is where entry %d should be", e.Seq, want)
	}
	var env Envelope
	if err := json.Unmarshal(e.Envelope, &env); err != nil {
		return time.Time{}, "its envelope is not JSON: " + err.Error()
	}
	if digest, err := env.Digest(); err != nil || digest != e.Digest {
		return time.Time{}, "its envelope is not the assertion it names"
	}
	at, err := time.Parse(time.RFC3339Nano, e.RecordedAt)
	if err != nil {
		return time.Time{}, "it records no time it was made"
	}
	if at.Before(prevAt) {
		return time.Time{}, "it is recorded earlier than the entry before it, which ABOM never writes: something else wrote to the log"
	}
	st, err := b.OpenLogged(env, e.Seq)
	if err != nil {
		return time.Time{}, "its signature does not verify: " + err.Error()
	}
	if st.PredicateType != e.PredicateType {
		return time.Time{}, fmt.Sprintf("it is logged as %s and signed as %s", e.PredicateType, st.PredicateType)
	}
	if NextChain(prev, e.Digest) != e.Chain {
		return time.Time{}, "its chain does not follow from the entry before it"
	}
	if e.PredicateType == PredicateCheckpoint {
		var cp checkpointPredicate
		if err := json.Unmarshal(st.Predicate, &cp); err != nil || cp.Seq != prevSeq || cp.Chain != prev {
			return time.Time{}, "its checkpoint does not name the head before it"
		}
	}
	return at, ""
}
