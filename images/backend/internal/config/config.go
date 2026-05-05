package config

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	VeloDB    VeloDBConfig
	Postgres  PostgresConfig
	Server    ServerConfig
	Generator GeneratorConfig
}

type VeloDBConfig struct {
	Host         string
	Port         string
	User         string
	Password     string
	Database     string
	CloudCluster string
}

type PostgresConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Database string
}

type ServerConfig struct {
	Port         string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type GeneratorConfig struct {
	Enabled             bool
	PartnerID           int
	OrdersBaselineRate  int      // orders per minute
	ClickstreamBaselineRate int  // events per second
	KafkaBrokers        []string
	KafkaTopic          string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Try to load .env file (ignore error if not found)
	_ = godotenv.Load()

	cfg := &Config{
		VeloDB: VeloDBConfig{
			Host:         getEnv("VELODB_HOST", ""),
			Port:         getEnv("VELODB_PORT", "9030"),
			User:         getEnv("VELODB_USER", ""),
			Password:     getEnv("VELODB_PASSWORD", ""),
			Database:     getEnv("VELODB_DB", ""),
			CloudCluster: getEnv("VELODB_CLOUD_CLUSTER", ""),
		},
		Postgres: PostgresConfig{
			Host:     getEnv("POSTGRES_HOST", ""),
			Port:     getEnv("POSTGRES_PORT", "5432"),
			User:     getEnv("POSTGRES_USER", ""),
			Password: getEnv("POSTGRES_PASSWORD", ""),
			Database: getEnv("POSTGRES_DB", ""),
		},
		Server: ServerConfig{
			Port:         getEnv("SERVER_PORT", "8080"),
			ReadTimeout:  parseDuration(getEnv("SERVER_READ_TIMEOUT", "30s"), 30*time.Second),
			WriteTimeout: parseDuration(getEnv("SERVER_WRITE_TIMEOUT", "30s"), 30*time.Second),
		},
		Generator: GeneratorConfig{
			Enabled:                 getEnv("GENERATOR_ENABLED", "true") == "true",
			PartnerID:               parseInt(getEnv("GENERATOR_PARTNER_ID", "1"), 1),
			OrdersBaselineRate:      parseInt(getEnv("GENERATOR_ORDERS_RATE", "10"), 10),
			ClickstreamBaselineRate: parseInt(getEnv("GENERATOR_CLICKSTREAM_RATE", "10"), 10),
			KafkaBrokers:            parseStringSlice(getEnv("KAFKA_BROKERS", "localhost:9092")),
			KafkaTopic:              getEnv("KAFKA_TOPIC", "clickstream"),
		},
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks if required configuration values are set
func (c *Config) Validate() error {
	if c.VeloDB.Host == "" {
		return fmt.Errorf("VELODB_HOST is required")
	}
	if c.VeloDB.User == "" {
		return fmt.Errorf("VELODB_USER is required")
	}
	if c.VeloDB.Password == "" {
		return fmt.Errorf("VELODB_PASSWORD is required")
	}
	if c.VeloDB.Database == "" {
		return fmt.Errorf("VELODB_DB is required")
	}
	if c.Postgres.Host == "" {
		return fmt.Errorf("POSTGRES_HOST is required")
	}
	if c.Postgres.User == "" {
		return fmt.Errorf("POSTGRES_USER is required")
	}
	if c.Postgres.Password == "" {
		return fmt.Errorf("POSTGRES_PASSWORD is required")
	}
	if c.Postgres.Database == "" {
		return fmt.Errorf("POSTGRES_DB is required")
	}
	return nil
}

// VeloDBDSN returns the MySQL DSN for VeloDB
func (c *VeloDBConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=10s",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// PostgresDSN returns the connection string for Postgres
func (c *PostgresConfig) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		c.Host, c.Port, c.User, c.Password, c.Database)
}

// getEnv gets an environment variable with a default value
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// parseDuration parses a duration string with a default fallback
func parseDuration(value string, defaultValue time.Duration) time.Duration {
	d, err := time.ParseDuration(value)
	if err != nil {
		return defaultValue
	}
	return d
}

// parseInt parses an integer string with a default fallback
func parseInt(value string, defaultValue int) int {
	var i int
	_, err := fmt.Sscanf(value, "%d", &i)
	if err != nil {
		return defaultValue
	}
	return i
}

// parseStringSlice parses a comma-separated string into a slice
func parseStringSlice(value string) []string {
	if value == "" {
		return []string{}
	}
	result := []string{}
	for _, s := range splitByComma(value) {
		trimmed := trimSpace(s)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitByComma(s string) []string {
	result := []string{}
	current := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			result = append(result, current)
			current = ""
		} else {
			current += string(s[i])
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
