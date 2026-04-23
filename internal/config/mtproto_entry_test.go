package config

import (
	"os"
	"testing"
)

// TestLoad_DisableIPv6 exercises the DISABLE_IPV6 env-var flag. The value
// must survive through Load so downstream AAAA-suppression paths see it.
func TestLoad_DisableIPv6(t *testing.T) {
	defaults := map[string]string{
		"CLOUDFLARE_API_TOKEN": "tok",
		"CLOUDFLARE_ZONE_ID":   "zone",
		"DOMAIN":               "example.com",
		"ACME_EMAIL":           "a@example.com",
	}
	for k, v := range defaults {
		t.Setenv(k, v)
	}

	cases := []struct {
		val  string
		want bool
	}{
		{"", false},
		{"false", false},
		{"true", true},
		{"1", true},
		{"yes", true},
	}
	for _, tc := range cases {
		t.Run("DISABLE_IPV6="+tc.val, func(t *testing.T) {
			os.Unsetenv("DISABLE_IPV6")
			if tc.val != "" {
				t.Setenv("DISABLE_IPV6", tc.val)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.DisableIPv6 != tc.want {
				t.Errorf("DisableIPv6 = %v, want %v", cfg.DisableIPv6, tc.want)
			}
		})
	}
}

func TestResolveMTProtoEntry(t *testing.T) {
	tests := []struct {
		name          string
		domain        string
		prefixMode    bool
		entry         string
		wantLabel     string
		wantFQDN      string
	}{
		{
			name:      "short label in prefix mode gets prefix-transformed",
			domain:    "zone33.example.com",
			prefixMode: true,
			entry:     "mtp",
			wantLabel: "mtp",
			wantFQDN:  "mtp-zone33.example.com",
		},
		{
			name:      "short label in normal mode",
			domain:    "example.com",
			prefixMode: false,
			entry:     "mtp",
			wantLabel: "mtp",
			wantFQDN:  "mtp.example.com",
		},
		{
			name:      "FQDN sibling under base zone passes through verbatim",
			domain:    "zone33.example.com",
			prefixMode: true,
			entry:     "zone451.example.com",
			wantLabel: "zone451",
			wantFQDN:  "zone451.example.com",
		},
		{
			name:      "FQDN matching the current zone passes through",
			domain:    "zone33.example.com",
			prefixMode: true,
			entry:     "foo.bar.example.com",
			wantLabel: "foo",
			wantFQDN:  "foo.bar.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Domain: tt.domain, SubdomainPrefix: tt.prefixMode}
			label, fqdn := cfg.ResolveMTProtoEntry(tt.entry)
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
			if fqdn != tt.wantFQDN {
				t.Errorf("fqdn = %q, want %q", fqdn, tt.wantFQDN)
			}
			// Cross-check: GetSubdomainFQDN should preserve FQDN-shaped input.
			if got := cfg.GetSubdomainFQDN(fqdn); got != fqdn {
				t.Errorf("GetSubdomainFQDN(%q) = %q, want verbatim %q", fqdn, got, fqdn)
			}
		})
	}
}
