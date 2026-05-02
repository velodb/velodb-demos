package service

import (
	"database/sql"
)

// Config holds all generator configurations
type Config struct {
	Clickstream *ClickstreamConfig
	Ecommerce   *EcommerceConfig
}

// ClickstreamConfig configures the clickstream generator
type ClickstreamConfig struct {
	Enabled         bool
	PartnerID       int
	BaselineRate    int // events per second
	SpikeMultiplier int
	KafkaBrokers    []string
	KafkaTopic      string
	DB              *sql.DB // Optional: for user ID validation
}

// Note: User ID ranges are defined in datagen.PartnerUserRanges
// Use datagen.GetUserIDForPartner(partnerID) to get a user ID
