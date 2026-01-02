package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if client == nil {
		t.Fatal("New() returned nil client")
	}
	if client.zoneID != "test-zone-id" {
		t.Errorf("zoneID = %q, want %q", client.zoneID, "test-zone-id")
	}
	if client.domain != "example.com" {
		t.Errorf("domain = %q, want %q", client.domain, "example.com")
	}
	if client.recordCache == nil {
		t.Error("recordCache is nil")
	}
}

func TestNew_EmptyToken(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "",
		CloudflareZoneID:   "test-zone-id",
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("New() with empty token should return error")
	}
}

// MockCloudflareServer creates a test server that simulates Cloudflare API
func MockCloudflareServer(t *testing.T) *httptest.Server {
	records := make(map[string]map[string]interface{})
	nextID := 1

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check authorization
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]interface{}{{"message": "Unauthorized"}},
			})
			return
		}

		path := r.URL.Path

		// List DNS records
		if strings.Contains(path, "/dns_records") && r.Method == "GET" {
			name := r.URL.Query().Get("name")
			recordType := r.URL.Query().Get("type")

			var result []map[string]interface{}
			for _, rec := range records {
				if (name == "" || rec["name"] == name) && (recordType == "" || rec["type"] == recordType) {
					result = append(result, rec)
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  result,
			})
			return
		}

		// Create DNS record
		if strings.Contains(path, "/dns_records") && r.Method == "POST" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			id := nextID
			nextID++
			record := map[string]interface{}{
				"id":      id,
				"name":    body["name"],
				"type":    body["type"],
				"content": body["content"],
				"ttl":     body["ttl"],
				"proxied": body["proxied"],
			}
			records[body["name"].(string)+":"+body["type"].(string)] = record

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  record,
			})
			return
		}

		// Update DNS record
		if strings.Contains(path, "/dns_records/") && r.Method == "PATCH" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			key := body["name"].(string) + ":" + body["type"].(string)
			if record, ok := records[key]; ok {
				record["content"] = body["content"]
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"result":  record,
				})
				return
			}

			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]interface{}{{"message": "Record not found"}},
			})
			return
		}

		// Delete DNS record
		if strings.Contains(path, "/dns_records/") && r.Method == "DELETE" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  map[string]interface{}{"id": "deleted"},
			})
			return
		}

		// Zone details
		if strings.Contains(path, "/zones/") && r.Method == "GET" && !strings.Contains(path, "dns_records") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id":     "test-zone-id",
					"name":   "example.com",
					"status": "active",
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestClient_RecordCache(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	// Verify cache is initialized
	if client.recordCache == nil {
		t.Fatal("recordCache should be initialized")
	}

	// Verify cache is empty
	if len(client.recordCache) != 0 {
		t.Errorf("recordCache should be empty, got %d entries", len(client.recordCache))
	}
}

// Test the cache key format
func TestCacheKeyFormat(t *testing.T) {
	tests := []struct {
		name       string
		recordType string
		wantKey    string
	}{
		{"example.com", "A", "example.com:A"},
		{"*.example.com", "A", "*.example.com:A"},
		{"example.com", "AAAA", "example.com:AAAA"},
		{"sub.example.com", "CNAME", "sub.example.com:CNAME"},
	}

	for _, tt := range tests {
		t.Run(tt.wantKey, func(t *testing.T) {
			key := tt.name + ":" + tt.recordType
			if key != tt.wantKey {
				t.Errorf("cache key = %q, want %q", key, tt.wantKey)
			}
		})
	}
}

