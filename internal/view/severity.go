package view

// The bands a rule's `@security-severity` score is read through, in the words code scanning uses.
// `unknown` is a rule that declares no score: a statement about the rule, never a score of nothing.
const (
	BandCritical = "critical"
	BandHigh     = "high"
	BandMedium   = "medium"
	BandLow      = "low"
	BandUnknown  = "unknown"
)

// BandFloor is the lowest score a band admits.
type BandFloor struct {
	Band  string
	Floor float64
}

// bandFloors are code scanning's published thresholds, most severe first. Low is everything above
// zero, because the scale starts at 0.1 and a stored zero has only ever meant "declared none".
var bandFloors = []BandFloor{{BandCritical, 9.0}, {BandHigh, 7.0}, {BandMedium, 4.0}}

// BandFloors lists the scored bands above low with their thresholds, so the store can write the
// same reading into SQL rather than a second copy of the numbers.
func BandFloors() []BandFloor { return append([]BandFloor{}, bandFloors...) }

// Bands is the vocabulary of `securitySeverity`, most severe first.
func Bands() []string {
	return []string{BandCritical, BandHigh, BandMedium, BandLow, BandUnknown}
}

// BandOf reads a score the way code scanning does.
func BandOf(score *float64) string {
	if score == nil {
		return BandUnknown
	}
	for _, f := range bandFloors {
		if *score >= f.Floor {
			return f.Band
		}
	}
	if *score > 0 {
		return BandLow
	}
	return BandUnknown
}

// BandRank orders bands, most severe highest, on the scale an advisory's severity is ranked on.
// Anything that is not a band ranks with `unknown`.
func BandRank(band string) int {
	switch band {
	case BandCritical:
		return 4
	case BandHigh:
		return 3
	case BandMedium:
		return 2
	case BandLow:
		return 1
	}
	return 0
}
