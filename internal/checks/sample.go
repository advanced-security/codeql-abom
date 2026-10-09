package checks

import (
	"cmp"
	"maps"
	"math"
	"math/rand/v2"
	"slices"
)

// candidate is one subject a sampled check can read: its key in the queue, the stratum its random
// baseline is drawn within, and when its claim last changed.
type candidate struct {
	key, stratum, changed string
}

// draw is what one run of a sampled check reads, as indexes into its candidates: the open
// disagreements again, what changed most recently, and a random baseline from each stratum.
type draw struct {
	reread, recent []int
	random         map[string][]int
	sizes          map[string]int
}

// drawSample draws each part independently, so a subject in the recent part can also be drawn at
// random and the baseline stays a random sample of its whole stratum.
func drawSample(cands []candidate, open map[string]bool, recent, sample int, rng *rand.Rand) draw {
	d := draw{random: map[string][]int{}, sizes: map[string]int{}}
	byStratum := map[string][]int{}
	order := make([]int, len(cands))
	for i, c := range cands {
		order[i] = i
		d.sizes[c.stratum]++
		byStratum[c.stratum] = append(byStratum[c.stratum], i)
		if open[c.key] {
			d.reread = append(d.reread, i)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(cands[b].changed, cands[a].changed), cmp.Compare(cands[a].key, cands[b].key))
	})
	d.recent = order[:min(max(recent, 0), len(order))]
	// Strata in a fixed order, so a seeded generator draws the same sample every time.
	for _, stratum := range slices.Sorted(maps.Keys(byStratum)) {
		idx := byStratum[stratum]
		rng.Shuffle(len(idx), func(i, j int) { idx[i], idx[j] = idx[j], idx[i] })
		d.random[stratum] = idx[:min(max(sample, 0), len(idx))]
	}
	return d
}

// upperBound is the largest share of a population that can disagree, at 95% confidence, given d
// disagreements among n subjects drawn from it at random: the one-sided Clopper-Pearson bound.
func upperBound(d, n int) float64 {
	switch {
	case n <= 0 || d >= n:
		return 1
	case d == 0:
		return 1 - math.Pow(0.05, 1/float64(n))
	}
	lo, hi := float64(d)/float64(n), 1.0
	for range 64 {
		mid := (lo + hi) / 2
		if binomialCDF(d, n, mid) > 0.05 {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// binomialCDF is the chance of at most d successes in n trials that each succeed with chance p.
func binomialCDF(d, n int, p float64) float64 {
	logChoose := func(n, k int) float64 {
		a, _ := math.Lgamma(float64(n + 1))
		b, _ := math.Lgamma(float64(k + 1))
		c, _ := math.Lgamma(float64(n - k + 1))
		return a - b - c
	}
	sum := 0.0
	for k := 0; k <= d; k++ {
		sum += math.Exp(logChoose(n, k) + float64(k)*math.Log(p) + float64(n-k)*math.Log1p(-p))
	}
	return sum
}
