package caddy

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"text/template"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
)

// Generator generates Caddyfile configuration from templates and mappings
type Generator struct {
	cfg        *config.Config
	mappingMgr *mapping.Manager

	// Discovery services (from stevedore socket API)
	discoveredServices []discovery.Service
	mu                 sync.RWMutex

	// TemplatePath allows overriding the default template path (for testing)
	TemplatePath string
	// TemplateContent allows providing template content directly (for testing)
	TemplateContent string
}

// TemplateData contains data passed to the Caddyfile template
type TemplateData struct {
	Domain          string
	AcmeEmail       string
	LogLevel        string
	SubdomainPrefix bool   // Use prefix mode (subdomain-basedomain.parent)
	BaseDomain      string // Parent domain in prefix mode (e.g., example.com)
	CloudflareProxy bool   // Use Cloudflare proxy mode with mTLS
	// CatchallFQDN, when non-empty, enables the 451 catchall site block
	// and is also used as default_sni in the global Caddy options.
	CatchallFQDN  string
	ProxyMappings []MappingData // Subdomains routed via the CF-proxy+mTLS block
	DirectMappings []MappingData // Subdomains served directly (own LE cert, no mTLS)
	// Mappings is kept for legacy template/test use: it is the concatenation of
	// ProxyMappings followed by DirectMappings.
	Mappings []MappingData
}

// MappingData represents a mapping in the template
type MappingData struct {
	Subdomain string // Original subdomain name (for @matcher naming)
	FQDN      string // Full domain name (e.g., app-zone.example.com)
	Target    string
	Options   mapping.MappingOptions
	// Direct marks this subdomain as direct-mode (own LE cert, no mTLS).
	Direct bool
}

// New creates a new Caddy configuration generator
func New(cfg *config.Config, mappingMgr *mapping.Manager) *Generator {
	return &Generator{
		cfg:        cfg,
		mappingMgr: mappingMgr,
	}
}

// UpdateDiscoveredServices updates the list of services from stevedore discovery
func (g *Generator) UpdateDiscoveredServices(services []discovery.Service) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.discoveredServices = services
}

// Generate creates the Caddyfile from template and current mappings/services
func (g *Generator) Generate() error {
	content, err := g.GenerateContent()
	if err != nil {
		return err
	}

	// Write Caddyfile
	changed, err := writeFileIfChanged(g.cfg.CaddyFile, []byte(content))
	if err != nil {
		return err
	}
	if !changed {
		slog.Debug("Caddyfile unchanged, skipping reload", "path", g.cfg.CaddyFile, "mappings", len(g.collectMappings()))
		return nil
	}

	slog.Info("Generated Caddyfile", "path", g.cfg.CaddyFile, "mappings", len(g.collectMappings()))

	// Reload Caddy (if running)
	if err := g.reloadCaddy(); err != nil {
		slog.Warn("Failed to reload Caddy", "error", err)
	}

	return nil
}

func writeFileIfChanged(path string, content []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, content) {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("failed to read Caddyfile: %w", err)
	}

	if err := os.WriteFile(path, content, 0644); err != nil {
		return false, fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	return true, nil
}

