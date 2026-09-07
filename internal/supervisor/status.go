package supervisor

import (
	"time"
)

// Loop states, as the interface shows them.
//
// Stopped and paused are told apart deliberately: "paused" on a loop that has never run reads as
// one that was interrupted, and sends the operator looking for what went wrong.
const (
	StateStopped = "stopped"
	StatePaused  = "paused"
	StatePausing = "pausing"
	StateWorking = "working"
	StateIdle    = "idle"
)

// Problem is something the operator has to fix, paired with the exact way to fix it. It matches
// the shape ghauth uses, so the interface renders an unusable tool the same way either way.
type Problem struct {
	Reason string `json:"reason"`
	Remedy string `json:"remedy"`
}

// Status is what the loop is doing, for an operator deciding whether to leave it alone.
type Status struct {
	State string `json:"state"`
	// Cycle counts rounds since this process started, so a stuck loop is visible as a number
	// that stops moving.
	Cycle       int    `json:"cycle"`
	CycleStart  string `json:"cycleStart"`
	LastCycleAt string `json:"lastCycleAt"`
	NextWakeAt  string `json:"nextWakeAt"`

	Reclaimed int `json:"reclaimed"`
	Analyzed  int `json:"analyzed"`
	// Skipped counts repositories the ledger already had on record. Analyzed cannot stand in for
	// it — Processed excludes skips — so a re-analysis of a current corpus would otherwise move
	// no number at all and read as a loop doing nothing.
	Skipped     int `json:"skipped"`
	Settled     int `json:"settled"`
	Failed      int `json:"failed"`
	Unavailable int `json:"unavailable"`
	Discovered  int `json:"discovered"`
	Edges       int `json:"edges"`
	Rechecked   int `json:"rechecked"`
	Drifted     int `json:"drifted"`

	// ReachPending counts dependencies ingested since reach was last measured. Non-zero means
	// what is on screen is behind the graph, which is worth saying rather than hiding.
	ReachPending int `json:"reachPending"`
	// ReachThreshold is how many arrivals trigger a recount. Reported so the pending figure has
	// a denominator: "1,383 arrived since" says nothing about whether that is a lot.
	ReachThreshold    int    `json:"reachThreshold"`
	LastRebuildReason string `json:"lastRebuildReason"`
	LastRebuildMillis int64  `json:"lastRebuildMillis"`

	// LastError is the most recent failure. The loop keeps going, so this is the only place it
	// is visible.
	LastError   string   `json:"lastError"`
	LastErrorAt string   `json:"lastErrorAt"`
	Problem     *Problem `json:"problem"`

	// Concurrency and WorkDir say what starting the loop will spend, so pressing Start is not
	// an unpriced decision: it clones repositories and runs CodeQL over each one.
	Concurrency int    `json:"concurrency"`
	WorkDir     string `json:"workDir"`
	// ClaimsPerCycle bounds how much work one cycle takes on.
	ClaimsPerCycle int `json:"claimsPerCycle"`
}

// Status returns a copy of the current state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.status
	if !s.started {
		out.State = StateStopped
	} else if !s.running && out.State == StateWorking {
		// A pause is honored between cycles, so a repository already being cloned and analyzed
		// runs to completion. Reporting that as still working reads as a button that did
		// nothing, for as long as the cycle takes.
		out.State = StatePausing
	}
	out.Concurrency = s.Options.Concurrency
	out.WorkDir = s.Options.WorkDir
	out.ClaimsPerCycle = s.Options.Limit
	out.ReachThreshold = s.Aggregates.afterEdges()
	return out
}

func (s *Supervisor) setState(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.State = state
}

func (s *Supervisor) beginCycle() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Cycle++
	s.status.CycleStart = s.timestamp()
	s.status.State = StateWorking
}

func (s *Supervisor) record(apply func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apply(&s.status)
	s.status.LastCycleAt = s.timestamp()
}

// fail records a failure and asks for a full interval before trying again, so a persistent
// error backs off instead of spinning.
func (s *Supervisor) fail(doing string, err error) time.Duration {
	s.mu.Lock()
	s.status.LastError = doing + ": " + err.Error()
	s.status.LastErrorAt = s.timestamp()
	s.mu.Unlock()
	s.logf("FAILED %s: %v", doing, err)
	return s.interval()
}

// timestamp must be called with the lock held; Now is only read, never written.
func (s *Supervisor) timestamp() string {
	if s.Now != nil {
		return s.Now().UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}
