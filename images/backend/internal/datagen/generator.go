package datagen

import (
	"math/rand"
	"time"
)

// Generator is the base interface for all data generators
type Generator interface {
	Generate() (string, error)
}

// GeneratorOptions holds common options for all generators
type GeneratorOptions struct {
	Partner *PartnerProfile
	Global  *GlobalConfig
	Days    int
	Output  string // postgres, csv, json
	Rand    *rand.Rand
}

// NewRand creates a new random number generator with the given seed
func NewRand(seed int64) *rand.Rand {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return rand.New(rand.NewSource(seed))
}

// PickWeighted selects an item based on weighted probabilities
func PickWeighted(r *rand.Rand, weights map[string]float64) string {
	total := 0.0
	for _, w := range weights {
		total += w
	}

	pick := r.Float64() * total
	current := 0.0

	for key, weight := range weights {
		current += weight
		if pick <= current {
			return key
		}
	}

	// Fallback to first key
	for key := range weights {
		return key
	}
	return ""
}

// RandomTimeInRange generates a random timestamp within a date range
func RandomTimeInRange(r *rand.Rand, start, end time.Time) time.Time {
	delta := end.Unix() - start.Unix()
	sec := r.Int63n(delta)
	return time.Unix(start.Unix()+sec, 0)
}

// ApplyTimePattern adjusts timestamp based on time-of-day distribution
func ApplyTimePattern(r *rand.Rand, baseTime time.Time, patterns map[string]float64) time.Time {
	timeOfDay := PickWeighted(r, patterns)

	year, month, day := baseTime.Date()
	loc := baseTime.Location()

	switch timeOfDay {
	case "daytime": // 9am-5pm
		hour := 9 + r.Intn(8)
		minute := r.Intn(60)
		return time.Date(year, month, day, hour, minute, r.Intn(60), 0, loc)
	case "evening": // 5pm-11pm
		hour := 17 + r.Intn(6)
		minute := r.Intn(60)
		return time.Date(year, month, day, hour, minute, r.Intn(60), 0, loc)
	case "night": // 11pm-9am
		hour := r.Intn(10) // 0-9 (midnight to 9am)
		if hour >= 0 && hour < 9 {
			// Early morning
			minute := r.Intn(60)
			return time.Date(year, month, day, hour, minute, r.Intn(60), 0, loc)
		}
		// Late night (23:00)
		hour = 23
		minute := r.Intn(60)
		return time.Date(year, month, day, hour, minute, r.Intn(60), 0, loc)
	default:
		return baseTime
	}
}

// RandomInRange returns a random float between min and max
func RandomInRange(r *rand.Rand, min, max float64) float64 {
	return min + r.Float64()*(max-min)
}

// RandomIntInRange returns a random integer between min and max (inclusive)
func RandomIntInRange(r *rand.Rand, min, max int) int {
	if min == max {
		return min
	}
	return min + r.Intn(max-min+1)
}
