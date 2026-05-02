package service

import (
	"context"
	"time"
)

// Generator is the interface for all data generators
type Generator interface {
	Start(ctx context.Context) error
	Stop() error
	SetRate(rate int) error
	GetStatus() *Status
	EnableSpike(multiplier int, duration time.Duration) error
	DisableSpike() error
}

// Status represents the current state of a generator
type Status struct {
	Running         bool      `json:"running"`
	CurrentRate     int       `json:"current_rate"`
	BaselineRate    int       `json:"baseline_rate"`
	SpikeActive     bool      `json:"spike_active"`
	SpikeMultiplier int       `json:"spike_multiplier,omitempty"`
	SpikeEndsAt     time.Time `json:"spike_ends_at,omitempty"`
	EventsGenerated int64     `json:"events_generated"`
	Errors          int64     `json:"errors"`
	StartedAt       time.Time `json:"started_at,omitempty"`
}