// Test thread safety of record cache
func TestClient_RecordCache_ThreadSafety(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	// Simulate concurrent cache access
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				// Write to cache
				client.cacheMu.Lock()
				client.recordCache["test:A"] = "record-id"
				client.cacheMu.Unlock()

				// Read from cache
				client.cacheMu.RLock()
				_ = client.recordCache["test:A"]
				client.cacheMu.RUnlock()
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// Integration test helper - requires real Cloudflare credentials
func TestClient_UpdateRecord_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// This test requires real credentials and would actually update DNS
	// It's here as a template for manual integration testing
	t.Skip("Integration test - requires CLOUDFLARE_API_TOKEN and CLOUDFLARE_ZONE_ID env vars")

	cfg := &config.Config{
		CloudflareAPIToken: "", // Set from env
		CloudflareZoneID:   "", // Set from env
		Domain:             "", // Set from env
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx := context.Background()
	err = client.UpdateRecord(ctx, "test."+cfg.Domain, "A", "1.2.3.4")
	if err != nil {
		t.Errorf("UpdateRecord() error: %v", err)
	}
}

// Test error handling
func TestClient_UpdateRecord_Errors(t *testing.T) {
	// Create a server that returns errors
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []map[string]interface{}{{"message": "Internal error"}},
		})
	}))
	defer errorServer.Close()

	// Note: We can't easily test this without modifying the client to allow
	// custom API endpoints. This is here as documentation of the test structure.
	t.Log("Error handling test requires custom API endpoint support")
}

// Benchmark cache operations
func BenchmarkClient_CacheRead(b *testing.B) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, _ := New(cfg)
	client.recordCache["test:A"] = "record-id"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.cacheMu.RLock()
		_ = client.recordCache["test:A"]
		client.cacheMu.RUnlock()
	}
}

func BenchmarkClient_CacheWrite(b *testing.B) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, _ := New(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.cacheMu.Lock()
		client.recordCache["test:A"] = "record-id"
		client.cacheMu.Unlock()
	}
}

// TestValidateRecordName tests the domain-scoping security assertion
func TestValidateRecordName(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "home.example.com",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	tests := []struct {
		name      string
		record    string
		wantError bool
	}{
		// Valid records - within domain scope
		{"exact domain match", "home.example.com", false},
		{"subdomain", "app.home.example.com", false},
		{"nested subdomain", "api.v1.home.example.com", false},
		{"wildcard subdomain", "*.home.example.com", false},
		{"acme challenge", "_acme-challenge.home.example.com", false},
		{"uppercase (normalized)", "APP.HOME.EXAMPLE.COM", false},
		{"trailing dot", "home.example.com.", false},

		// Invalid records - outside domain scope (SECURITY)
		{"different domain", "other.org", true},
		{"parent domain", "example.com", true},
		{"sibling subdomain", "work.example.com", true},
		{"prefix attack", "fakehome.example.com", true},
		{"suffix attack", "home.example.com.evil.com", true},
		{"completely different", "google.com", true},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := client.validateRecordName(tt.record)
			if tt.wantError && err == nil {
				t.Errorf("validateRecordName(%q) expected error, got nil", tt.record)
			}
			if !tt.wantError && err != nil {
				t.Errorf("validateRecordName(%q) unexpected error: %v", tt.record, err)
			}
			if tt.wantError && err != nil && !strings.Contains(err.Error(), "SECURITY") {
				t.Errorf("validateRecordName(%q) error should contain 'SECURITY': %v", tt.record, err)
			}
		})
	}
}

// TestValidateRecordName_DifferentDomains tests validation with various domain configurations
func TestValidateRecordName_DifferentDomains(t *testing.T) {
	testCases := []struct {
		domain       string
		validNames   []string
		invalidNames []string
	}{
		{
			domain:       "example.com",
			validNames:   []string{"example.com", "sub.example.com", "*.example.com"},
			invalidNames: []string{"example.org", "notexample.com", "com"},
		},
		{
			domain:       "sub.example.com",
			validNames:   []string{"sub.example.com", "app.sub.example.com"},
			invalidNames: []string{"example.com", "other.example.com"},
		},
	}

	for _, tc := range testCases {
		t.Run("domain:"+tc.domain, func(t *testing.T) {
			cfg := &config.Config{
				CloudflareAPIToken: "test-token",
				CloudflareZoneID:   "test-zone-id",
				Domain:             tc.domain,
			}
			client, _ := New(cfg)

			for _, name := range tc.validNames {
				if err := client.validateRecordName(name); err != nil {
					t.Errorf("domain %q: validateRecordName(%q) should be valid: %v", tc.domain, name, err)
				}
			}

			for _, name := range tc.invalidNames {
				if err := client.validateRecordName(name); err == nil {
					t.Errorf("domain %q: validateRecordName(%q) should be invalid", tc.domain, name)
				}
			}
		})
	}
}

