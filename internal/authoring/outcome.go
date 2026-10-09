package authoring

import (
	"fmt"
	"slices"
)

// Where a request stands as a whole, read from its steps.
const (
	OutcomeWorking    = "working"
	OutcomeFailed     = "failed"
	OutcomeAttention  = "attention"
	OutcomeUnfinished = "unfinished"
	OutcomeUnstarted  = "unstarted"
	OutcomeReady      = "ready"
)

// Outcome is a request's one answer to "where does this stand": a headline and the step behind it.
type Outcome struct {
	State    string `json:"state"`
	Headline string `json:"headline"`
	Detail   string `json:"detail"`
	// Step is the step the outcome was read from, empty when no one step decides it.
	Step string `json:"step"`
	// Passed of Total steps have passed.
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

// Overall reads a request's outcome from its steps: while an agent works, the first step not yet
// passed; otherwise a failure first, then what is unconfirmed, then the first step not taken.
func Overall(steps []Step, working bool) Outcome {
	o := overall(steps, working)
	o.Total = len(steps)
	for _, s := range steps {
		if s.State == Passing {
			o.Passed++
		}
	}
	return o
}

func overall(steps []Step, working bool) Outcome {
	if working {
		i := slices.IndexFunc(steps, func(s Step) bool { return s.State != Passing })
		if i < 0 {
			return Outcome{State: OutcomeWorking, Headline: "Agent working", Detail: "every step has passed"}
		}
		return Outcome{State: OutcomeWorking, Headline: "Agent working: " + stepLabels[steps[i].Step],
			Detail: steps[i].Detail, Step: steps[i].Step}
	}
	for _, rule := range []struct {
		state    State
		outcome  string
		headline string
	}{
		{Failed, OutcomeFailed, "%s failed"},
		{Warning, OutcomeAttention, "%s is unconfirmed"},
	} {
		if s, ok := first(steps, rule.state); ok {
			return Outcome{State: rule.outcome, Headline: fmt.Sprintf(rule.headline, stepLabels[s.Step]),
				Detail: s.Detail, Step: s.Step}
		}
	}
	next, unfinished := first(steps, Pending)
	switch {
	case unfinished && !slices.ContainsFunc(steps, func(s Step) bool { return s.State != Pending }):
		return Outcome{State: OutcomeUnstarted, Headline: "Not started", Detail: next.Detail, Step: next.Step}
	case unfinished:
		return Outcome{State: OutcomeUnfinished, Headline: "Unfinished: " + stepLabels[next.Step],
			Detail: next.Detail, Step: next.Step}
	}
	return Outcome{State: OutcomeReady, Headline: "Ready", Detail: "every step has passed"}
}

func first(steps []Step, state State) (Step, bool) {
	for _, s := range steps {
		if s.State == state {
			return s, true
		}
	}
	return Step{}, false
}
