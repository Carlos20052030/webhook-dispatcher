package worker

import (
	"math"
	"math/rand"
	"time"
)

// Backoff returns the delay before the next attempt using full jitter:
// sleep = rand(0, min(cap, base * 2^(attempt-1))). attempt starts at 1.
func Backoff(base, cap time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Guard against overflow on large attempt counts.
	exp := math.Pow(2, float64(attempt-1))
	max := float64(base) * exp
	if max > float64(cap) || math.IsInf(max, 0) {
		max = float64(cap)
	}
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max) + 1))
}