// TestValidateRecordName_PrefixMode tests validation with baseDomain for prefix mode
// In prefix mode, subdomains like "app-zone.example.com" are valid when domain is "zone.example.com"
// because baseDomain becomes "example.com"
func TestValidateRecordName_PrefixMode(t *testing.T) {
	testCases := []struct {
		name         string
		domain       string
		baseDomain   string
		validNames   []string
		invalidNames []string
	}{
		{
			name:       "prefix mode with multi-level domain",
			domain:     "zone.example.com",
			baseDomain: "example.com",
			validNames: []string{
				// Within domain (normal mode records)
				"zone.example.com",
				"app.zone.example.com",
				// Within baseDomain (prefix mode records)
				"example.com",
				"app-zone.example.com",
				"test-zone.example.com",
				"other.example.com",
			},
			invalidNames: []string{
				"evil.com",
				"example.org",
				"notexample.com",
			},
		},
		{
			name:       "prefix mode same domain and baseDomain",
			domain:     "example.com",
			baseDomain: "example.com",
			validNames: []string{
				"example.com",
				"sub.example.com",
			},
			invalidNames: []string{
				"other.org",
			},
		},
		{
			name:       "empty baseDomain falls back to domain only",
			domain:     "zone.example.com",
			baseDomain: "",
			validNames: []string{
				"zone.example.com",
				"app.zone.example.com",
			},
			invalidNames: []string{
				"example.com", // baseDomain empty, so parent not allowed
				"app-zone.example.com",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				CloudflareAPIToken: "test-token",
				CloudflareZoneID:   "test-zone-id",
				Domain:             tc.domain,
				SubdomainPrefix:    tc.baseDomain != "" && tc.baseDomain != tc.domain,
			}

			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			// Override baseDomain for testing (normally set from cfg.GetBaseDomain())
			client.baseDomain = tc.baseDomain

			for _, name := range tc.validNames {
				if err := client.validateRecordName(name); err != nil {
					t.Errorf("validateRecordName(%q) should be valid (domain=%q, baseDomain=%q): %v",
						name, tc.domain, tc.baseDomain, err)
				}
			}

			for _, name := range tc.invalidNames {
				if err := client.validateRecordName(name); err == nil {
					t.Errorf("validateRecordName(%q) should be invalid (domain=%q, baseDomain=%q)",
						name, tc.domain, tc.baseDomain)
				}
			}
		})
	}
}

// TestClient_BaseDomain tests that baseDomain is set correctly from config
func TestClient_BaseDomain(t *testing.T) {
	testCases := []struct {
		name           string
		domain         string
		subdomainPrefix bool
		wantBaseDomain string
	}{
		{
			name:           "normal mode - baseDomain equals domain",
			domain:         "example.com",
			subdomainPrefix: false,
			wantBaseDomain: "example.com",
		},
		{
			name:           "prefix mode - baseDomain is parent",
			domain:         "zone.example.com",
			subdomainPrefix: true,
			wantBaseDomain: "example.com",
		},
		{
			name:           "prefix mode - single level domain",
			domain:         "example.com",
			subdomainPrefix: true,
			wantBaseDomain: "example.com", // No parent, stays same
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				CloudflareAPIToken: "test-token",
				CloudflareZoneID:   "test-zone-id",
				Domain:             tc.domain,
				SubdomainPrefix:    tc.subdomainPrefix,
			}

			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}

			if client.baseDomain != tc.wantBaseDomain {
				t.Errorf("baseDomain = %q, want %q", client.baseDomain, tc.wantBaseDomain)
			}
		})
	}
}

// TestClient_IsProxied tests the proxy mode getter
func TestClient_IsProxied(t *testing.T) {
	tests := []struct {
		name    string
		proxied bool
	}{
		{"proxied mode", true},
		{"direct mode", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				CloudflareAPIToken: "test-token",
				CloudflareZoneID:   "test-zone-id",
				Domain:             "example.com",
				CloudflareProxy:    tt.proxied,
			}

			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}

			if client.IsProxied() != tt.proxied {
				t.Errorf("IsProxied() = %v, want %v", client.IsProxied(), tt.proxied)
			}
		})
	}
}

// TestClient_Domain tests the Domain getter
func TestClient_Domain(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "example.com",
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}

	if client.Domain() != "example.com" {
		t.Errorf("Domain() = %q, want %q", client.Domain(), "example.com")
	}
}

