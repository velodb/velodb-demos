package main

import (
	"context"
	"testing"
	"time"
)

func TestStartRateLimiterHitsTarget(t *testing.T) {
	for _, rps := range []int{1000, 10000, 100000} {
		ctx, cancel := context.WithCancel(context.Background())
		tokens := startRateLimiter(ctx, rps)

		const window = 2 * time.Second
		deadline := time.After(window)
		got := 0
	drain:
		for {
			select {
			case <-deadline:
				break drain
			case _, ok := <-tokens:
				if !ok {
					break drain
				}
				got++
			}
		}
		cancel()

		rate := float64(got) / window.Seconds()
		pct := 100 * rate / float64(rps)
		t.Logf("target=%d/s achieved=%.0f/s (%.0f%%)", rps, rate, pct)
		if pct < 90 {
			t.Errorf("target=%d/s: achieved only %.0f/s (%.0f%% of target), want >=90%%", rps, rate, pct)
		}
	}
}
