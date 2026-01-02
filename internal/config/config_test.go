package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_RequiredFields(t *testing.T) {
	// Clear all environment variables first
	clearEnv()

	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		errContains string
	}{
		{
			name:        "missing all required fields",
			env:         map[string]string{},
			wantErr:     true,
			errContains: "CLOUDFLARE_API_TOKEN is required",
		},
		{
			name: "missing zone ID",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
			},
			wantErr:     true,
			errContains: "CLOUDFLARE_ZONE_ID is required",
		},
		{
			name: "missing domain",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
				"CLOUDFLARE_ZONE_ID":   "test-zone",
			},
			wantErr:     true,
			errContains: "DOMAIN is required",
		},
		{
			name: "missing ACME email",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
				"CLOUDFLARE_ZONE_ID":   "test-zone",
				"DOMAIN":               "example.com",
			},
			wantErr:     true,
			errContains: "ACME_EMAIL is required",
		},
		{
			name: "all required fields present",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
				"CLOUDFLARE_ZONE_ID":   "test-zone",
				"DOMAIN":               "example.com",
				"ACME_EMAIL":           "test@example.com",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv()
			for k, v := range tt.env {
				os.Setenv(k, v)
			}

			cfg, err := Load()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() expected error containing %q, got nil", tt.errContains)
					return
				}
				if tt.errContains != "" && err.Error() != tt.errContains {
					t.Errorf("Load() error = %v, want error containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("Load() unexpected error: %v", err)
					return
				}
				if cfg == nil {
					t.Error("Load() returned nil config")
				}
			}
		})
	}
}

