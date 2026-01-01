package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all configuration for the dyndns service
type Config struct {
	// Cloudflare settings
	CloudflareAPIToken string
	CloudflareZoneID   string

	// Domain settings
	Domain    string
	AcmeEmail string

	// Fritzbox settings for TR-064/UPnP
	FritzboxHost     string
	FritzboxUser     string
	FritzboxPassword string

	// Manual IP override
	ManualIPv4 string
	ManualIPv6 string

	// Timing
	IPCheckInterval time.Duration

	// Logging
	LogLevel string

	// Paths
	DataDir      string
	LogsDir      string
	SharedDir    string
	MappingsFile string
	CaddyFile    string

	// Stevedore discovery settings
	StevedoreSocket string
	StevedoreToken  string
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	cfg := &Config{
		CloudflareAPIToken: os.Getenv("CLOUDFLARE_API_TOKEN"),
		CloudflareZoneID:   os.Getenv("CLOUDFLARE_ZONE_ID"),
		Domain:             os.Getenv("DOMAIN"),
		AcmeEmail:          os.Getenv("ACME_EMAIL"),
		FritzboxHost:       getEnvDefault("FRITZBOX_HOST", "192.168.178.1"),
		FritzboxUser:       os.Getenv("FRITZBOX_USER"),
		FritzboxPassword:   os.Getenv("FRITZBOX_PASSWORD"),
		ManualIPv4:         os.Getenv("MANUAL_IPV4"),
		ManualIPv6:         os.Getenv("MANUAL_IPV6"),
		LogLevel:           getEnvDefault("LOG_LEVEL", "info"),
		DataDir:            getEnvDefault("DYNDNS_DATA", "/data"),
		LogsDir:            getEnvDefault("DYNDNS_LOGS", "/var/log/dyndns"),
		SharedDir:          getEnvDefault("STEVEDORE_SHARED", "/shared"),
		StevedoreSocket:    getEnvDefault("STEVEDORE_SOCKET", "/var/run/stevedore/query.sock"),
		StevedoreToken:     os.Getenv("STEVEDORE_TOKEN"),
	}

	// Parse IP check interval
	intervalStr := getEnvDefault("IP_CHECK_INTERVAL", "5m")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return nil, fmt.Errorf("invalid IP_CHECK_INTERVAL: %w", err)
	}
	cfg.IPCheckInterval = interval

	// Set derived paths - prefer shared directory for cross-deployment communication
	// Check shared dir first (Stevedore standard), fallback to data dir
	sharedMappings := cfg.SharedDir + "/dyndns-mappings.yaml"
	dataMappings := cfg.DataDir + "/mappings.yaml"

	// Use explicit MAPPINGS_FILE if set, otherwise prefer shared location
	if mappingsFile := os.Getenv("MAPPINGS_FILE"); mappingsFile != "" {
		cfg.MappingsFile = mappingsFile
	} else if fileExists(sharedMappings) {
		cfg.MappingsFile = sharedMappings
	} else if fileExists(dataMappings) {
		cfg.MappingsFile = dataMappings
	} else {
		// Default to shared location for new installations
		cfg.MappingsFile = sharedMappings
	}

	cfg.CaddyFile = "/etc/caddy/Caddyfile"

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks that all required configuration is present
func (c *Config) Validate() error {
	if c.CloudflareAPIToken == "" {
		return fmt.Errorf("CLOUDFLARE_API_TOKEN is required")
	}
	if c.CloudflareZoneID == "" {
		return fmt.Errorf("CLOUDFLARE_ZONE_ID is required")
	}
	if c.Domain == "" {
		return fmt.Errorf("DOMAIN is required")
	}
	if c.AcmeEmail == "" {
		return fmt.Errorf("ACME_EMAIL is required")
	}
	return nil
}

// UseManualIP returns true if manual IP configuration is set
func (c *Config) UseManualIP() bool {
	return c.ManualIPv4 != "" || c.ManualIPv6 != ""
}

// UseDiscovery returns true if stevedore discovery is configured
func (c *Config) UseDiscovery() bool {
	return c.StevedoreToken != ""
}

func getEnvDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
