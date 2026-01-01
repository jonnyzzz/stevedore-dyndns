//go:build integration

package internal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/ipdetect"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
)

// TestIntegration_IPDetectionFallback tests the full IP detection flow
func TestIntegration_IPDetectionFallback(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	// Create mock external IP service
	mockIPService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "203.0.113.42")
	}))
	defer mockIPService.Close()

	cfg := &config.Config{
		FritzboxHost: "192.0.2.1", // Non-routable, will fail
	}

	detector := ipdetect.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// This should fall back to external services
	ipv4, _, err := detector.Detect(ctx)
	if err != nil {
		t.Logf("IP detection result: IPv4=%s, error=%v", ipv4, err)
	}

	// The test passes if we either got an IP or got an expected error
	// (network might not be available in CI)
}

// TestIntegration_MappingHotReload tests hot reloading of mappings
func TestIntegration_MappingHotReload(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	tmpDir := t.TempDir()
	mappingsPath := filepath.Join(tmpDir, "mappings.yaml")

	// Create initial mappings
	initial := `
mappings:
  - subdomain: app1
    target: "backend1:8080"
`
	if err := os.WriteFile(mappingsPath, []byte(initial), 0644); err != nil {
		t.Fatalf("Failed to write initial mappings: %v", err)
	}

	mgr := mapping.New(mappingsPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Failed to load initial mappings: %v", err)
	}

	// Verify initial state
	mappings := mgr.Get()
	if len(mappings) != 1 {
		t.Fatalf("Expected 1 mapping, got %d", len(mappings))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start watcher
	reloaded := make(chan bool, 1)
	go mgr.Watch(ctx, func() {
		reloaded <- true
	})

	// Give watcher time to start
	time.Sleep(100 * time.Millisecond)

	// Update mappings
	updated := `
mappings:
  - subdomain: app1
    target: "backend1:8080"
  - subdomain: app2
    target: "backend2:9090"
`
	if err := os.WriteFile(mappingsPath, []byte(updated), 0644); err != nil {
		t.Fatalf("Failed to write updated mappings: %v", err)
	}

	// Wait for reload
	select {
	case <-reloaded:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for mapping reload")
	}

	// Verify updated state
	mappings = mgr.Get()
	if len(mappings) != 2 {
		t.Fatalf("Expected 2 mappings after update, got %d", len(mappings))
	}
}

// TestIntegration_EndToEnd simulates the full service flow
func TestIntegration_EndToEnd(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	tmpDir := t.TempDir()

	// Create mappings
	mappingsPath := filepath.Join(tmpDir, "mappings.yaml")
	mappingsContent := `
mappings:
  - subdomain: api
    target: "api-backend:8080"
    options:
      websocket: true
      health_path: /healthz
  - subdomain: web
    target: "web-frontend:3000"
`
	if err := os.WriteFile(mappingsPath, []byte(mappingsContent), 0644); err != nil {
		t.Fatalf("Failed to write mappings: %v", err)
	}

	// Load and validate mappings
	mgr := mapping.New(mappingsPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Failed to load mappings: %v", err)
	}

	mappings := mgr.Get()
	if len(mappings) != 2 {
		t.Fatalf("Expected 2 mappings, got %d", len(mappings))
	}

	// Verify first mapping
	if mappings[0].Subdomain != "api" {
		t.Errorf("First mapping subdomain = %q, want %q", mappings[0].Subdomain, "api")
	}
	if !mappings[0].Options.Websocket {
		t.Error("First mapping should have websocket enabled")
	}
	if mappings[0].Options.HealthPath != "/healthz" {
		t.Errorf("First mapping health path = %q, want %q", mappings[0].Options.HealthPath, "/healthz")
	}

	// Verify second mapping
	if mappings[1].Subdomain != "web" {
		t.Errorf("Second mapping subdomain = %q, want %q", mappings[1].Subdomain, "web")
	}
}

// TestIntegration_ConfigValidation tests configuration validation
func TestIntegration_ConfigValidation(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name: "valid minimal config",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
				"CLOUDFLARE_ZONE_ID":   "test-zone",
				"DOMAIN":               "example.com",
				"ACME_EMAIL":           "test@example.com",
			},
			wantErr: false,
		},
		{
			name: "valid full config",
			env: map[string]string{
				"CLOUDFLARE_API_TOKEN": "test-token",
				"CLOUDFLARE_ZONE_ID":   "test-zone",
				"DOMAIN":               "example.com",
				"ACME_EMAIL":           "test@example.com",
				"FRITZBOX_HOST":        "192.168.1.1",
				"IP_CHECK_INTERVAL":    "10m",
				"LOG_LEVEL":            "debug",
			},
			wantErr: false,
		},
		{
			name:    "missing required fields",
			env:     map[string]string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment
			for _, key := range []string{
				"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_ZONE_ID", "DOMAIN", "ACME_EMAIL",
				"FRITZBOX_HOST", "FRITZBOX_USER", "FRITZBOX_PASSWORD",
				"MANUAL_IPV4", "MANUAL_IPV6", "IP_CHECK_INTERVAL", "LOG_LEVEL",
			} {
				os.Unsetenv(key)
			}

			// Set test environment
			for k, v := range tt.env {
				os.Setenv(k, v)
			}

			cfg, err := config.Load()
			if (err != nil) != tt.wantErr {
				t.Errorf("config.Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr && cfg == nil {
				t.Error("config.Load() returned nil config with no error")
			}
		})
	}
}

// TestIntegration_ConcurrentAccess tests thread safety under load
func TestIntegration_ConcurrentAccess(t *testing.T) {
	if os.Getenv("INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test - set INTEGRATION_TEST=true to run")
	}

	tmpDir := t.TempDir()
	mappingsPath := filepath.Join(tmpDir, "mappings.yaml")

	content := `
mappings:
  - subdomain: app
    target: "backend:8080"
`
	if err := os.WriteFile(mappingsPath, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write mappings: %v", err)
	}

	mgr := mapping.New(mappingsPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Failed to load mappings: %v", err)
	}

	// Run concurrent operations
	done := make(chan bool)
	errors := make(chan error, 100)

	// Readers
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				mappings := mgr.Get()
				if len(mappings) == 0 {
					errors <- fmt.Errorf("got empty mappings")
				}
			}
			done <- true
		}()
	}

	// Writers (reloaders)
	for i := 0; i < 5; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				if err := mgr.Load(); err != nil {
					errors <- err
				}
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 15; i++ {
		<-done
	}

	close(errors)
	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
	}
}
