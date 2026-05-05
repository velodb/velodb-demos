package datagen

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// PartnerProfile represents a single partner's configuration
type PartnerProfile struct {
	ID   int    `yaml:"id"`
	Name string `yaml:"name"`
	Tier string `yaml:"tier"`

	Volume struct {
		OrdersPerDay int     `yaml:"orders_per_day"`
		GMVDaily     float64 `yaml:"gmv_daily"`
		SKUCount     int     `yaml:"sku_count"`
	} `yaml:"volume"`

	CustomerSegments map[string]float64 `yaml:"customer_segments"`

	CustomerLTV struct {
		VIP      PriceRange `yaml:"vip"`
		Standard PriceRange `yaml:"standard"`
		Trial    PriceRange `yaml:"trial"`
	} `yaml:"customer_ltv"`

	Products struct {
		Categories        []string           `yaml:"categories"`
		PriceDistribution map[string]float64 `yaml:"price_distribution"`
		Margin            PriceRange         `yaml:"margin"`
	} `yaml:"products"`

	Growth struct {
		Type       string  `yaml:"type"`
		Rate       float64 `yaml:"rate"`
		Volatility float64 `yaml:"volatility"`
	} `yaml:"growth"`

	VIPOrder struct {
		SpikeMultiplier int     `yaml:"spike_multiplier"`
		RevenueImpact   float64 `yaml:"revenue_impact"`
		MinAmount       float64 `yaml:"min_amount"`
		MaxAmount       float64 `yaml:"max_amount"`
	} `yaml:"vip_order"`
}

// PriceRange represents min/max values
type PriceRange struct {
	Min float64 `yaml:"min"`
	Max float64 `yaml:"max"`
}

// GlobalConfig represents global data generation settings
type GlobalConfig struct {
	TimePatterns map[string]float64 `yaml:"time_patterns"`
	DeviceSplit  map[string]float64 `yaml:"device_split"`
	Conversion   map[string]float64 `yaml:"conversion"`
	EventDistribution map[string]float64 `yaml:"event_distribution"`
	Session      struct {
		DurationMin         int `yaml:"duration_min"`
		DurationMax         int `yaml:"duration_max"`
		EventsPerSessionMin int `yaml:"events_per_session_min"`
		EventsPerSessionMax int `yaml:"events_per_session_max"`
	} `yaml:"session"`
}

// Config holds all partner profiles and global settings
type Config struct {
	Partners []PartnerProfile `yaml:"partners"`
	Global   GlobalConfig     `yaml:"global"`
}

// LoadConfig loads partner profiles from YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}

	return &config, nil
}

// GetPartner returns a partner profile by ID
func (c *Config) GetPartner(id int) (*PartnerProfile, error) {
	for i := range c.Partners {
		if c.Partners[i].ID == id {
			return &c.Partners[i], nil
		}
	}
	return nil, fmt.Errorf("partner %d not found in configuration", id)
}