// GenerateContent generates the Caddyfile content as a string without writing to disk.
// This is useful for testing and validation.
func (g *Generator) GenerateContent() (string, error) {
	// Get template content
	var tmplContent string
	if g.TemplateContent != "" {
		tmplContent = g.TemplateContent
	} else {
		tmplPath := g.TemplatePath
		if tmplPath == "" {
			tmplPath = "/etc/caddy/Caddyfile.template"
		}
		content, err := os.ReadFile(tmplPath)
		if err != nil {
			return "", fmt.Errorf("failed to read template: %w", err)
		}
		tmplContent = string(content)
	}

	funcMap := template.FuncMap{
		"default": func(def, val string) string {
			if val == "" {
				return def
			}
			return val
		},
	}

	tmpl, err := template.New("Caddyfile").Funcs(funcMap).Parse(tmplContent)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Prepare template data - combine mappings and discovered services
	mappingData := g.collectMappings()
	proxyMappings, directMappings := splitMappings(mappingData)

	data := TemplateData{
		Domain:          g.cfg.Domain,
		AcmeEmail:       g.cfg.AcmeEmail,
		LogLevel:        g.cfg.LogLevel,
		SubdomainPrefix: g.cfg.SubdomainPrefix,
		BaseDomain:      g.cfg.GetBaseDomain(),
		CloudflareProxy: g.cfg.CloudflareProxy,
		CatchallFQDN:    g.catchallFQDN(),
		ProxyMappings:   proxyMappings,
		DirectMappings:  directMappings,
		Mappings:        mappingData,
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// GetTemplateData returns the template data that would be used for generation.
// This is useful for testing template rendering.
func (g *Generator) GetTemplateData() TemplateData {
	mappings := g.collectMappings()
	proxy, direct := splitMappings(mappings)
	return TemplateData{
		Domain:          g.cfg.Domain,
		AcmeEmail:       g.cfg.AcmeEmail,
		LogLevel:        g.cfg.LogLevel,
		SubdomainPrefix: g.cfg.SubdomainPrefix,
		BaseDomain:      g.cfg.GetBaseDomain(),
		CloudflareProxy: g.cfg.CloudflareProxy,
		CatchallFQDN:    g.catchallFQDN(),
		ProxyMappings:   proxy,
		DirectMappings:  direct,
		Mappings:        mappings,
	}
}

// splitMappings separates a flat mapping list into proxy-mode and direct-mode
// slices, preserving relative order within each group.
func splitMappings(all []MappingData) (proxy, direct []MappingData) {
	for _, m := range all {
		if m.Direct {
			direct = append(direct, m)
		} else {
			proxy = append(proxy, m)
		}
	}
	return
}

// catchallFQDN returns the fully-qualified domain name for the 451 catchall
// site, or the empty string when the feature is disabled.
func (g *Generator) catchallFQDN() string {
	if g.cfg.CatchallSubdomain == "" {
		return ""
	}
	return g.cfg.GetSubdomainFQDN(g.cfg.CatchallSubdomain)
}

// GetActiveSubdomains returns a list of all currently active subdomains
func (g *Generator) GetActiveSubdomains() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	seen := make(map[string]bool)
	var result []string

	// From discovered services
	for _, svc := range g.discoveredServices {
		if !seen[svc.Subdomain] {
			seen[svc.Subdomain] = true
			result = append(result, svc.Subdomain)
		}
	}

	// From YAML mappings
	if g.mappingMgr != nil {
		for _, m := range g.mappingMgr.Get() {
			if !seen[m.Subdomain] {
				seen[m.Subdomain] = true
				result = append(result, m.Subdomain)
			}
		}
	}

	return result
}

// IsSubdomainDirect returns true when the given subdomain was discovered with
// the direct-mode flag set. Unknown subdomains (including YAML mappings) return
// false. Callers that need the catchall treated as direct should check
// separately via CatchallSubdomain.
func (g *Generator) IsSubdomainDirect(subdomain string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	for _, svc := range g.discoveredServices {
		if svc.Subdomain == subdomain {
			return svc.Direct
		}
	}
	return false
}

// collectMappings gathers all mappings from both YAML files and discovery
func (g *Generator) collectMappings() []MappingData {
	seen := make(map[string]bool)
	var result []MappingData

	// First, add discovered services (higher priority)
	g.mu.RLock()
	for _, svc := range g.discoveredServices {
		if seen[svc.Subdomain] {
			slog.Warn("Duplicate subdomain in discovered services", "subdomain", svc.Subdomain)
			continue
		}
		seen[svc.Subdomain] = true
		result = append(result, MappingData{
			Subdomain: svc.Subdomain,
			FQDN:      g.cfg.GetSubdomainFQDN(svc.Subdomain),
			Target:    svc.GetTarget(),
			Options: mapping.MappingOptions{
				Websocket:  svc.Websocket,
				HealthPath: svc.GetHealthPath(),
			},
			Direct: svc.Direct,
		})
	}
	g.mu.RUnlock()

	// Then, add YAML mappings (only if subdomain not already used)
	if g.mappingMgr != nil {
		for _, m := range g.mappingMgr.Get() {
			if seen[m.Subdomain] {
				slog.Debug("Skipping YAML mapping, subdomain used by discovered service", "subdomain", m.Subdomain)
				continue
			}
			seen[m.Subdomain] = true
			result = append(result, MappingData{
				Subdomain: m.Subdomain,
				FQDN:      g.cfg.GetSubdomainFQDN(m.Subdomain),
				Target:    m.GetTarget(),
				Options:   m.Options,
			})
		}
	}

	return result
}

func (g *Generator) reloadCaddy() error {
	// Send SIGUSR1 to Caddy to trigger config reload
	// This is handled by the entrypoint script which manages both processes
	slog.Debug("Caddy reload requested")
	return nil
}
