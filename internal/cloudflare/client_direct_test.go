package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudflare/cloudflare-go"
	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
)

// TestUpdateRecordProxied_PerRecordFlag verifies that UpdateRecordProxied
// sends the requested proxied value regardless of the client's default, so a
// mixed-mode deployment can publish proxy and direct subdomains side-by-side.
func TestUpdateRecordProxied_PerRecordFlag(t *testing.T) {
	tests := []struct {
		name            string
		defaultProxied  bool
		requestProxied  bool
		wantProxiedJSON bool
	}{
		{"default-direct call-proxied", false, true, true},
		{"default-proxy call-direct", true, false, false},
		{"default-proxy call-proxied", true, true, true},
		{"default-direct call-direct", false, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotProxied *bool

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// List → empty, Create → record
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
					writeJSON(w, map[string]any{
						"result":  []any{},
						"success": true,
						"errors":  []any{},
					})
				case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dns_records"):
					var body struct {
						Proxied *bool `json:"proxied"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatalf("decode: %v", err)
					}
					gotProxied = body.Proxied
					writeJSON(w, map[string]any{
						"result":  map[string]any{"id": "rec_123"},
						"success": true,
						"errors":  []any{},
					})
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			defer srv.Close()

			api, err := cloudflare.NewWithAPIToken("test-token", cloudflare.BaseURL(srv.URL+"/client/v4"))
			if err != nil {
				t.Fatalf("cloudflare client: %v", err)
			}

			c := &Client{
				api:         api,
				zoneID:      "zone123",
				domain:      "example.com",
				baseDomain:  "example.com",
				proxied:     tc.defaultProxied,
				ttl:         60,
				recordCache: map[string]string{},
			}

			_ = (&config.Config{}) // keep import used
			if err := c.UpdateRecordProxied(context.Background(), "app.example.com", "A", "1.2.3.4", tc.requestProxied); err != nil {
				t.Fatalf("UpdateRecordProxied: %v", err)
			}

			if gotProxied == nil {
				t.Fatalf("server never received Proxied flag")
			}
			if *gotProxied != tc.wantProxiedJSON {
				t.Fatalf("Proxied sent to CF = %v, want %v (default=%v request=%v)",
					*gotProxied, tc.wantProxiedJSON, tc.defaultProxied, tc.requestProxied)
			}
		})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
