package view

import (
	"fmt"
	"strings"
)

// AdvisoryUnrated is an advisory GitHub published no severity for. The others are a finding's
// bands; `moderate`, how some of GitHub's interfaces spell medium, reads as medium in the store.
const AdvisoryUnrated = "unrated"

// AdvisorySeverities is the vocabulary of `advisorySeverity`, most severe first.
func AdvisorySeverities() []string {
	return []string{BandCritical, BandHigh, BandMedium, BandLow, AdvisoryUnrated}
}

// How a repository reaches what a predicate is about: by naming it itself, or through something
// else it uses. Kept as two words so a rule cannot refuse a repository for a route it cannot change.
const (
	ReachedDirect     = "direct"
	ReachedTransitive = "transitive"
)

// findingOnlyTerms describe a finding and have no meaning for an advisory.
var findingOnlyTerms = []string{
	paramRules, paramNotRules, paramProvider, paramSource, paramSeverity, paramSecuritySeverity,
	paramMode,
}

// policyTerms are what a policy applies: the set path's terms, and the names of other policies it
// refuses whatever they refuse.
var policyTerms = func() map[string]bool {
	out := map[string]bool{paramPolicies: true}
	for t := range setTerms {
		out[t] = true
	}
	return out
}()

// UnenforceableAsPolicy names the terms a policy's predicate carries that a policy cannot apply.
func (s Spec) UnenforceableAsPolicy() []string {
	return s.carried(func(t string) bool { return policyTerms[t] })
}

// SetConflict refuses a predicate whose terms each apply and together ask no single question.
//
// A policy is about findings or about advisories. Read as one node carrying both it matches almost
// nothing, and read as either it enforces a question nobody wrote. "Either" is a superset naming
// two policies, and that is the only place an OR is spelled.
func (s Spec) SetConflict() error {
	carried := s.carried(func(string) bool { return false })
	if len(s.Policies) > 0 {
		others := []string{}
		for _, t := range carried {
			if t != paramPolicies {
				others = append(others, t)
			}
		}
		if len(others) > 0 {
			return fmt.Errorf("%s names policies and nothing else, so %s would narrow nothing; "+
				"narrow the policies it names instead", paramPolicies, strings.Join(others, ", "))
		}
		return nil
	}
	if len(s.AdvisorySeverities) == 0 {
		return nil
	}
	findings := []string{}
	for _, t := range carried {
		for _, f := range findingOnlyTerms {
			if t == f {
				findings = append(findings, t)
			}
		}
	}
	if len(findings) == 0 {
		return nil
	}
	return fmt.Errorf("%s ask about findings and %s about advisories, and one predicate asks "+
		"about one of them; save each as a policy and name both in a third with %s=",
		strings.Join(findings, ", "), paramAdvisorySeverity, paramPolicies)
}
