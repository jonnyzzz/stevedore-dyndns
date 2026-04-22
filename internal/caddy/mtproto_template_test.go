package caddy

import (
	"strings"
	"testing"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
)

func TestGenerate_MTProtoSiteRespondsOK(t *testing.T) {
	cfg := &config.Config{
		Domain:                "zone.example.com",
		AcmeEmail:             "admin@example.com",
		LogLevel:              "info",
		SubdomainPrefix:       true,
		CloudflareProxy:       true,
		MTProtoDispatcher:     true,
		MTProtoSubdomains:     []string{"mtp"},
		MTProtoCaddyLoopback:  "127.0.0.1:8443",
	}
	g := newGeneratorWithDefaults(t, cfg)

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	// MTProto site block exists.
	marker := "mtp-zone.example.com {"
	if !strings.Contains(content, marker) {
		t.Errorf("MTProto site block not emitted:\n%s", content)
	}

	// That block responds with the fallback body and has no reverse_proxy.
	block := blockAfter(t, content, marker)
	if !strings.Contains(block, `respond "OK, it's 451" 200`) {
		t.Errorf("MTProto site must emit fallback body:\n%s", block)
	}
	if strings.Contains(block, "reverse_proxy") {
		t.Errorf("MTProto site must not contain reverse_proxy when unclaimed:\n%s", block)
	}
	if strings.Contains(block, "require_and_verify") {
		t.Errorf("MTProto site must not enforce mTLS:\n%s", block)
	}

	// Global options enforce loopback binding and port override.
	if !strings.Contains(content, "https_port 8443") {
		t.Errorf("expected https_port 8443 in globals:\n%s", content)
	}
	if !strings.Contains(content, "default_bind 127.0.0.1") {
		t.Errorf("expected default_bind 127.0.0.1 in globals:\n%s", content)
	}
}

func TestGenerate_MTProtoSiteReverseProxiesClaimingService(t *testing.T) {
	cfg := &config.Config{
		Domain:                "zone.example.com",
		AcmeEmail:             "admin@example.com",
		LogLevel:              "info",
		SubdomainPrefix:       true,
		CloudflareProxy:       true,
		MTProtoDispatcher:     true,
		MTProtoSubdomains:     []string{"mtp"},
		MTProtoCaddyLoopback:  "127.0.0.1:8443",
	}
	g := newGeneratorWithDefaults(t, cfg)
	g.UpdateDiscoveredServices([]discovery.Service{
		{Subdomain: "mtp", Port: 4000, HealthCheck: "/alive"},
	})

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	// MTProto site block exists and now reverse-proxies.
	marker := "mtp-zone.example.com {"
	if !strings.Contains(content, marker) {
		t.Fatalf("MTProto site block not emitted:\n%s", content)
	}
	block := blockAfter(t, content, marker)
	if !strings.Contains(block, "reverse_proxy 127.0.0.1:4000") {
		t.Errorf("MTProto site must reverse-proxy to claimed backend:\n%s", block)
	}
	if strings.Contains(block, `respond "OK, it's 451" 200`) {
		t.Errorf("MTProto site must NOT emit fallback when backend is claimed:\n%s", block)
	}
	if !strings.Contains(block, "health_uri /alive") {
		t.Errorf("MTProto site must honor service health path:\n%s", block)
	}

	// The claimed service must not also be rendered inside the proxy/direct
	// blocks — it belongs exclusively to the MTProto site now.
	// (Direct flag is false on this service, so it would otherwise land in
	// the wildcard proxy block as an @mtp matcher.)
	if strings.Contains(content, "@mtp host mtp-zone.example.com") {
		t.Errorf("claimed service must not also be rendered in the wildcard block:\n%s", content)
	}
}

// TestGenerate_MTProtoSiteFQDNEntryUsesHostnameVerbatim pins the behavior for
// sibling-zone bindings like zone451.example.com: the FQDN must survive
// prefix-mode transformation and appear verbatim in the Caddyfile site block.
func TestGenerate_MTProtoSiteFQDNEntryUsesHostnameVerbatim(t *testing.T) {
	cfg := &config.Config{
		Domain:               "zone33.example.com",
		AcmeEmail:            "admin@example.com",
		LogLevel:             "info",
		SubdomainPrefix:      true,
		CloudflareProxy:      true,
		MTProtoDispatcher:    true,
		MTProtoSubdomains:    []string{"zone451.example.com"},
		MTProtoCaddyLoopback: "127.0.0.1:8443",
	}
	g := newGeneratorWithDefaults(t, cfg)

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	marker := "zone451.example.com {"
	if !strings.Contains(content, marker) {
		t.Fatalf("FQDN-form MTProto site not emitted verbatim:\n%s", content)
	}
	// The accidental prefix-transformed name must NOT appear.
	if strings.Contains(content, "zone451.example.com-zone33.example.com") {
		t.Fatalf("prefix-mode transformation leaked into FQDN entry:\n%s", content)
	}
}

func TestGenerate_MTProtoDisabledKeepsDefaults(t *testing.T) {
	cfg := &config.Config{
		Domain:          "zone.example.com",
		AcmeEmail:       "admin@example.com",
		LogLevel:        "info",
		SubdomainPrefix: true,
		CloudflareProxy: true,
	}
	g := newGeneratorWithDefaults(t, cfg)

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}
	if strings.Contains(content, "https_port") {
		t.Errorf("https_port must NOT be emitted when MTProto dispatcher is off:\n%s", content)
	}
	if strings.Contains(content, "default_bind") {
		t.Errorf("default_bind must NOT be emitted when MTProto dispatcher is off:\n%s", content)
	}
}