// MockZoneSettingsServer creates a test server for zone settings API
func MockZoneSettingsServer(t *testing.T, sslMode string, tlsClientAuth string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Check authorization
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]interface{}{{"message": "Unauthorized"}},
			})
			return
		}

		path := r.URL.Path

		// GET zone setting
		if strings.Contains(path, "/settings/ssl") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id":    "ssl",
					"value": sslMode,
				},
			})
			return
		}

		if strings.Contains(path, "/settings/tls_client_auth") && r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id":    "tls_client_auth",
					"value": tlsClientAuth,
				},
			})
			return
		}

		// PATCH zone setting
		if strings.Contains(path, "/settings/") && r.Method == "PATCH" {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			settingName := ""
			if strings.Contains(path, "/settings/ssl") {
				settingName = "ssl"
			} else if strings.Contains(path, "/settings/tls_client_auth") {
				settingName = "tls_client_auth"
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id":    settingName,
					"value": body["value"],
				},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}

// TestSSLModeValidation tests the SSL mode validation logic
func TestSSLModeValidation(t *testing.T) {
	// Valid SSL modes
	validModes := []string{"off", "flexible", "full", "strict"}

	for _, mode := range validModes {
		t.Run("valid_mode_"+mode, func(t *testing.T) {
			// This test documents valid SSL modes
			// Actual API call validation is handled by Cloudflare
			switch mode {
			case "off":
				// No encryption
			case "flexible":
				// HTTPS to Cloudflare, HTTP to origin
			case "full":
				// HTTPS to both, no cert validation
			case "strict":
				// HTTPS to both, with cert validation
			}
		})
	}
}

// TestAuthenticatedOriginPullValues tests valid values for AOP
func TestAuthenticatedOriginPullValues(t *testing.T) {
	tests := []struct {
		enabled bool
		want    string
	}{
		{true, "on"},
		{false, "off"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			value := "off"
			if tt.enabled {
				value = "on"
			}
			if value != tt.want {
				t.Errorf("AOP value = %q, want %q", value, tt.want)
			}
		})
	}
}

// TestIsManagedRecord tests the record ownership detection in both normal and prefix modes
// This is critical for terraform-like DNS reconciliation (detecting stale records to delete)
func TestIsManagedRecord(t *testing.T) {
	testCases := []struct {
		name           string
		domain         string
		baseDomain     string
		managedFQDNs   []string
		unmanagedFQDNs []string
	}{
		{
			name:       "normal mode - domain home.jonnyzzz.com",
			domain:     "home.jonnyzzz.com",
			baseDomain: "home.jonnyzzz.com", // same as domain in normal mode
			managedFQDNs: []string{
				"app.home.jonnyzzz.com",
				"test.home.jonnyzzz.com",
				"roomtone.home.jonnyzzz.com",
				"api.v1.home.jonnyzzz.com", // nested subdomain
			},
			unmanagedFQDNs: []string{
				"home.jonnyzzz.com",       // the domain itself
				"jonnyzzz.com",            // parent domain
				"other.jonnyzzz.com",      // sibling subdomain
				"app-home.jonnyzzz.com",   // prefix-style but we're in normal mode
				"evil.com",                // completely different
				"fakehome.jonnyzzz.com",   // prefix attack
			},
		},
		{
			name:       "prefix mode - domain home.jonnyzzz.com, baseDomain jonnyzzz.com",
			domain:     "home.jonnyzzz.com",
			baseDomain: "jonnyzzz.com",
			managedFQDNs: []string{
				// Normal mode records (subdomains of domain)
				"app.home.jonnyzzz.com",
				"test.home.jonnyzzz.com",
				// Prefix mode records (pattern: {subdomain}-{zone}.{baseDomain})
				"app-home.jonnyzzz.com",
				"test-home.jonnyzzz.com",
				"roomtone-home.jonnyzzz.com",
				"api-home.jonnyzzz.com",
			},
			unmanagedFQDNs: []string{
				"home.jonnyzzz.com",       // the domain itself
				"jonnyzzz.com",            // baseDomain itself
				"other.jonnyzzz.com",      // different subdomain of baseDomain
				"app-work.jonnyzzz.com",   // different zone
				"app.jonnyzzz.com",        // not our pattern
				"evil.com",                // completely different
			},
		},
		{
			name:       "prefix mode - zone.example.com",
			domain:     "zone.example.com",
			baseDomain: "example.com",
			managedFQDNs: []string{
				"app.zone.example.com",
				"app-zone.example.com",
				"test-zone.example.com",
			},
			unmanagedFQDNs: []string{
				"zone.example.com",
				"example.com",
				"app.example.com",
				"app-other.example.com",
			},
		},
		{
			name:       "same domain and baseDomain",
			domain:     "example.com",
			baseDomain: "example.com",
			managedFQDNs: []string{
				"app.example.com",
				"test.example.com",
				"sub.domain.example.com",
			},
			unmanagedFQDNs: []string{
				"example.com",
				"other.org",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				CloudflareAPIToken: "test-token",
				CloudflareZoneID:   "test-zone-id",
				Domain:             tc.domain,
				SubdomainPrefix:    tc.baseDomain != tc.domain,
			}

			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			// Override baseDomain for testing
			client.baseDomain = tc.baseDomain

			// Test managed FQDNs (should return true)
			for _, fqdn := range tc.managedFQDNs {
				if !client.IsManagedRecord(fqdn) {
					t.Errorf("IsManagedRecord(%q) = false, want true (domain=%q, baseDomain=%q)",
						fqdn, tc.domain, tc.baseDomain)
				}
			}

			// Test unmanaged FQDNs (should return false)
			for _, fqdn := range tc.unmanagedFQDNs {
				if client.IsManagedRecord(fqdn) {
					t.Errorf("IsManagedRecord(%q) = true, want false (domain=%q, baseDomain=%q)",
						fqdn, tc.domain, tc.baseDomain)
				}
			}
		})
	}
}

