package caddy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
)

func newGeneratorWithDefaults(t *testing.T, cfg *config.Config) *Generator {
	t.Helper()
	templatePath := filepath.Join("..", "..", "Caddyfile.template")
	g := New(cfg, nil)
	g.TemplatePath = templatePath
	return g
}

func TestGenerate_DirectModeEmitsOwnSite(t *testing.T) {
	cfg := &config.Config{
		Domain:          "zone.example.com",
		AcmeEmail:       "admin@example.com",
		LogLevel:        "info",
		SubdomainPrefix: true,
		CloudflareProxy: true,
	}
	g := newGeneratorWithDefaults(t, cfg)
	g.UpdateDiscoveredServices([]discovery.Service{
		{Subdomain: "proxied", Port: 8080},                 // stays in wildcard block
		{Subdomain: "directapp", Port: 9090, Direct: true}, // own site block
	})

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	if !strings.Contains(content, "@proxied host proxied-zone.example.com") {
		t.Errorf("proxied service not rendered in wildcard block:\n%s", content)
	}

	// Direct site should appear as its own block with explicit FQDN matcher.
	if !strings.Contains(content, "directapp-zone.example.com {") {
		t.Errorf("direct service not rendered as explicit site block:\n%s", content)
	}

	// Direct site uses "client_auth { mode request }" as a policy differentiator
	// — NOT require_and_verify, which would force a cert.
	directBlock := blockAfter(t, content, "directapp-zone.example.com {")
	if !strings.Contains(directBlock, "dns cloudflare") {
		t.Errorf("direct site block must request DNS-01 challenge:\n%s", directBlock)
	}
	if strings.Contains(directBlock, "require_and_verify") {
		t.Errorf("direct site must not enforce mTLS (require_and_verify):\n%s", directBlock)
	}
	if !strings.Contains(directBlock, "mode request") {
		t.Errorf("direct site must carry client_auth mode=request (policy differentiator):\n%s", directBlock)
	}
}

func TestGenerate_UnknownHostReturns451InProxyBlock(t *testing.T) {
	cfg := &config.Config{
		Domain:          "zone.example.com",
		AcmeEmail:       "admin@example.com",
		LogLevel:        "info",
		SubdomainPrefix: true,
		CloudflareProxy: true,
	}
	g := newGeneratorWithDefaults(t, cfg)
	g.UpdateDiscoveredServices([]discovery.Service{
		{Subdomain: "app", Port: 8080},
	})

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	proxyBlock := blockAfter(t, content, "*.example.com, zone.example.com {")
	if !strings.Contains(proxyBlock, "respond \"451 Unavailable For Legal Reasons\" 451") {
		t.Errorf("proxy-block fallback should respond 451:\n%s", proxyBlock)
	}
	if strings.Contains(proxyBlock, "\"Not Found\" 404") {
		t.Errorf("legacy 404 fallback still present in proxy block:\n%s", proxyBlock)
	}
}

func TestGenerate_CatchallSiteAndDefaultSNI(t *testing.T) {
	cfg := &config.Config{
		Domain:            "zone.example.com",
		AcmeEmail:         "admin@example.com",
		LogLevel:          "info",
		SubdomainPrefix:   true,
		CloudflareProxy:   true,
		CatchallSubdomain: "catchall",
	}
	g := newGeneratorWithDefaults(t, cfg)
	g.UpdateDiscoveredServices([]discovery.Service{
		{Subdomain: "proxied", Port: 8080},
	})

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	if !strings.Contains(content, "default_sni catchall-zone.example.com") {
		t.Errorf("default_sni missing for catchall:\n%s", content)
	}
	if !strings.Contains(content, "catchall-zone.example.com {") {
		t.Errorf("catchall site block missing:\n%s", content)
	}

	catchall := blockAfter(t, content, "catchall-zone.example.com {")
	if !strings.Contains(catchall, "respond \"451 Unavailable For Legal Reasons\" 451") {
		t.Errorf("catchall should respond 451:\n%s", catchall)
	}
	if strings.Contains(catchall, "require_and_verify") {
		t.Errorf("catchall must not enforce mTLS:\n%s", catchall)
	}
}

func TestGenerate_CatchallDisabledByDefault(t *testing.T) {
	cfg := &config.Config{
		Domain:          "zone.example.com",
		AcmeEmail:       "admin@example.com",
		LogLevel:        "info",
		SubdomainPrefix: true,
		CloudflareProxy: true,
		// CatchallSubdomain intentionally empty
	}
	g := newGeneratorWithDefaults(t, cfg)
	g.UpdateDiscoveredServices([]discovery.Service{
		{Subdomain: "app", Port: 8080},
	})

	content, err := g.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent: %v", err)
	}

	if strings.Contains(content, "default_sni") {
		t.Errorf("default_sni must not be emitted when catchall disabled:\n%s", content)
	}
}

// blockAfter returns the contents of the Caddy block that starts with the
// given marker line (must end with "{"). It returns everything up to the
// matching "}".
func blockAfter(t *testing.T, content, marker string) string {
	t.Helper()
	idx := strings.Index(content, marker)
	if idx < 0 {
		t.Fatalf("marker %q not found in content", marker)
	}
	sub := content[idx+len(marker):]

	depth := 1
	for i, r := range sub {
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return sub[:i]
			}
		}
	}
	t.Fatalf("unterminated block after %q", marker)
	return ""
}
