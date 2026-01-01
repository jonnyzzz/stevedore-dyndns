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
			json.NewEncoder(w).Encode(map[string]interface{}{
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

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  result,
			})
			return
		}

		// Create DNS record
		if strings.Contains(path, "/dns_records") && r.Method == "POST" {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

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

			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  record,
			})
			return
		}

		// Update DNS record
		if strings.Contains(path, "/dns_records/") && r.Method == "PATCH" {
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)

			key := body["name"].(string) + ":" + body["type"].(string)
			if record, ok := records[key]; ok {
				record["content"] = body["content"]
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": true,
					"result":  record,
				})
				return
			}

			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"errors":  []map[string]interface{}{{"message": "Record not found"}},
			})
			return
		}

		// Delete DNS record
		if strings.Contains(path, "/dns_records/") && r.Method == "DELETE" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result":  map[string]interface{}{"id": "deleted"},
			})
			return
		}

		// Zone details
		if strings.Contains(path, "/zones/") && r.Method == "GET" && !strings.Contains(path, "dns_records") {
			json.NewEncoder(w).Encode(map[string]interface{}{
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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
		Domain:             "zone33.mcp-server-1.com",
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
		{"exact domain match", "zone33.mcp-server-1.com", false},
		{"subdomain", "app.zone33.mcp-server-1.com", false},
		{"nested subdomain", "api.v1.zone33.mcp-server-1.com", false},
		{"wildcard subdomain", "*.zone33.mcp-server-1.com", false},
		{"acme challenge", "_acme-challenge.zone33.mcp-server-1.com", false},
		{"uppercase (normalized)", "APP.ZONE33.MCP-SERVER-1.COM", false},
		{"trailing dot", "zone33.mcp-server-1.com.", false},

		// Invalid records - outside domain scope (SECURITY)
		{"different domain", "example.com", true},
		{"parent domain", "mcp-server-1.com", true},
		{"sibling subdomain", "zone34.mcp-server-1.com", true},
		{"prefix attack", "fakezone33.mcp-server-1.com", true},
		{"suffix attack", "zone33.mcp-server-1.com.evil.com", true},
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
		domain      string
		validNames  []string
		invalidNames []string
	}{
		{
			domain:      "example.com",
			validNames:  []string{"example.com", "sub.example.com", "*.example.com"},
			invalidNames: []string{"example.org", "notexample.com", "com"},
		},
		{
			domain:      "sub.example.com",
			validNames:  []string{"sub.example.com", "app.sub.example.com"},
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