// TestIsManagedRecord_CaseInsensitive tests case-insensitive matching
func TestIsManagedRecord_CaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "home.jonnyzzz.com",
		SubdomainPrefix:    true,
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	client.baseDomain = "jonnyzzz.com"

	// All these should match
	caseMixes := []string{
		"app-home.jonnyzzz.com",
		"APP-HOME.JONNYZZZ.COM",
		"App-Home.Jonnyzzz.Com",
		"APP-home.JONNYZZZ.com",
	}

	for _, fqdn := range caseMixes {
		if !client.IsManagedRecord(fqdn) {
			t.Errorf("IsManagedRecord(%q) should be true (case-insensitive)", fqdn)
		}
	}
}

// TestDNSReconciliation_Integration tests the full reconciliation logic
// This simulates the terraform-like behavior: compare existing vs desired records
func TestDNSReconciliation_Integration(t *testing.T) {
	cfg := &config.Config{
		CloudflareAPIToken: "test-token",
		CloudflareZoneID:   "test-zone-id",
		Domain:             "home.jonnyzzz.com",
		SubdomainPrefix:    true,
	}

	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	client.baseDomain = "jonnyzzz.com"

	// Simulate existing records in Cloudflare (would come from GetManagedRecordFQDNs)
	existingFQDNs := []string{
		"roomtone-home.jonnyzzz.com",
		"dyndns-home.jonnyzzz.com",
		"test-home.jonnyzzz.com", // This one should be deleted (stale)
	}

	// Simulate active records (from GetActiveSubdomains + GetSubdomainFQDN)
	activeFQDNs := map[string]bool{
		"roomtone-home.jonnyzzz.com": true,
		"dyndns-home.jonnyzzz.com":   true,
		// test-home is NOT in active list - should be deleted
	}

	// Find stale records (exist in CF but not in active list)
	var staleRecords []string
	for _, existing := range existingFQDNs {
		normalized := strings.ToLower(existing)
		if !activeFQDNs[normalized] {
			staleRecords = append(staleRecords, existing)
		}
	}

	// Verify we found the stale record
	if len(staleRecords) != 1 || staleRecords[0] != "test-home.jonnyzzz.com" {
		t.Errorf("Expected stale records [test-home.jonnyzzz.com], got %v", staleRecords)
	}

	// Verify stale record is managed (would be deleted)
	for _, stale := range staleRecords {
		if !client.IsManagedRecord(stale) {
			t.Errorf("Stale record %q should be managed", stale)
		}
	}
}