func TestLoad_DefaultValues(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Check defaults
	if cfg.FritzboxHost != "192.168.178.1" {
		t.Errorf("FritzboxHost = %q, want %q", cfg.FritzboxHost, "192.168.178.1")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.IPCheckInterval != 5*time.Minute {
		t.Errorf("IPCheckInterval = %v, want %v", cfg.IPCheckInterval, 5*time.Minute)
	}
}

func TestLoad_CustomValues(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	os.Setenv("FRITZBOX_HOST", "192.168.1.1")
	os.Setenv("FRITZBOX_USER", "admin")
	os.Setenv("FRITZBOX_PASSWORD", "secret")
	os.Setenv("MANUAL_IPV4", "1.2.3.4")
	os.Setenv("MANUAL_IPV6", "2001:db8::1")
	os.Setenv("IP_CHECK_INTERVAL", "10m")
	os.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.FritzboxHost != "192.168.1.1" {
		t.Errorf("FritzboxHost = %q, want %q", cfg.FritzboxHost, "192.168.1.1")
	}
	if cfg.FritzboxUser != "admin" {
		t.Errorf("FritzboxUser = %q, want %q", cfg.FritzboxUser, "admin")
	}
	if cfg.FritzboxPassword != "secret" {
		t.Errorf("FritzboxPassword = %q, want %q", cfg.FritzboxPassword, "secret")
	}
	if cfg.ManualIPv4 != "1.2.3.4" {
		t.Errorf("ManualIPv4 = %q, want %q", cfg.ManualIPv4, "1.2.3.4")
	}
	if cfg.ManualIPv6 != "2001:db8::1" {
		t.Errorf("ManualIPv6 = %q, want %q", cfg.ManualIPv6, "2001:db8::1")
	}
	if cfg.IPCheckInterval != 10*time.Minute {
		t.Errorf("IPCheckInterval = %v, want %v", cfg.IPCheckInterval, 10*time.Minute)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_InvalidIPCheckInterval(t *testing.T) {
	clearEnv()
	setRequiredEnv()
	os.Setenv("IP_CHECK_INTERVAL", "invalid")

	_, err := Load()
	if err == nil {
		t.Error("Load() expected error for invalid IP_CHECK_INTERVAL, got nil")
	}
}

func TestConfig_UseManualIP(t *testing.T) {
	tests := []struct {
		name       string
		manualIPv4 string
		manualIPv6 string
		want       bool
	}{
		{"no manual IPs", "", "", false},
		{"only IPv4", "1.2.3.4", "", true},
		{"only IPv6", "", "2001:db8::1", true},
		{"both IPs", "1.2.3.4", "2001:db8::1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ManualIPv4: tt.manualIPv4,
				ManualIPv6: tt.manualIPv6,
			}
			if got := cfg.UseManualIP(); got != tt.want {
				t.Errorf("UseManualIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_UseDiscovery(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"no token", "", false},
		{"with token", "test-token", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				StevedoreToken: tt.token,
			}
			if got := cfg.UseDiscovery(); got != tt.want {
				t.Errorf("UseDiscovery() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoad_DiscoverySettings(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Check default socket path
	if cfg.StevedoreSocket != "/var/run/stevedore/query.sock" {
		t.Errorf("StevedoreSocket = %q, want %q", cfg.StevedoreSocket, "/var/run/stevedore/query.sock")
	}

	// No token by default
	if cfg.StevedoreToken != "" {
		t.Errorf("StevedoreToken = %q, want empty", cfg.StevedoreToken)
	}

	// Custom settings
	clearEnv()
	setRequiredEnv()
	os.Setenv("STEVEDORE_SOCKET", "/custom/socket.sock")
	os.Setenv("STEVEDORE_TOKEN", "my-token")

	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.StevedoreSocket != "/custom/socket.sock" {
		t.Errorf("StevedoreSocket = %q, want %q", cfg.StevedoreSocket, "/custom/socket.sock")
	}
	if cfg.StevedoreToken != "my-token" {
		t.Errorf("StevedoreToken = %q, want %q", cfg.StevedoreToken, "my-token")
	}
}

func TestLoad_DNSTTLSettings(t *testing.T) {
	t.Run("default TTL matches IP check interval", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("IP_CHECK_INTERVAL", "5m")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		// 5 minutes = 300 seconds
		if cfg.DNSTTL != 300 {
			t.Errorf("DNSTTL = %d, want %d", cfg.DNSTTL, 300)
		}
	})

	t.Run("minimum TTL is 60 seconds", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("IP_CHECK_INTERVAL", "30s")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		// Should be clamped to minimum 60
		if cfg.DNSTTL != 60 {
			t.Errorf("DNSTTL = %d, want %d (minimum)", cfg.DNSTTL, 60)
		}
	})

	t.Run("custom TTL from env", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("DNS_TTL", "120")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.DNSTTL != 120 {
			t.Errorf("DNSTTL = %d, want %d", cfg.DNSTTL, 120)
		}
	})

	t.Run("custom TTL clamped to minimum", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("DNS_TTL", "30")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.DNSTTL != 60 {
			t.Errorf("DNSTTL = %d, want %d (minimum)", cfg.DNSTTL, 60)
		}
	})

	t.Run("invalid TTL returns error", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("DNS_TTL", "not-a-number")

		_, err := Load()
		if err == nil {
			t.Error("Load() expected error for invalid DNS_TTL, got nil")
		}
	})
}

func TestLoad_CloudflareProxySettings(t *testing.T) {
	t.Run("default proxy is false", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.CloudflareProxy {
			t.Error("CloudflareProxy = true, want false (default)")
		}
	})

	t.Run("proxy enabled with true", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("CLOUDFLARE_PROXY", "true")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if !cfg.CloudflareProxy {
			t.Error("CloudflareProxy = false, want true")
		}
	})

	t.Run("proxy enabled with 1", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("CLOUDFLARE_PROXY", "1")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if !cfg.CloudflareProxy {
			t.Error("CloudflareProxy = false, want true (for '1')")
		}
	})

	t.Run("proxy enabled with yes", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("CLOUDFLARE_PROXY", "yes")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if !cfg.CloudflareProxy {
			t.Error("CloudflareProxy = false, want true (for 'yes')")
		}
	})
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: &Config{
				CloudflareAPIToken: "token",
				CloudflareZoneID:   "zone",
				Domain:             "example.com",
				AcmeEmail:          "test@example.com",
			},
			wantErr: false,
		},
		{
			name: "missing token",
			cfg: &Config{
				CloudflareZoneID: "zone",
				Domain:           "example.com",
				AcmeEmail:        "test@example.com",
			},
			wantErr: true,
		},
		{
			name: "missing zone",
			cfg: &Config{
				CloudflareAPIToken: "token",
				Domain:             "example.com",
				AcmeEmail:          "test@example.com",
			},
			wantErr: true,
		},
		{
			name: "missing domain",
			cfg: &Config{
				CloudflareAPIToken: "token",
				CloudflareZoneID:   "zone",
				AcmeEmail:          "test@example.com",
			},
			wantErr: true,
		},
		{
			name: "missing email",
			cfg: &Config{
				CloudflareAPIToken: "token",
				CloudflareZoneID:   "zone",
				Domain:             "example.com",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestLoad_SharedDir(t *testing.T) {
	clearEnv()
	setRequiredEnv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	// Default shared dir
	if cfg.SharedDir != "/shared" {
		t.Errorf("SharedDir = %q, want %q", cfg.SharedDir, "/shared")
	}

	// Custom shared dir
	clearEnv()
	setRequiredEnv()
	os.Setenv("STEVEDORE_SHARED", "/opt/stevedore/shared")

	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.SharedDir != "/opt/stevedore/shared" {
		t.Errorf("SharedDir = %q, want %q", cfg.SharedDir, "/opt/stevedore/shared")
	}
}

func TestLoad_MappingsFilePath(t *testing.T) {
	// Create temp directories for testing
	tmpDir := t.TempDir()
	sharedDir := filepath.Join(tmpDir, "shared")
	dataDir := filepath.Join(tmpDir, "data")
	_ = os.MkdirAll(sharedDir, 0755)
	_ = os.MkdirAll(dataDir, 0755)

	t.Run("explicit MAPPINGS_FILE takes priority", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("MAPPINGS_FILE", "/custom/path/mappings.yaml")
		os.Setenv("STEVEDORE_SHARED", sharedDir)
		os.Setenv("DYNDNS_DATA", dataDir)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.MappingsFile != "/custom/path/mappings.yaml" {
			t.Errorf("MappingsFile = %q, want %q", cfg.MappingsFile, "/custom/path/mappings.yaml")
		}
	})

	t.Run("shared dir preferred when file exists", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("STEVEDORE_SHARED", sharedDir)
		os.Setenv("DYNDNS_DATA", dataDir)

		// Create shared mappings file
		sharedMappings := filepath.Join(sharedDir, "dyndns-mappings.yaml")
		_ = os.WriteFile(sharedMappings, []byte("mappings: []"), 0644)
		defer os.Remove(sharedMappings)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.MappingsFile != sharedMappings {
			t.Errorf("MappingsFile = %q, want %q", cfg.MappingsFile, sharedMappings)
		}
	})

	t.Run("data dir fallback when shared file does not exist", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("STEVEDORE_SHARED", sharedDir)
		os.Setenv("DYNDNS_DATA", dataDir)

		// Create data mappings file but not shared
		dataMappings := filepath.Join(dataDir, "mappings.yaml")
		_ = os.WriteFile(dataMappings, []byte("mappings: []"), 0644)
		defer os.Remove(dataMappings)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		if cfg.MappingsFile != dataMappings {
			t.Errorf("MappingsFile = %q, want %q", cfg.MappingsFile, dataMappings)
		}
	})

	t.Run("defaults to shared location when neither file exists", func(t *testing.T) {
		clearEnv()
		setRequiredEnv()
		os.Setenv("STEVEDORE_SHARED", sharedDir)
		os.Setenv("DYNDNS_DATA", dataDir)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() unexpected error: %v", err)
		}

		expected := filepath.Join(sharedDir, "dyndns-mappings.yaml")
		if cfg.MappingsFile != expected {
			t.Errorf("MappingsFile = %q, want %q", cfg.MappingsFile, expected)
		}
	})
}

