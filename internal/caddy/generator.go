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
	tmpl       *template.Template

	// Discovery services (from stevedore socket API)
	discoveredServices []discovery.Service
	mu                 sync.RWMutex
}

// TemplateData contains data passed to the Caddyfile template
type TemplateData struct {
	Domain    string
	AcmeEmail string
	LogLevel  string
	Mappings  []MappingData
}

// MappingData represents a mapping in the template
type MappingData struct {
	Subdomain string
	Target    string
	Options   mapping.MappingOptions
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
	// Load template
	tmplPath := "/etc/caddy/Caddyfile.template"
	tmplContent, err := os.ReadFile(tmplPath)
	if err != nil {
		return fmt.Errorf("failed to read template: %w", err)
	}

	funcMap := template.FuncMap{
		"default": func(def, val string) string {
			if val == "" {
				return def
			}
			return val
		},
	}

	tmpl, err := template.New("Caddyfile").Funcs(funcMap).Parse(string(tmplContent))
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	// Prepare template data - combine mappings and discovered services
	mappingData := g.collectMappings()

	data := TemplateData{
		Domain:    g.cfg.Domain,
		AcmeEmail: g.cfg.AcmeEmail,
		LogLevel:  g.cfg.LogLevel,
		Mappings:  mappingData,
	}

	// Execute template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	// Write Caddyfile
	if err := os.WriteFile(g.cfg.CaddyFile, buf.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write Caddyfile: %w", err)
	}

	slog.Info("Generated Caddyfile", "path", g.cfg.CaddyFile, "mappings", len(mappingData))

	// Reload Caddy (if running)
	if err := g.reloadCaddy(); err != nil {
		slog.Warn("Failed to reload Caddy", "error", err)
	}

	return nil
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
			Target:    svc.GetTarget(),
			Options: mapping.MappingOptions{
				Websocket:  svc.Websocket,
				HealthPath: svc.GetHealthPath(),
			},
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
