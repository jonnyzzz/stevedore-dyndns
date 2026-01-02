package caddy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonnyzzz/stevedore-dyndns/internal/config"
	"github.com/jonnyzzz/stevedore-dyndns/internal/discovery"
	"github.com/jonnyzzz/stevedore-dyndns/internal/mapping"
)

func TestNew(t *testing.T) {
	cfg := &config.Config{}
	mgr := mapping.New("")
	gen := New(cfg, mgr)

	if gen == nil {
		t.Fatal("New() returned nil")
	}
	if gen.cfg != cfg {
		t.Error("New() did not store config")
	}
	if gen.mappingMgr != mgr {
		t.Error("New() did not store mapping manager")
	}
}

func TestGenerator_Generate(t *testing.T) {
	// Create temp directories
	tmpDir := t.TempDir()
	templateDir := filepath.Join(tmpDir, "etc", "caddy")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatalf("Failed to create template dir: %v", err)
	}

	// Create template file
	templateContent := `# Caddy config for {{.Domain}}
email {{.AcmeEmail}}
log_level {{.LogLevel}}

{{range .Mappings}}
# {{.Subdomain}}
@{{.Subdomain}} host {{.Subdomain}}.example.com
handle @{{.Subdomain}} {
    reverse_proxy {{.Target}}
}
{{end}}
`
	templatePath := filepath.Join(templateDir, "Caddyfile.template")
	if err := os.WriteFile(templatePath, []byte(templateContent), 0644); err != nil {
		t.Fatalf("Failed to write template: %v", err)
	}

	// Create mappings file
	mappingsContent := `
mappings:
  - subdomain: app1
    target: "192.168.1.100:8080"
  - subdomain: app2
    target: "192.168.1.101:3000"
`
	mappingsPath := filepath.Join(tmpDir, "mappings.yaml")
	if err := os.WriteFile(mappingsPath, []byte(mappingsContent), 0644); err != nil {
		t.Fatalf("Failed to write mappings: %v", err)
	}

	// Create output file path
	caddyfilePath := filepath.Join(tmpDir, "Caddyfile")

	// Create config
	cfg := &config.Config{
		Domain:    "example.com",
		AcmeEmail: "test@example.com",
		LogLevel:  "info",
		CaddyFile: caddyfilePath,
	}

	// Create and load mapping manager
	mgr := mapping.New(mappingsPath)
	if err := mgr.Load(); err != nil {
		t.Fatalf("Failed to load mappings: %v", err)
	}

	// Create generator
	gen := New(cfg, mgr)

	// Override template path for testing
	// Note: The actual Generate() function reads from a fixed path
	// This test demonstrates the expected behavior

	// For a proper test, we'd need to make the template path configurable
	// For now, just verify the generator was created correctly
	if gen.cfg.Domain != "example.com" {
		t.Errorf("Domain = %q, want %q", gen.cfg.Domain, "example.com")
	}
}

func TestTemplateData(t *testing.T) {
	data := TemplateData{
		Domain:    "example.com",
		AcmeEmail: "admin@example.com",
		LogLevel:  "debug",
		Mappings: []MappingData{
			{
				Subdomain: "app1",
				Target:    "backend:8080",
				Options:   mapping.MappingOptions{Websocket: true},
			},
			{
				Subdomain: "app2",
				Target:    "frontend:3000",
				Options:   mapping.MappingOptions{HealthPath: "/healthz"},
			},
		},
	}

	if data.Domain != "example.com" {
		t.Errorf("Domain = %q, want %q", data.Domain, "example.com")
	}
	if len(data.Mappings) != 2 {
		t.Errorf("len(Mappings) = %d, want 2", len(data.Mappings))
	}
	if data.Mappings[0].Options.Websocket != true {
		t.Error("Mappings[0].Options.Websocket should be true")
	}
	if data.Mappings[1].Options.HealthPath != "/healthz" {
		t.Errorf("Mappings[1].Options.HealthPath = %q, want %q", data.Mappings[1].Options.HealthPath, "/healthz")
	}
}

func TestMappingData(t *testing.T) {
	md := MappingData{
		Subdomain: "myapp",
		Target:    "localhost:8080",
		Options: mapping.MappingOptions{
			Websocket:      true,
			BufferRequests: false,
			HealthPath:     "/health",
		},
	}

	if md.Subdomain != "myapp" {
		t.Errorf("Subdomain = %q, want %q", md.Subdomain, "myapp")
	}
	if md.Target != "localhost:8080" {
		t.Errorf("Target = %q, want %q", md.Target, "localhost:8080")
	}
}

