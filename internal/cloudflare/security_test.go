package cloudflare

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/cloudflare/cloudflare-go"
)

// Integration tests for Cloudflare security configuration.
// These tests require valid Cloudflare credentials and are skipped if not provided.
//
// Run with: go test -v -tags=integration ./internal/cloudflare/
// Required environment variables:
//   - CLOUDFLARE_API_TOKEN
//   - CLOUDFLARE_ZONE_ID

// TestIntegration_SSLModeConfiguration verifies the Cloudflare SSL mode is properly set.
// This is an integration test that requires valid credentials.
func TestIntegration_SSLModeConfiguration(t *testing.T) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")

	if token == "" || zoneID == "" {
		t.Skip("Skipping integration test: CLOUDFLARE_API_TOKEN or CLOUDFLARE_ZONE_ID not set")
	}

	// Create a minimal client for testing
	client, err := newTestClient(token, zoneID)
	if err != nil {
		t.Fatalf("Failed to create Cloudflare client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mode, err := client.GetSSLMode(ctx)
	if err != nil {
		t.Fatalf("Failed to get SSL mode: %v", err)
	}

	t.Logf("Current SSL mode: %s", mode)

	// Verify SSL mode is appropriate for proxy mode
	validModes := map[string]bool{
		"full":   true, // Good: connects to origin on 443
		"strict": true, // Best: full + certificate validation
	}

	insecureModes := map[string]string{
		"off":      "No encryption between Cloudflare and origin",
		"flexible": "Cloudflare connects to origin on port 80 (HTTP)",
	}

	if reason, insecure := insecureModes[mode]; insecure {
		t.Errorf("SECURITY: SSL mode %q is insecure: %s", mode, reason)
		t.Error("For Cloudflare proxy mode, SSL mode should be 'full' or 'strict'")
	}

	if !validModes[mode] {
		t.Logf("WARNING: SSL mode %q is not 'full' or 'strict'", mode)
	}
}

// TestIntegration_AuthenticatedOriginPull verifies AOP is enabled.
func TestIntegration_AuthenticatedOriginPull(t *testing.T) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")

	if token == "" || zoneID == "" {
		t.Skip("Skipping integration test: CLOUDFLARE_API_TOKEN or CLOUDFLARE_ZONE_ID not set")
	}

	client, err := newTestClient(token, zoneID)
	if err != nil {
		t.Fatalf("Failed to create Cloudflare client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enabled, err := client.IsAuthenticatedOriginPullEnabled(ctx)
	if err != nil {
		t.Fatalf("Failed to check AOP status: %v", err)
	}

	t.Logf("Authenticated Origin Pull enabled: %v", enabled)

	if !enabled {
		t.Error("SECURITY: Authenticated Origin Pull (mTLS) is NOT enabled")
		t.Error("This means anyone who discovers your origin IP can bypass Cloudflare")
		t.Error("Enable AOP in Cloudflare dashboard: SSL/TLS > Origin Server > Authenticated Origin Pulls")
	}
}

// TestIntegration_FullSecurityAudit runs a complete security check on Cloudflare configuration.
func TestIntegration_FullSecurityAudit(t *testing.T) {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	zoneID := os.Getenv("CLOUDFLARE_ZONE_ID")

	if token == "" || zoneID == "" {
		t.Skip("Skipping integration test: CLOUDFLARE_API_TOKEN or CLOUDFLARE_ZONE_ID not set")
	}

	client, err := newTestClient(token, zoneID)
	if err != nil {
		t.Fatalf("Failed to create Cloudflare client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Log("=== Cloudflare Security Audit ===")

	// Check 1: SSL Mode
	t.Run("SSL Mode", func(t *testing.T) {
		mode, err := client.GetSSLMode(ctx)
		if err != nil {
			t.Fatalf("Failed to get SSL mode: %v", err)
		}
		t.Logf("SSL Mode: %s", mode)

		switch mode {
		case "strict":
			t.Log("✓ EXCELLENT: Full (strict) mode - maximum security")
		case "full":
			t.Log("✓ GOOD: Full mode - secure for self-signed or Cloudflare Origin CA certs")
		case "flexible":
			t.Error("✗ INSECURE: Flexible mode - origin connection is HTTP!")
		case "off":
			t.Error("✗ CRITICAL: SSL is OFF!")
		default:
			t.Logf("? Unknown mode: %s", mode)
		}
	})

	// Check 2: Authenticated Origin Pull
	t.Run("Authenticated Origin Pull", func(t *testing.T) {
		enabled, err := client.IsAuthenticatedOriginPullEnabled(ctx)
		if err != nil {
			t.Fatalf("Failed to check AOP: %v", err)
		}
		t.Logf("Authenticated Origin Pull: %v", enabled)

		if enabled {
			t.Log("✓ GOOD: mTLS enabled - only Cloudflare can reach origin")
		} else {
			t.Error("✗ WARNING: mTLS disabled - origin directly reachable if IP is known")
		}
	})

	// Check 3: Zone info
	t.Run("Zone Info", func(t *testing.T) {
		zone, err := client.GetZoneInfo(ctx)
		if err != nil {
			t.Fatalf("Failed to get zone info: %v", err)
		}
		t.Logf("Zone: %s (ID: %s)", zone.Name, zone.ID)
		t.Logf("Status: %s", zone.Status)
		t.Logf("Plan: %s", zone.Plan.Name)
	})

	t.Log("=== End Security Audit ===")
}

// newTestClient creates a minimal client for testing
func newTestClient(token, zoneID string) (*Client, error) {
	api, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, err
	}

	return &Client{
		api:         api,
		zoneID:      zoneID,
		recordCache: make(map[string]string),
	}, nil
}
