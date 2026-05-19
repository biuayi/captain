// Package lottery holds the pure prize-selection algorithm (DESIGN §SS-5).
// The DB transaction (pool resolution, atomic stock decrement, one-draw
// idempotency) lives in repo; this package is the testable picking core.
package lottery

import "math/rand"

// Candidate is a drawable prize with remaining stock.
type Candidate struct {
	PrizeID   string
	Code      string
	Level     string // grand | normal | none
	Weight    int
	Remaining int
}

// WeightedPick chooses a candidate by weight among those with remaining
// stock, using rng. Returns the index, or -1 when nothing is drawable.
func WeightedPick(cands []Candidate, rng *rand.Rand) int {
	total := 0
	for _, c := range cands {
		if c.Remaining > 0 && c.Weight > 0 {
			total += c.Weight
		}
	}
	if total == 0 {
		return -1
	}
	n := rng.Intn(total)
	for i, c := range cands {
		if c.Remaining <= 0 || c.Weight <= 0 {
			continue
		}
		if n < c.Weight {
			return i
		}
		n -= c.Weight
	}
	return -1
}