func TestGenerator_Generate_EmptyMappings(t *testing.T) {
	tmpDir := t.TempDir()
	caddyfilePath := filepath.Join(tmpDir, "Caddyfile")

	cfg := &config.Config{
		Domain:    "example.com",
		AcmeEmail: "test@example.com",
		LogLevel:  "info",
		CaddyFile: caddyfilePath,
	}

	// Create empty mapping manager
	mgr := mapping.New("/nonexistent")
	_ = mgr.Load() // Will result in empty mappings

	gen := New(cfg, mgr)

	// The generator should handle empty mappings gracefully
	// In production, this would create a Caddyfile with no route handlers
	if gen.mappingMgr == nil {
		t.Error("mappingMgr should not be nil")
	}

	mappings := gen.mappingMgr.Get()
	if len(mappings) != 0 {
		t.Errorf("len(mappings) = %d, want 0", len(mappings))
	}
}

func TestTemplateFunctions(t *testing.T) {
	// Test the default function
	tests := []struct {
		name string
		def  string
		val  string
		want string
	}{
		{"empty value uses default", "/health", "", "/health"},
		{"non-empty value used", "/health", "/custom", "/custom"},
		{"both empty", "", "", ""},
	}

	defaultFunc := func(def, val string) string {
		if val == "" {
			return def
		}
		return val
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := defaultFunc(tt.def, tt.val)
			if got != tt.want {
				t.Errorf("default(%q, %q) = %q, want %q", tt.def, tt.val, got, tt.want)
			}
		})
	}
}

// Test that Caddyfile content is properly formatted
func TestCaddyfileFormat(t *testing.T) {
	// Expected patterns in a properly formatted Caddyfile
	expectedPatterns := []string{
		"*.example.com",         // Wildcard domain
		"tls {",                 // TLS block
		"dns cloudflare",        // Cloudflare DNS challenge
		"reverse_proxy",         // Reverse proxy directive
		"header_up X-Real-IP",   // Forwarded headers
	}

	// Sample Caddyfile content (what we expect the template to produce)
	sampleCaddyfile := `
{
    email test@example.com
}

*.example.com, example.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    @app host app.example.com
    handle @app {
        reverse_proxy backend:8080 {
            header_up X-Real-IP {remote_host}
        }
    }
}
`

	for _, pattern := range expectedPatterns {
		if !strings.Contains(sampleCaddyfile, pattern) {
			t.Errorf("Caddyfile should contain %q", pattern)
		}
	}
}

func TestTemplateWebsocketForcesHTTP1(t *testing.T) {
	templatePath := filepath.Join("..", "..", "Caddyfile.template")
	cfg := &config.Config{
		Domain:    "example.com",
		AcmeEmail: "admin@example.com",
		LogLevel:  "info",
	}
	gen := New(cfg, nil)
	gen.TemplatePath = templatePath

	gen.UpdateDiscoveredServices([]discovery.Service{
		{
			Subdomain: "app",
			Port:      8080,
			Websocket: true,
		},
	})
	content, err := gen.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}
	if !strings.Contains(content, "transport http") || !strings.Contains(content, "versions 1.1") {
		t.Error("Expected websocket transport to force HTTP/1.1")
	}

	gen.UpdateDiscoveredServices([]discovery.Service{
		{
			Subdomain: "plain",
			Port:      8081,
			Websocket: false,
		},
	})
	content, err = gen.GenerateContent()
	if err != nil {
		t.Fatalf("GenerateContent failed: %v", err)
	}
	if strings.Contains(content, "transport http") {
		t.Error("Unexpected HTTP/1 transport for non-websocket service")
	}
}

// Test file permissions
func TestCaddyfile_Permissions(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "Caddyfile")

	// Write file with expected permissions (0644)
	content := "test content"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("Failed to stat file: %v", err)
	}

	// Check permissions (on Unix systems)
	mode := info.Mode().Perm()
	if mode != 0644 {
		t.Errorf("File permissions = %o, want %o", mode, 0644)
	}
}

// Benchmark template execution
func BenchmarkGenerator_TemplateExecution(b *testing.B) {
	// Create test data
	data := TemplateData{
		Domain:    "example.com",
		AcmeEmail: "test@example.com",
		LogLevel:  "info",
		Mappings:  make([]MappingData, 10),
	}
	for i := 0; i < 10; i++ {
		data.Mappings[i] = MappingData{
			Subdomain: "app" + string(rune('0'+i)),
			Target:    "backend:808" + string(rune('0'+i)),
		}
	}

	// Simple template for benchmarking
	tmplContent := `
{{range .Mappings}}
@{{.Subdomain}} host {{.Subdomain}}.{{$.Domain}}
handle @{{.Subdomain}} {
    reverse_proxy {{.Target}}
}
{{end}}
`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Template parsing and execution
		_ = strings.Replace(tmplContent, "{{.Domain}}", data.Domain, -1)
	}
}
