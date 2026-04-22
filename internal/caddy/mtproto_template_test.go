package caddy

import (
	"strings"
	"testing"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
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

	// That block responds 200 OK and has no reverse_proxy.
	block := blockAfter(t, content, marker)
	if !strings.Contains(block, `respond "OK" 200`) {
		t.Errorf("MTProto site must respond 200 OK:\n%s", block)
	}
	if strings.Contains(block, "reverse_proxy") {
		t.Errorf("MTProto site must not contain reverse_proxy:\n%s", block)
	}
	if strings.Contains(block, "client_auth") {
		t.Errorf("MTProto site must not require client certificate:\n%s", block)
	}

	// Global options enforce loopback binding and port override.
	if !strings.Contains(content, "https_port 8443") {
		t.Errorf("expected https_port 8443 in globals:\n%s", content)
	}
	if !strings.Contains(content, "default_bind 127.0.0.1") {
		t.Errorf("expected default_bind 127.0.0.1 in globals:\n%s", content)
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