func TestFileExists(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.txt")

	// File doesn't exist
	if fileExists(tmpFile) {
		t.Error("fileExists() = true for non-existent file")
	}

	// Create file
	_ = os.WriteFile(tmpFile, []byte("test"), 0644)

	if !fileExists(tmpFile) {
		t.Error("fileExists() = false for existing file")
	}
}

// Helper functions

func clearEnv() {
	envVars := []string{
		"CLOUDFLARE_API_TOKEN",
		"CLOUDFLARE_ZONE_ID",
		"CLOUDFLARE_PROXY",
		"DOMAIN",
		"ACME_EMAIL",
		"FRITZBOX_HOST",
		"FRITZBOX_USER",
		"FRITZBOX_PASSWORD",
		"MANUAL_IPV4",
		"MANUAL_IPV6",
		"IP_CHECK_INTERVAL",
		"DNS_TTL",
		"LOG_LEVEL",
		"DYNDNS_DATA",
		"DYNDNS_LOGS",
		"STEVEDORE_SHARED",
		"MAPPINGS_FILE",
		"STEVEDORE_SOCKET",
		"STEVEDORE_TOKEN",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}
}

func setRequiredEnv() {
	os.Setenv("CLOUDFLARE_API_TOKEN", "test-token")
	os.Setenv("CLOUDFLARE_ZONE_ID", "test-zone")
	os.Setenv("DOMAIN", "example.com")
	os.Setenv("ACME_EMAIL", "test@example.com")
}
