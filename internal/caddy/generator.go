package caddy

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"text/template"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
)

// Generator generates Caddyfile configuration from templates and mappings
type Generator struct {
	cfg        *config.Config
	mappingMgr *mapping.Manager
	tmpl       *template.Template
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

// Generate creates the Caddyfile from template and current mappings
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

	// Prepare template data
	mappings := g.mappingMgr.Get()
	mappingData := make([]MappingData, 0, len(mappings))
	for _, m := range mappings {
		mappingData = append(mappingData, MappingData{
			Subdomain: m.Subdomain,
			Target:    m.GetTarget(),
			Options:   m.Options,
		})
	}

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

	slog.Info("Generated Caddyfile", "path", g.cfg.CaddyFile, "mappings", len(mappings))

	// Reload Caddy (if running)
	if err := g.reloadCaddy(); err != nil {
		slog.Warn("Failed to reload Caddy", "error", err)
	}

	return nil
}

func (g *Generator) reloadCaddy() error {
	// Send SIGUSR1 to Caddy to trigger config reload
	// This is handled by the entrypoint script which manages both processes
	slog.Debug("Caddy reload requested")
	return nil
}
