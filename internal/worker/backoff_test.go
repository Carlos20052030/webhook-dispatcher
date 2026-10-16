package worker

import (
	"testing"
	"time"
)

func TestBackoff_WithinBounds(t *testing.T) {
	base := time.Second
	cap := time.Hour
	for attempt := 1; attempt <= 15; attempt++ {
		upper := time.Duration(float64(base) * pow(2, attempt-1))
		if upper > cap {
			upper = cap
		}
		for i := 0; i < 100; i++ {
			d := Backoff(base, cap, attempt)
			if d < 0 {
				t.Fatalf("attempt %d: negative duration", attempt)
			}
			if d > upper {
				t.Fatalf("attempt %d: %v > upper %v", attempt, d, upper)
			}
		}
	}
}

func TestBackoff_ZeroAndNegativeAttempt(t *testing.T) {
	base := time.Second
	cap := time.Minute
	for _, attempt := range []int{0, -5} {
		d := Backoff(base, cap, attempt)
		if d < 0 || d > base {
			t.Errorf("attempt %d: out of range: %v", attempt, d)
		}
	}
}

func pow(b, e int) float64 {
	r := 1.0
	for i := 0; i < e; i++ {
		r *= float64(b)
	}
	return r
}
